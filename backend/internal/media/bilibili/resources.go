package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const (
	apiReplyMain       = "/x/v2/reply/wbi/main"
	apiWebInterfaceNav = "/x/web-interface/nav"
	errBiliLoginRead   = "B 站登录状态不可读取"
	errBiliLoginSave   = "B 站登录状态不可保存"
)

type Crypt = func(data []byte, decrypt bool) ([]byte, error)

type storedCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"http_only"`
	Expires  int64  `json:"expires"`
}

type cookieStore struct {
	mu      sync.Mutex
	path    string
	crypt   Crypt
	cookies []storedCookie
	loaded  bool
}

func newCookieStore(path string, crypt Crypt) *cookieStore {
	if crypt == nil {
		crypt = func(data []byte, decrypt bool) ([]byte, error) { return data, nil }
	}
	return &cookieStore{path: path, crypt: crypt}
}

func (store *cookieStore) load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loaded {
		return nil
	}
	store.loaded = true
	raw, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return contract.Unavailable(errBiliLoginRead)
	}
	plain, err := store.crypt(raw, true)
	if err != nil {
		return contract.Unavailable(errBiliLoginRead)
	}
	var cookies []storedCookie
	if err := json.Unmarshal(plain, &cookies); err != nil {
		return contract.Unavailable(errBiliLoginRead)
	}
	store.cookies = cookies
	return nil
}

func (store *cookieStore) saveLocked() error {
	encoded, err := json.Marshal(store.cookies)
	if err != nil {
		return contract.Unavailable(errBiliLoginSave)
	}
	sealed, err := store.crypt(encoded, false)
	if err != nil {
		return contract.Unavailable("系统密钥库不可用，未保存 B 站登录状态")
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		return contract.Unavailable(errBiliLoginSave)
	}
	temporary := store.path + ".writing"
	if err := os.WriteFile(temporary, sealed, 0o600); err != nil {
		return contract.Unavailable(errBiliLoginSave)
	}
	if err := os.Rename(temporary, store.path); err != nil {
		_ = os.Remove(temporary)
		return contract.Unavailable(errBiliLoginSave)
	}
	return nil
}

func hostMatches(host, domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func (store *cookieStore) header(host, requestPath string, secure bool) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().Unix()
	pairs := []string{}
	for _, cookie := range store.cookies {
		if cookie.Expires > 0 && cookie.Expires <= now {
			continue
		}
		if cookie.Secure && !secure {
			continue
		}
		if !hostMatches(host, cookie.Domain) {
			continue
		}
		if cookie.Path != "" && cookie.Path != "/" && !strings.HasPrefix(requestPath, cookie.Path) {
			continue
		}
		pairs = append(pairs, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(pairs, "; ")
}

func (store *cookieStore) setFromResponse(host string, response *http.Response) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	for _, raw := range response.Header.Values("Set-Cookie") {
		header := http.Header{}
		header.Add("Set-Cookie", raw)
		parsed := (&http.Response{Header: header}).Cookies()
		for _, cookie := range parsed {
			domain := cookie.Domain
			if domain == "" {
				domain = host
			}
			path := cookie.Path
			if path == "" {
				path = "/"
			}
			expires := int64(0)
			if !cookie.Expires.IsZero() {
				expires = cookie.Expires.Unix()
			}
			if cookie.MaxAge < 0 || (expires > 0 && expires <= now.Unix()) {
				store.removeLocked(domain, path, cookie.Name)
				continue
			}
			store.upsertLocked(storedCookie{
				Name: cookie.Name, Value: cookie.Value, Domain: strings.TrimPrefix(domain, "."),
				Path: path, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, Expires: expires,
			})
		}
	}
	_ = store.saveLocked()
}

func (store *cookieStore) upsertLocked(cookie storedCookie) {
	for index := range store.cookies {
		existing := &store.cookies[index]
		if existing.Name == cookie.Name && hostMatches(existing.Domain, cookie.Domain) && existing.Path == cookie.Path {
			*existing = cookie
			return
		}
	}
	store.cookies = append(store.cookies, cookie)
}

func (store *cookieStore) removeLocked(domain, path, name string) {
	kept := store.cookies[:0]
	for _, cookie := range store.cookies {
		if cookie.Name == name && cookie.Path == path && hostMatches(cookie.Domain, domain) {
			continue
		}
		kept = append(kept, cookie)
	}
	store.cookies = kept
}

func (store *cookieStore) clear() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.cookies = []storedCookie{}
	if err := os.Remove(store.path); err != nil && !os.IsNotExist(err) {
		return contract.Unavailable("B 站登录状态不可清除")
	}
	return nil
}

var biliHeaders = map[string]string{
	"User-Agent": "Mozilla/5.0",
	"Referer":    "https://www.bilibili.com/",
}

type wbiKey struct {
	value   string
	expires int64
}

type qrState struct {
	key     string
	expires int64
}

type Resources struct {
	client     *http.Client
	cookies    *cookieStore
	fetch      func(*http.Request) (*http.Response, error)
	mu         sync.Mutex
	qr         *qrState
	signingKey *wbiKey
	baseAPI    string
	basePass   string
}

func NewResources(root string, crypt Crypt) *Resources {
	store := newCookieStore(filepath.Join(root, "bili-session", "cookies.dpapi"), crypt)
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			return fmt.Errorf("B 站请求不允许重定向")
		},
	}
	return &Resources{
		client:   client,
		cookies:  store,
		baseAPI:  "https://api.bilibili.com",
		basePass: "https://passport.bilibili.com",
	}
}

func (resources *Resources) read(requestPath string, params map[string]string, passport bool) (any, error) {
	return resources.readContext(context.Background(), requestPath, params, passport)
}

var discussionReadPaths = map[string]bool{
	"/x/web-interface/view": true,
	"/x/v2/reply":           true,
	"/x/v2/reply/reply":     true,
	apiReplyMain:            true,
}

func (resources *Resources) ReadDiscussion(requestPath string, params map[string]string, ctx context.Context, signed bool) (any, error) {
	if !discussionReadPaths[requestPath] {
		return nil, fmt.Errorf("讨论读取地址无效")
	}
	if signed {
		value, err := resources.sign(params)
		if err != nil {
			return nil, err
		}
		params = value
	}
	return resources.readContext(ctx, requestPath, params, false)
}

func (resources *Resources) RawDiscussion(requestPath string, params map[string]string, ctx context.Context, limit int64) ([]byte, error) {
	if resources.cookies != nil {
		if err := resources.cookies.load(); err != nil {
			return nil, err
		}
	}
	target := resources.baseAPI + requestPath
	if len(params) > 0 {
		pairs := make([]string, 0, len(params))
		for key, value := range params {
			pairs = append(pairs, urlEncode(key)+"="+urlEncode(value))
		}
		target += "?" + strings.Join(pairs, "&")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("下载地址无效")
	}
	for key, value := range biliHeaders {
		request.Header.Set(key, value)
	}
	if resources.cookies != nil {
		if cookie := resources.cookies.header(request.URL.Hostname(), request.URL.Path, request.URL.Scheme == "https"); cookie != "" {
			request.Header.Set("Cookie", cookie)
		}
	}
	response, err := resources.do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("network_or_protocol")
	}
	defer response.Body.Close()
	resources.cookies.setFromResponse(request.URL.Hostname(), response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("danmaku_too_large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("danmaku_too_large")
	}
	return body, nil
}

func (resources *Resources) do(request *http.Request) (*http.Response, error) {
	if resources.fetch != nil {
		return resources.fetch(request)
	}
	return resources.client.Do(request)
}

type ScopedFetcher func(url string, headers map[string]string, ctx context.Context) (*http.Response, error)

func NewScopedResources(fetcher ScopedFetcher) *Resources {
	return &Resources{
		fetch: func(request *http.Request) (*http.Response, error) {
			headers := map[string]string{}
			for key := range request.Header {
				headers[key] = request.Header.Get(key)
			}
			return fetcher(request.URL.String(), headers, request.Context())
		},
		baseAPI:  "https://api.bilibili.com",
		basePass: "https://passport.bilibili.com",
	}
}

func (resources *Resources) readContext(ctx context.Context, requestPath string, params map[string]string, passport bool) (any, error) {
	if resources.cookies != nil {
		if err := resources.cookies.load(); err != nil {
			return nil, err
		}
	}
	base := resources.baseAPI
	if passport {
		base = resources.basePass
	}
	target := base + requestPath
	if len(params) > 0 {
		pairs := make([]string, 0, len(params))
		for key, value := range params {
			pairs = append(pairs, urlEncode(key)+"="+urlEncode(value))
		}
		target += "?" + strings.Join(pairs, "&")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, contract.InputInvalid("B 站请求地址无效")
	}
	for key, value := range biliHeaders {
		request.Header.Set(key, value)
	}
	if resources.cookies != nil {
		if cookie := resources.cookies.header(request.URL.Hostname(), request.URL.Path, request.URL.Scheme == "https"); cookie != "" {
			request.Header.Set("Cookie", cookie)
		}
	}
	response, err := resources.do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, contract.Unavailable("B 站服务暂不可用，请稍后重试")
	}
	defer response.Body.Close()
	if resources.cookies != nil {
		resources.cookies.setFromResponse(request.URL.Hostname(), response)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, contract.Unavailable(fmt.Sprintf("B 站服务暂不可用（HTTP %d）", response.StatusCode))
	}
	var payload struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, contract.Unavailable("B 站服务返回格式异常，请稍后重试")
	}
	if requestPath == apiWebInterfaceNav && payload.Code == -101 {
		result := map[string]any{"isLogin": false}
		if len(payload.Data) > 0 {
			_ = json.Unmarshal(payload.Data, &result)
			result["isLogin"] = false
		}
		return result, nil
	}
	if payload.Code != 0 {
		if payload.Code == -101 {
			return nil, contract.Unavailable("B 站登录已失效，请重新扫码")
		}
		return nil, contract.Unavailable(fmt.Sprintf("B 站读取失败（%d），请稍后重试", payload.Code))
	}
	var data any
	if len(payload.Data) > 0 {
		_ = json.Unmarshal(payload.Data, &data)
	}
	return data, nil
}

var mediaReadPaths = map[string]bool{
	"/x/web-interface/view": true,
	"/x/player/wbi/playurl": true,
	"/x/player/v2":          true,
	"/x/v2/reply":           true,
	"/x/v2/reply/reply":     true,
	apiReplyMain:            true,
}

func (resources *Resources) ReadMedia(requestPath string, params map[string]string) (any, error) {
	if !mediaReadPaths[requestPath] {
		return nil, contract.InputInvalid("媒体读取地址无效")
	}
	if requestPath == "/x/player/wbi/playurl" || requestPath == apiReplyMain {
		signed, err := resources.sign(params)
		if err != nil {
			return nil, err
		}
		return resources.read(requestPath, signed, false)
	}
	return resources.read(requestPath, params, false)
}

func (resources *Resources) Status() (map[string]any, error) {
	data, err := resources.read(apiWebInterfaceNav, nil, false)
	if err != nil {
		return nil, err
	}
	user, _ := data.(map[string]any)
	loggedIn, _ := user["isLogin"].(bool)
	result := map[string]any{"loggedIn": loggedIn, "name": ""}
	if loggedIn {
		name, _ := user["uname"].(string)
		if name == "" {
			name = "B站账号"
		}
		result["name"] = name
		if mid, ok := user["mid"]; ok {
			result["mid"] = mid
		}
	}
	return result, nil
}

var wbiOrder = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38,
	41, 13,
}

func (resources *Resources) sign(params map[string]string) (map[string]string, error) {
	resources.mu.Lock()
	key := resources.signingKey
	resources.mu.Unlock()
	if key == nil || key.expires <= time.Now().UnixMilli() {
		data, err := resources.read(apiWebInterfaceNav, nil, false)
		if err != nil {
			return nil, err
		}
		user, _ := data.(map[string]any)
		wbi, _ := user["wbi_img"].(map[string]any)
		if wbi == nil {
			return params, nil
		}
		imgURL, _ := wbi["img_url"].(string)
		subURL, _ := wbi["sub_url"].(string)
		keys := wbiPart(imgURL) + wbiPart(subURL)
		if len(keys) != 64 || !isHex(keys) {
			return nil, contract.Unavailable("B 站搜索签名无效，请稍后重试")
		}
		mixed := make([]byte, 0, 32)
		for _, index := range wbiOrder {
			if index < len(keys) {
				mixed = append(mixed, keys[index])
			}
		}
		key = &wbiKey{value: string(mixed), expires: time.Now().UnixMilli() + 3600000}
		resources.mu.Lock()
		resources.signingKey = key
		resources.mu.Unlock()
	}
	signed := map[string]string{}
	for name, value := range params {
		signed[name] = strings.NewReplacer("!", "", "'", "", "(", "", ")", "", "*", "").Replace(value)
	}
	signed["wts"] = fmt.Sprintf("%d", time.Now().Unix())
	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sortStrings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, urlEncode(name)+"="+strings.ReplaceAll(urlEncode(signed[name]), "%20", "+"))
	}
	encoded := strings.Join(pairs, "&")
	signed["w_rid"] = md5Hex(encoded + key.value)
	return signed, nil
}

func wbiPart(value string) string {
	path := value
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	segments := strings.Split(path, "/")
	last := segments[len(segments)-1]
	if index := strings.Index(last, "."); index >= 0 {
		last = last[:index]
	}
	return last
}

func (resources *Resources) Browse(kind, query string, page int) (map[string]any, error) {
	if page < 1 || page > 100 || len(query) > 100 || (kind != "search" && kind != "popular") {
		return nil, contract.InputInvalid("视频检索参数无效")
	}
	if kind == "search" && strings.TrimSpace(query) == "" {
		return nil, contract.InputInvalid("请输入搜索关键词")
	}
	params := map[string]string{}
	if kind == "search" {
		signed, err := resources.sign(map[string]string{
			"keyword": query, "search_type": "video", "page": fmt.Sprintf("%d", page),
		})
		if err != nil {
			return nil, err
		}
		params = signed
	}
	path := "/x/web-interface/popular"
	if kind == "search" {
		path = "/x/web-interface/search/type"
	}
	data, err := resources.read(path, params, false)
	if err != nil {
		return nil, err
	}
	response, _ := data.(map[string]any)
	if response == nil {
		return nil, contract.Unavailable("B 站视频列表格式异常，请稍后重试")
	}
	var raw []any
	if kind == "popular" {
		raw, _ = response["list"].([]any)
	} else {
		raw, _ = response["result"].([]any)
	}
	if raw == nil {
		return nil, contract.Unavailable("B 站视频列表格式异常，请稍后重试")
	}
	videos := []map[string]any{}
	limit := len(raw)
	if limit > 50 {
		limit = 50
	}
	for _, item := range raw[:limit] {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		bvid, _ := row["bvid"].(string)
		if !bvidPattern.MatchString(bvid) {
			continue
		}
		title, _ := row["title"].(string)
		author, _ := row["author"].(string)
		if author == "" {
			if owner, ok := row["owner"].(map[string]any); ok {
				author, _ = owner["name"].(string)
			}
		}
		videos = append(videos, map[string]any{
			"bvid": bvid, "title": clipRunes(htmlUnescape(stripTags(title)), 300),
			"author": clipRunes(author, 100), "duration": durationText(row["duration"]),
			"url": "https://www.bilibili.com/video/" + bvid + "/",
		})
	}
	hasMore := false
	if kind != "popular" {
		pages := 1
		if value, ok := response["numPages"].(float64); ok {
			pages = int(value)
		}
		if pages > 100 {
			pages = 100
		}
		hasMore = page < pages
	}
	return map[string]any{"videos": videos, "page": page, "hasMore": hasMore}, nil
}

func (resources *Resources) Begin() (string, error) {
	data, err := resources.read("/x/passport-login/web/qrcode/generate", nil, true)
	if err != nil {
		return "", err
	}
	row, _ := data.(map[string]any)
	key, _ := row["qrcode_key"].(string)
	qrURL, _ := row["url"].(string)
	if key == "" || qrURL == "" {
		return "", contract.Unavailable("B 站扫码服务暂不可用，请重新生成二维码")
	}
	if !allowedQRURL(qrURL) {
		return "", contract.Unavailable("扫码地址不是 B 站官方地址")
	}
	resources.mu.Lock()
	resources.qr = &qrState{key: key, expires: time.Now().UnixMilli() + 180000}
	resources.mu.Unlock()
	return qrURL, nil
}

func (resources *Resources) Poll() (map[string]any, error) {
	resources.mu.Lock()
	current := resources.qr
	if current == nil || current.expires <= time.Now().UnixMilli() {
		resources.qr = nil
		resources.mu.Unlock()
		return map[string]any{"status": "expired"}, nil
	}
	resources.mu.Unlock()
	data, err := resources.read("/x/passport-login/web/qrcode/poll", map[string]string{"qrcode_key": current.key}, true)
	if err != nil {
		return nil, err
	}
	resources.mu.Lock()
	unchanged := resources.qr == current
	if !unchanged {
		resources.mu.Unlock()
		return map[string]any{"status": "expired"}, nil
	}
	resources.mu.Unlock()
	row, _ := data.(map[string]any)
	code := -1
	if value, ok := row["code"].(float64); ok {
		code = int(value)
	}
	status, known := map[int]string{0: "success", 86101: "waiting", 86090: "scanning", 86038: "expired"}[code]
	if !known {
		return nil, contract.Unavailable("B 站扫码服务暂不可用，请重新生成二维码")
	}
	if code == 0 || code == 86038 {
		resources.mu.Lock()
		resources.qr = nil
		resources.mu.Unlock()
	}
	return map[string]any{"status": status}, nil
}

func (resources *Resources) CancelLogin() {
	resources.mu.Lock()
	resources.qr = nil
	resources.mu.Unlock()
}

func (resources *Resources) Logout() (map[string]any, error) {
	resources.CancelLogin()
	if err := resources.cookies.clear(); err != nil {
		return nil, err
	}
	return map[string]any{"loggedIn": false}, nil
}

func (resources *Resources) Folders() ([]map[string]any, error) {
	status, err := resources.Status()
	if err != nil {
		return nil, err
	}
	loggedIn, _ := status["loggedIn"].(bool)
	mid, _ := status["mid"].(float64)
	if !loggedIn || mid <= 0 {
		return nil, contract.Unavailable("请先扫码登录 B 站")
	}
	data, err := resources.read("/x/v3/fav/folder/created/list-all", map[string]string{
		"up_mid": fmt.Sprintf("%d", int64(mid)),
	}, false)
	if err != nil {
		return nil, err
	}
	row, _ := data.(map[string]any)
	list, _ := row["list"].([]any)
	folders := []map[string]any{}
	for _, item := range list {
		folder, _ := item.(map[string]any)
		if folder == nil {
			continue
		}
		id, _ := folder["id"].(float64)
		title, _ := folder["title"].(string)
		count, _ := folder["media_count"].(float64)
		if id < 1 {
			continue
		}
		folders = append(folders, map[string]any{"id": int64(id), "title": title, "media_count": int64(count)})
	}
	return folders, nil
}

func (resources *Resources) Videos(id int64, page int) (map[string]any, error) {
	if id < 1 || page < 1 || page > 1000 {
		return nil, contract.InputInvalid("收藏夹分页无效")
	}
	folders, err := resources.Folders()
	if err != nil {
		return nil, err
	}
	owned := false
	for _, folder := range folders {
		if value, ok := folder["id"].(int64); ok && value == id {
			owned = true
		}
	}
	if !owned {
		return nil, contract.InputInvalid("收藏夹不属于当前账号，请刷新列表")
	}
	data, err := resources.read("/x/v3/fav/resource/list", map[string]string{
		"media_id": fmt.Sprintf("%d", id), "pn": fmt.Sprintf("%d", page), "ps": "20",
	}, false)
	if err != nil {
		return nil, err
	}
	row, _ := data.(map[string]any)
	medias, _ := row["medias"].([]any)
	hasMore, _ := row["has_more"].(bool)
	videos := []map[string]any{}
	for _, item := range medias {
		mediaRow, _ := item.(map[string]any)
		if mediaRow == nil {
			continue
		}
		bvid, _ := mediaRow["bvid"].(string)
		title, _ := mediaRow["title"].(string)
		kind, _ := mediaRow["type"].(float64)
		available := kind == 2 && bvidPattern.MatchString(bvid) && title != "已失效视频"
		url := ""
		if available {
			url = "https://www.bilibili.com/video/" + bvid + "/"
		}
		videos = append(videos, map[string]any{
			"bvid": bvid, "title": title, "duration": mediaRow["duration"],
			"type": kind, "id": mediaRow["id"], "available": available, "url": url,
		})
	}
	return map[string]any{"videos": videos, "hasMore": hasMore, "page": page}, nil
}
