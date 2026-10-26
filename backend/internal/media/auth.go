package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const mediaVaultLimit = 8 * 1024 * 1024

var cookieScopePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
var cookieExpiresPattern = regexp.MustCompile(`^\d{1,13}$`)
var cookieNameForbidden = regexp.MustCompile(`[\x00-\x20\x7f;,=]`)
var cookieValueForbidden = regexp.MustCompile(`[\x00-\x1f\x7f;]`)
var cookiePathForbidden = regexp.MustCompile(`[\x00-\x20\x7f;]`)
var profileNameForbidden = regexp.MustCompile(`[\x00-\x1f]`)
var mediaProfileIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type MediaCookie struct {
	Domain   string
	Path     string
	Name     string
	Value    string
	Secure   bool
	HTTPOnly bool
	HostOnly bool
	Expires  int64
}

type CookieParseResult struct {
	Text    string
	Count   int
	Cookies []MediaCookie
}

func MediaCookieScope(value string) (string, error) {
	site := strings.ToLower(strings.TrimSpace(value))
	site = strings.Trim(site, ".")
	if !cookieScopePattern.MatchString(site) ||
		site == "co.uk" || site == "com.cn" || site == "com.au" {
		return "", contract.InputInvalid("请填写具体站点域名，不含协议、端口或路径")
	}
	return site, nil
}

func cookieBelongs(host, site string) bool {
	return host == site || strings.HasSuffix(host, "."+site)
}

func ParseMediaCookies(text, scope string) (CookieParseResult, error) {
	site, err := MediaCookieScope(scope)
	if err != nil {
		return CookieParseResult{}, err
	}
	if len(text) > 1_048_576 {
		return CookieParseResult{}, fmt.Errorf("Cookie 文件超过 1 MB")
	}
	rows := []string{}
	cookies := []MediaCookie{}
	seen := map[string]bool{}
	now := time.Now().Unix()
	normalized := strings.ReplaceAll(strings.TrimPrefix(text, "\uFEFF"), "\r\n", "\n")
	for _, raw := range strings.Split(normalized, "\n") {
		httpOnly := strings.HasPrefix(raw, "#HttpOnly_")
		line := raw
		if httpOnly {
			line = raw[10:]
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cells := strings.Split(line, "\t")
		if len(cells) != 7 {
			return CookieParseResult{}, fmt.Errorf("请使用 Netscape cookies.txt 格式")
		}
		rawDomain := cells[0]
		domain := strings.ToLower(strings.TrimPrefix(rawDomain, "."))
		if !cookieBelongs(domain, site) {
			continue
		}
		subs, path, secure, expires, name, value := cells[1], cells[2], cells[3], cells[4], cells[5], cells[6]
		if (subs != "TRUE" && subs != "FALSE") || (secure != "TRUE" && secure != "FALSE") ||
			!cookieExpiresPattern.MatchString(expires) || !strings.HasPrefix(path, "/") ||
			cookieNameForbidden.MatchString(name) || name == "" || len(name) > 256 ||
			len(value) > 16384 || cookieValueForbidden.MatchString(value) ||
			cookiePathForbidden.MatchString(path) {
			return CookieParseResult{}, fmt.Errorf("Cookie 字段格式无效")
		}
		seconds, _ := strconv.ParseInt(expires, 10, 64)
		if seconds > 0 && seconds <= now {
			continue
		}
		dedupeKey := domain + "\n" + path + "\n" + name
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true
		rawDomainLower := strings.ToLower(rawDomain)
		displayDomain := rawDomainLower
		if httpOnly {
			displayDomain = "#HttpOnly_" + rawDomainLower
		}
		rows = append(rows, strings.Join([]string{
			displayDomain, subs, path, secure, expires, name, value,
		}, "\t"))
		cookies = append(cookies, MediaCookie{
			Domain: domain, Path: path, Name: name, Value: value,
			Secure: secure == "TRUE", HTTPOnly: httpOnly, HostOnly: subs == "FALSE", Expires: seconds,
		})
	}
	if len(rows) == 0 || len(rows) > 2000 {
		return CookieParseResult{}, fmt.Errorf("没有匹配该域名且未过期的 Cookie")
	}
	return CookieParseResult{
		Text:    "# Netscape HTTP Cookie File\n" + strings.Join(rows, "\n") + "\n",
		Count:   len(rows),
		Cookies: cookies,
	}, nil
}

type MediaProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Site      string `json:"site"`
	Cookies   string `json:"cookies"`
	Count     int    `json:"count"`
	UpdatedAt int64  `json:"updated_at"`
}

type MediaAuth struct {
	root  string
	crypt Crypt
	mu    chan struct{}
}

func NewMediaAuth(root string, crypt Crypt) *MediaAuth {
	if crypt == nil {
		crypt = func(data []byte, decrypt bool) ([]byte, error) { return data, nil }
	}
	if err := os.MkdirAll(root, 0o755); err == nil {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
	}
	return &MediaAuth{root: root, crypt: crypt, mu: make(chan struct{}, 1)}
}

func (auth *MediaAuth) path() (string, error) {
	folder := filepath.Join(auth.root, "download-auth")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("登录档案目录无效")
	}
	info, err := os.Lstat(folder)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("登录档案目录无效")
	}
	real, err := filepath.EvalSymlinks(folder)
	if err != nil || strings.ToLower(real) != strings.ToLower(folder) {
		return "", fmt.Errorf("登录档案目录无效")
	}
	return filepath.Join(folder, "profiles.dpapi"), nil
}

func (auth *MediaAuth) readLocked() (map[string]MediaProfile, error) {
	path, err := auth.path()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MediaProfile{}, nil
		}
		return nil, fmt.Errorf("登录档案不能在当前系统用户下读取，请重新导入")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > mediaVaultLimit {
		return nil, fmt.Errorf("登录档案文件无效")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("登录档案不能在当前系统用户下读取，请重新导入")
	}
	plain, err := auth.crypt(raw, true)
	if err != nil {
		return nil, fmt.Errorf("登录档案不能在当前系统用户下读取，请重新导入")
	}
	rows := map[string]MediaProfile{}
	if err := json.Unmarshal(plain, &rows); err != nil {
		return nil, fmt.Errorf("登录档案不能在当前系统用户下读取，请重新导入")
	}
	for id, row := range rows {
		if !mediaProfileIDPattern.MatchString(id) || row.ID != id ||
			len(strings.TrimSpace(row.Name)) < 1 || len(row.Name) > 80 ||
			len(row.Site) > 253 || len(row.Cookies) > 1_048_576 ||
			row.Count < 1 || row.Count > 2000 || row.UpdatedAt < 0 {
			return nil, fmt.Errorf("登录档案不能在当前系统用户下读取，请重新导入")
		}
	}
	return rows, nil
}

func (auth *MediaAuth) public(row MediaProfile) map[string]any {
	return map[string]any{
		"id": row.ID, "name": row.Name, "site": row.Site,
		"count": row.Count, "updated_at": row.UpdatedAt,
	}
}

func (auth *MediaAuth) List() ([]map[string]any, error) {
	auth.mu <- struct{}{}
	defer func() { <-auth.mu }()
	rows, err := auth.readLocked()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, row := range rows {
		result = append(result, auth.public(row))
	}
	return result, nil
}

type SaveProfileInput struct {
	Name    string
	Site    string
	Cookies string
	ID      string
}

func (auth *MediaAuth) Save(input SaveProfileInput) (map[string]any, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 80 || profileNameForbidden.MatchString(name) {
		return nil, contract.InputInvalid("请填写档案名称")
	}
	if input.ID != "" && !mediaProfileIDPattern.MatchString(input.ID) {
		return nil, contract.InputInvalid("登录档案标识无效")
	}
	site, err := MediaCookieScope(input.Site)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseMediaCookies(input.Cookies, site)
	if err != nil {
		return nil, err
	}
	auth.mu <- struct{}{}
	defer func() { <-auth.mu }()
	rows, err := auth.readLocked()
	if err != nil {
		return nil, err
	}
	if input.ID != "" && rows[input.ID].ID == "" {
		return nil, contract.NewError(contract.CodeNotFound, "所选登录档案不存在")
	}
	if input.ID == "" && len(rows) >= 50 {
		return nil, contract.NewError(contract.CodeConflict, "最多保存 50 个站点档案")
	}
	id := input.ID
	if id == "" {
		id = strings.ReplaceAll(ids.New(), "-", "")
	}
	row := MediaProfile{
		ID: id, Name: name, Site: site, Cookies: parsed.Text,
		Count: parsed.Count, UpdatedAt: time.Now().Unix(),
	}
	rows[id] = row
	if err := auth.writeLocked(rows); err != nil {
		return nil, err
	}
	return auth.public(row), nil
}

func (auth *MediaAuth) Remove(id string) (bool, error) {
	if !mediaProfileIDPattern.MatchString(id) {
		return false, contract.InputInvalid("登录档案标识无效")
	}
	auth.mu <- struct{}{}
	defer func() { <-auth.mu }()
	rows, err := auth.readLocked()
	if err != nil {
		return false, err
	}
	if rows[id].ID == "" {
		return false, contract.NewError(contract.CodeNotFound, "所选登录档案不存在")
	}
	delete(rows, id)
	if err := auth.writeLocked(rows); err != nil {
		return false, err
	}
	return true, nil
}

func (auth *MediaAuth) writeLocked(rows map[string]MediaProfile) error {
	path, err := auth.path()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("登录档案保存失败")
	}
	sealed, err := auth.crypt(encoded, false)
	if err != nil {
		return fmt.Errorf("系统密钥库不可用或凭据格式无效，未写入凭据")
	}
	if len(sealed) > mediaVaultLimit {
		return fmt.Errorf("登录档案加密后超过 8 MB，未保存")
	}
	temporary := path + "." + ids.New() + ".writing"
	if err := os.WriteFile(temporary, sealed, 0o600); err != nil {
		return fmt.Errorf("登录档案保存失败")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("登录档案保存失败")
	}
	return nil
}

type AuthFetcher func(requestURL string, headers map[string]string, ctx context.Context) (*http.Response, error)

func (auth *MediaAuth) CookieHeader(profileID, site string) (string, error) {
	if !mediaProfileIDPattern.MatchString(profileID) {
		return "", contract.InputInvalid("登录档案标识无效")
	}
	auth.mu <- struct{}{}
	rows, err := auth.readLocked()
	<-auth.mu
	if err != nil {
		return "", err
	}
	selected := rows[profileID]
	if selected.ID == "" {
		return "", contract.InputInvalid("登录档案不属于当前视频站点")
	}
	if selected.Site != site {
		return "", contract.InputInvalid("登录档案不属于当前视频站点")
	}
	parsed, err := ParseMediaCookies(selected.Cookies, selected.Site)
	if err != nil {
		return "", err
	}
	pairs := make([]string, 0, len(parsed.Cookies))
	for _, cookie := range parsed.Cookies {
		pairs = append(pairs, cookie.Name+"="+cookie.Value)
	}
	if len(pairs) == 0 {
		return "", contract.InputInvalid("登录档案没有可用 Cookie")
	}
	return strings.Join(pairs, "; "), nil
}

func (auth *MediaAuth) Scoped(requestURL string, options NativeMediaOptions, base AuthFetcher) (AuthFetcher, error) {
	if !options.UseCookies || options.CookieProfile == "" {
		return base, nil
	}
	if !mediaProfileIDPattern.MatchString(options.CookieProfile) {
		return nil, contract.InputInvalid("登录档案标识无效")
	}
	auth.mu <- struct{}{}
	rows, err := auth.readLocked()
	<-auth.mu
	if err != nil {
		return nil, err
	}
	selected := rows[options.CookieProfile]
	if selected.ID == "" {
		return nil, contract.InputInvalid("登录档案不属于当前视频站点")
	}
	parsed, err := ParseMediaCookies(selected.Cookies, selected.Site)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(requestURL)
	if err != nil || !cookieBelongs(target.Hostname(), selected.Site) {
		return nil, contract.InputInvalid("登录档案不属于当前视频站点")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, contract.Unavailable("登录档案不可用")
	}
	jarCookies := []*http.Cookie{}
	for _, cookie := range parsed.Cookies {
		jarCookies = append(jarCookies, &http.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
			Secure: cookie.Secure, HttpOnly: cookie.HTTPOnly,
		})
	}
	jar.SetCookies(target, jarCookies)
	return func(input string, headers map[string]string, ctx context.Context) (*http.Response, error) {
		targetURL, err := url.Parse(input)
		if err != nil {
			return nil, contract.InputInvalid("下载地址无效")
		}
		copied := map[string]string{}
		for key, value := range headers {
			lower := strings.ToLower(key)
			if lower == "cookie" || lower == "authorization" {
				continue
			}
			copied[key] = value
		}
		if targetURL.Scheme == "https" && targetURL.User == nil && targetURL.Port() == "" &&
			cookieBelongs(targetURL.Hostname(), selected.Site) {
			cookies := jar.Cookies(targetURL)
			if len(cookies) > 0 {
				pairs := make([]string, 0, len(cookies))
				for _, cookie := range cookies {
					pairs = append(pairs, cookie.Name+"="+cookie.Value)
				}
				copied["Cookie"] = strings.Join(pairs, "; ")
			}
		}
		return base(input, copied, ctx)
	}, nil
}
