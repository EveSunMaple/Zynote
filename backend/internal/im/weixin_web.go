package im

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const weixinWebAppID = "wx782c26e4c19acffb"

var weixinHosts = []string{"wx.qq.com", "wx2.qq.com", "wx8.qq.com", "web.wechat.com", "web2.wechat.com"}
var weixinCookieHosts = []string{"wx.qq.com", "wx2.qq.com", "wx8.qq.com", "web.wechat.com", "web2.wechat.com", "qq.com", "wechat.com", "weixin.qq.com", "login.wx.qq.com", "login.weixin.qq.com"}

type WeixinLogin struct {
	WxUin      string `json:"wxuin"`
	WxSid      string `json:"wxsid"`
	SKey       string `json:"skey"`
	PassTicket string `json:"pass_ticket,omitempty"`
}

type WeixinBaseRequest struct {
	Uin      int64  `json:"Uin"`
	Sid      string `json:"Sid"`
	Skey     string `json:"Skey"`
	DeviceID string `json:"DeviceID"`
}

type WeixinSyncKey struct {
	Count int `json:"Count"`
	List  []struct {
		Key int `json:"Key"`
		Val int `json:"Val"`
	} `json:"List"`
}

type WeixinWebSession struct {
	Domain      string            `json:"domain"`
	Login       WeixinLogin       `json:"login"`
	BaseRequest WeixinBaseRequest `json:"BaseRequest"`
	User        string            `json:"user,omitempty"`
	SyncKey     *WeixinSyncKey    `json:"SyncKey,omitempty"`
	Cookies     []map[string]any  `json:"cookies"`
	jar         *cookiejar.Jar
	uuid        string
	qr          string
}

func validWeixinIdentity(session *WeixinWebSession) bool {
	if !contains(weixinHosts, session.Domain) || session.Login.WxUin == "" ||
		!digitsValue(session.Login.WxUin, 1, 20) || session.Login.WxSid == "" ||
		len([]rune(session.Login.WxSid)) > 8192 || len([]rune(session.Login.SKey)) > 8192 {
		return false
	}
	if strconv.FormatInt(session.BaseRequest.Uin, 10) != session.Login.WxUin ||
		session.BaseRequest.Sid != session.Login.WxSid || session.BaseRequest.Skey != session.Login.SKey ||
		!regexp.MustCompile(`^e\d{15}$`).MatchString(session.BaseRequest.DeviceID) {
		return false
	}
	return true
}

func loadWeixinCookies(session *WeixinWebSession) error {
	for _, item := range session.Cookies {
		name := text(item["name"])
		if name == "" {
			name = text(item["key"])
		}
		value := text(item["value"])
		domain := strings.TrimPrefix(strings.ToLower(text(item["domain"])), ".")
		path := text(item["path"])
		if path == "" {
			path = "/"
		}
		if name == "" || len([]rune(value)) > 16384 || !contains(weixinCookieHosts, domain) {
			return fmt.Errorf("保存的微信 Cookie 域无效")
		}
		cookie := &http.Cookie{Name: name, Value: value, Domain: domain, Path: path}
		if secure, ok := item["secure"].(bool); ok {
			cookie.Secure = secure
		}
		session.jar.SetCookies(&url.URL{Scheme: "https", Host: domain, Path: path}, []*http.Cookie{cookie})
	}
	session.Cookies = nil
	return nil
}

func NewWeixinWebSession(saved any) (*WeixinWebSession, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("微信 Cookie 存储不可用")
	}
	session := &WeixinWebSession{jar: jar}
	if saved == nil {
		return session, nil
	}
	// Preserve the saved JSON for round-trips and decode the typed view.
	encoded, _ := json.Marshal(saved)
	if json.Unmarshal(encoded, session) != nil {
		return nil, fmt.Errorf("保存的微信会话无效")
	}
	if !validWeixinIdentity(session) {
		return nil, fmt.Errorf("保存的微信身份不一致")
	}
	if err := loadWeixinCookies(session); err != nil {
		return nil, err
	}
	return session, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func digitsValue(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, item := range value {
		if item < '0' || item > '9' {
			return false
		}
	}
	return true
}

func (session *WeixinWebSession) export() []map[string]any {
	cookies := []map[string]any{}
	seen := map[string]bool{}
	for _, host := range weixinCookieHosts {
		for _, cookie := range session.jar.Cookies(&url.URL{Scheme: "https", Host: host, Path: "/"}) {
			key := cookie.Name + "|" + cookie.Value + "|" + cookie.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			cookies = append(cookies, map[string]any{
				"key": cookie.Name, "value": cookie.Value, "domain": host, "path": cookie.Path,
				"secure": cookie.Secure, "hostOnly": true,
			})
			if len(cookies) >= 200 {
				return cookies
			}
		}
	}
	return cookies
}

func (session *WeixinWebSession) Session() map[string]any {
	return map[string]any{
		"domain": session.Domain, "login": session.Login, "BaseRequest": session.BaseRequest,
		"user": session.User, "SyncKey": session.SyncKey, "cookies": session.export(),
	}
}

func (session *WeixinWebSession) client() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second, Jar: session.jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("微信服务不允许跳转") },
	}
}

func (session *WeixinWebSession) request(ctx context.Context, target *url.URL, body any) (string, error) {
	if target.Scheme != "https" || target.Port() != "" || target.User != nil ||
		!(contains(weixinHosts, target.Hostname()) || target.Hostname() == "login.wx.qq.com") {
		return "", fmt.Errorf("微信服务地址无效")
	}
	var reader io.Reader
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return "", fmt.Errorf("微信请求失败")
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Linux; U; UOS x86_64; zh-cn) AppleWebKit/537.36 Chrome/91.0.4472.124 Safari/537.36 UOSBrowser/6.0.1.1001")
	if body != nil {
		request.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	response, err := session.client().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("微信服务暂时无法连接")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("微信服务未成功响应")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return "", fmt.Errorf("微信服务响应超过大小上限")
	}
	return string(raw), nil
}

func (session *WeixinWebSession) serviceURL(path string, query map[string]string) (*url.URL, error) {
	if session.Domain == "" {
		return nil, fmt.Errorf("微信尚未取得会话")
	}
	target, err := url.Parse("https://" + session.Domain + "/cgi-bin/mmwebwx-bin/" + path)
	if err != nil {
		return nil, fmt.Errorf("微信服务地址无效")
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	target.RawQuery = values.Encode()
	return target, nil
}

func (session *WeixinWebSession) json(ctx context.Context, path string, body any, query map[string]string) (map[string]any, error) {
	target, err := session.serviceURL(path, query)
	if err != nil {
		return nil, err
	}
	raw, err := session.request(ctx, target, body)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil, fmt.Errorf("微信服务响应格式无效")
	}
	base, _ := value["BaseResponse"].(map[string]any)
	if int(number(base["Ret"])) != 0 {
		return nil, fmt.Errorf("微信会话失效或操作未确认")
	}
	return value, nil
}

func (session *WeixinWebSession) Start(ctx context.Context) (map[string]any, error) {
	target, _ := url.Parse("https://login.wx.qq.com/jslogin")
	query := url.Values{}
	query.Set("appid", weixinWebAppID)
	query.Set("fun", "new")
	query.Set("lang", "zh_CN")
	query.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	query.Set("redirect_uri", "https://wx.qq.com/cgi-bin/mmwebwx-bin/webwxnewloginpage")
	target.RawQuery = query.Encode()
	raw, err := session.request(ctx, target, nil)
	if err != nil {
		return nil, err
	}
	codes := regexp.MustCompile(`window\.QRLogin\.code\s*=\s*(\d+)\s*;`).FindAllStringSubmatch(raw, -1)
	uuids := regexp.MustCompile(`window\.QRLogin\.uuid\s*=\s*"([A-Za-z0-9_-]{1,128}={0,2})"\s*;`).FindAllStringSubmatch(raw, -1)
	if len(codes) != 1 || codes[0][1] != "200" || len(uuids) != 1 {
		return nil, fmt.Errorf("微信 Web 未提供有效二维码")
	}
	session.uuid = uuids[0][1]
	session.qr = "https://login.weixin.qq.com/l/" + session.uuid
	return map[string]any{"state": "waiting_scan", "qr": session.qr}, nil
}

func (session *WeixinWebSession) Poll(ctx context.Context) (map[string]any, error) {
	if session.uuid == "" {
		return nil, fmt.Errorf("请先开始微信扫码")
	}
	target, _ := url.Parse("https://login.wx.qq.com/cgi-bin/mmwebwx-bin/login")
	query := url.Values{}
	query.Set("uuid", session.uuid)
	query.Set("tip", "0")
	query.Set("loginicon", "true")
	now := time.Now().Unix()
	query.Set("_", strconv.FormatInt(now, 10))
	query.Set("r", strconv.FormatInt(now/1579, 10))
	target.RawQuery = query.Encode()
	raw, err := session.request(ctx, target, nil)
	if err != nil {
		return nil, err
	}
	codes := regexp.MustCompile(`window\.code\s*=\s*(\d+)\s*;`).FindAllStringSubmatch(raw, -1)
	if len(codes) != 1 {
		return nil, fmt.Errorf("微信登录状态无效")
	}
	switch codes[0][1] {
	case "408":
		return map[string]any{"state": "waiting_scan", "qr": session.qr}, nil
	case "201":
		return map[string]any{"state": "waiting_confirm", "qr": session.qr}, nil
	case "400":
		return map[string]any{"state": "expired", "qr": ""}, nil
	case "200":
	default:
		return nil, fmt.Errorf("微信未批准本次扫码")
	}
	redirects := regexp.MustCompile(`window\.redirect_uri\s*=\s*"([^"<>]{1,4096})"\s*;`).FindAllStringSubmatch(raw, -1)
	if len(redirects) != 1 {
		return nil, fmt.Errorf("微信确认地址无效")
	}
	target, err = weixinRedirect(redirects[0][1])
	if err != nil {
		return nil, err
	}
	raw, err = session.request(ctx, target, nil)
	if err != nil {
		return nil, err
	}
	login, err := session.parseLogin(raw, target.Hostname())
	if err != nil {
		return nil, err
	}
	uin, err := strconv.ParseInt(login.WxUin, 10, 64)
	if err != nil || uin <= 0 {
		return nil, fmt.Errorf("当前微信账号编号无法无损编码，请使用微信 Bot 连接")
	}
	session.Domain = target.Hostname()
	session.Login = login
	session.BaseRequest = WeixinBaseRequest{Uin: uin, Sid: login.WxSid, Skey: login.SKey, DeviceID: randomDeviceID()}
	session.qr = ""
	return map[string]any{"state": "verifying", "qr": ""}, nil
}

func randomDeviceID() string {
	buffer := make([]byte, 15)
	_, _ = rand.Read(buffer)
	var builder strings.Builder
	builder.WriteString("e")
	for _, value := range buffer {
		builder.WriteByte('0' + value%10)
	}
	return builder.String()
}

func weixinRedirect(value string) (*url.URL, error) {
	target, err := url.Parse(value)
	if err != nil || target.Scheme != "https" || !contains(weixinHosts, target.Hostname()) ||
		target.Port() != "" || target.User != nil || target.Fragment != "" ||
		target.Path != "/cgi-bin/mmwebwx-bin/webwxnewloginpage" {
		return nil, fmt.Errorf("微信返回不支持的确认地址")
	}
	parts := []string{}
	for _, part := range strings.Split(target.RawQuery, "&") {
		if part == "" {
			continue
		}
		key := strings.SplitN(part, "=", 2)[0]
		decoded, _ := url.QueryUnescape(strings.ReplaceAll(key, "+", " "))
		if decoded == "fun" || decoded == "version" {
			continue
		}
		parts = append(parts, part)
	}
	target.RawQuery = strings.Join(append(parts, "fun=new", "version=v2"), "&")
	return target, nil
}

func (session *WeixinWebSession) bootstrapLogin(domain, raw string) (WeixinLogin, error) {
	if !weixinBootstrap(raw) {
		return WeixinLogin{}, fmt.Errorf("微信返回的网页尚不能建立已验证会话")
	}
	host := "https://" + domain + "/cgi-bin/mmwebwx-bin/webwxinit"
	cookies := session.jar.Cookies(mustURL(host))
	values := map[string]string{}
	for _, cookie := range cookies {
		if cookie.Name == "wxuin" || cookie.Name == "wxsid" {
			if _, exists := values[cookie.Name]; exists {
				return WeixinLogin{}, fmt.Errorf("微信网页会话 Cookie 缺失或有歧义")
			}
			values[cookie.Name] = cookie.Value
		}
	}
	if values["wxuin"] == "" || values["wxsid"] == "" {
		return WeixinLogin{}, fmt.Errorf("微信网页会话 Cookie 缺失或有歧义")
	}
	return WeixinLogin{WxUin: values["wxuin"], WxSid: values["wxsid"], SKey: ""}, nil
}

func parseWeixinLoginXML(raw string) (WeixinLogin, error) {
	var value struct {
		XMLName    xml.Name `xml:"error"`
		Ret        string   `xml:"ret"`
		Skey       string   `xml:"skey"`
		Wxsid      string   `xml:"wxsid"`
		Wxuin      string   `xml:"wxuin"`
		PassTicket string   `xml:"pass_ticket"`
	}
	if xml.Unmarshal([]byte(raw), &value) != nil || value.Ret != "0" {
		return WeixinLogin{}, fmt.Errorf("微信未确认有效登录")
	}
	if !digitsValue(value.Wxuin, 1, 20) || value.Wxsid == "" || len([]rune(value.Skey)) > 8192 {
		return WeixinLogin{}, fmt.Errorf("微信未确认有效登录")
	}
	return WeixinLogin{WxUin: value.Wxuin, WxSid: value.Wxsid, SKey: value.Skey, PassTicket: value.PassTicket}, nil
}

func (session *WeixinWebSession) parseLogin(raw, domain string) (WeixinLogin, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return session.bootstrapLogin(domain, trimmed)
	}
	if regexp.MustCompile(`(?i)<!DOCTYPE|<!ENTITY`).MatchString(raw) {
		return WeixinLogin{}, fmt.Errorf("微信确认响应包含无效声明")
	}
	return parseWeixinLoginXML(raw)
}

func mustURL(value string) *url.URL {
	parsed, _ := url.Parse(value)
	return parsed
}

func weixinBootstrap(html string) bool {
	lower := strings.ToLower(html)
	return strings.Contains(lower, "<title>微信网页版</title>") &&
		strings.Contains(lower, `ng-controller="appcontroller"`) &&
		strings.Contains(lower, `ng-controller="logincontroller"`) &&
		strings.Contains(lower, "wx_fed/webwx/res/static/js/index_")
}

func (session *WeixinWebSession) Verify(ctx context.Context, expected string) (map[string]any, error) {
	if session.Domain == "" || session.Login.WxUin == "" || (expected != "" && session.Login.WxUin != expected) {
		return nil, fmt.Errorf("微信账号归属不符")
	}
	value, err := session.json(ctx, "webwxinit", map[string]any{"BaseRequest": session.BaseRequest}, map[string]string{
		"_": strconv.FormatInt(time.Now().Unix(), 10),
	})
	if err != nil {
		return nil, err
	}
	user, _ := value["User"].(map[string]any)
	userName := text(user["UserName"])
	if userName == "" || len([]rune(userName)) > 256 {
		return nil, fmt.Errorf("微信会话身份无效")
	}
	if raw := user["Uin"]; raw != nil && text(raw) != "" && text(raw) != "0" && number(raw) != float64(session.BaseRequest.Uin) {
		return nil, fmt.Errorf("微信初始化账号与扫码账号不一致")
	}
	if session.User != "" && session.User != userName {
		return nil, fmt.Errorf("微信会话身份已改变")
	}
	skey := text(value["SKey"])
	if skey == "" {
		skey = session.Login.SKey
	}
	if skey == "" || len([]rune(skey)) > 8192 {
		return nil, fmt.Errorf("微信会话身份无效")
	}
	syncKey, err := parseSyncKey(value["SyncKey"])
	if err != nil {
		return nil, err
	}
	session.User = userName
	session.Login.SKey = skey
	session.BaseRequest.Skey = skey
	if session.SyncKey == nil {
		session.SyncKey = syncKey
	}
	return map[string]any{
		"account": session.Login.WxUin, "session": session.Session(),
		"capabilities": map[string]any{
			"identity": "personal_weixin_web_experimental", "history": "local_since_connection",
			"messages": "new_events_only", "send": "explicit_confirmed_text",
		},
	}, nil
}

func parseSyncKey(raw any) (*WeixinSyncKey, error) {
	encoded, _ := json.Marshal(raw)
	var value WeixinSyncKey
	if json.Unmarshal(encoded, &value) != nil || value.Count < 1 || value.Count > 100 ||
		value.Count != len(value.List) || len(value.List) > 100 {
		return nil, fmt.Errorf("微信同步游标无效")
	}
	return &value, nil
}

func weixinDirectoryPeer(raw any, seen map[string]bool) (map[string]any, bool, error) {
	item, _ := raw.(map[string]any)
	peer := text(item["UserName"])
	if peer == "" || len([]rune(peer)) > 256 {
		return nil, false, fmt.Errorf("微信联系人无效")
	}
	if seen[peer] {
		return nil, false, nil
	}
	seen[peer] = true
	name := text(item["RemarkName"])
	if name == "" {
		name = text(item["DisplayName"])
	}
	if name == "" {
		name = text(item["NickName"])
	}
	if name == "" {
		name = peer
	}
	runes := []rune(name)
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return map[string]any{"peer": peer, "name": string(runes)}, true, nil
}

func appendWeixinDirectoryPage(members []any, seen map[string]bool, peers []map[string]any) ([]map[string]any, error) {
	for _, raw := range members {
		peer, ok, err := weixinDirectoryPeer(raw, seen)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		peers = append(peers, peer)
		if len(peers) > 10000 {
			return nil, fmt.Errorf("微信联系人超出本次读取上限")
		}
	}
	return peers, nil
}

func (session *WeixinWebSession) Directory(ctx context.Context) (map[string]any, error) {
	if session.Domain == "" {
		return nil, fmt.Errorf("微信尚未核验身份")
	}
	peers := []map[string]any{}
	seen := map[string]bool{}
	seenSeq := map[int]bool{}
	seq := 0
	complete := false
	for page := 0; page < 20; page++ {
		value, err := session.json(ctx, "webwxgetcontact", nil, map[string]string{
			"r": strconv.FormatInt(time.Now().UnixMilli(), 10), "skey": session.Login.SKey, "seq": strconv.Itoa(seq),
		})
		if err != nil {
			return nil, err
		}
		members, _ := value["MemberList"].([]any)
		if len(members) > 10000 {
			return nil, fmt.Errorf("微信联系人超出本次读取上限")
		}
		peers, err = appendWeixinDirectoryPage(members, seen, peers)
		if err != nil {
			return nil, err
		}
		next := int(number(value["Seq"]))
		if next == 0 {
			complete = true
			break
		}
		if next == seq || seenSeq[next] {
			return nil, fmt.Errorf("微信联系人分页未前进")
		}
		seenSeq[seq] = true
		seq = next
	}
	coverage := "partial_page_limit"
	if complete {
		coverage = "complete_current_directory"
	}
	return map[string]any{"peers": peers, "coverage": coverage}, nil
}

type WeixinUpdates struct {
	Messages []Message
	Session  map[string]any
}

func weixinSyncValue(ctx context.Context, session *WeixinWebSession) (map[string]any, error) {
	query := map[string]string{"sid": session.Login.WxSid, "skey": session.Login.SKey}
	if session.Login.PassTicket != "" {
		query["pass_ticket"] = session.Login.PassTicket
	}
	return session.json(ctx, "webwxsync", map[string]any{
		"BaseRequest": session.BaseRequest, "SyncKey": session.SyncKey,
		"rr": strconv.FormatInt(time.Now().Unix(), 10),
	}, query)
}

func weixinMessage(raw any, session *WeixinWebSession, selected string) (Message, bool, error) {
	item, _ := raw.(map[string]any)
	sender := text(item["FromUserName"])
	to := text(item["ToUserName"])
	if sender != session.User && to != session.User {
		return Message{}, false, nil
	}
	peer := sender
	if sender == session.User {
		peer = to
	}
	if peer != selected {
		return Message{}, false, nil
	}
	mid, err := messageID(item["MsgId"])
	if err != nil {
		mid, err = messageID(item["NewMsgId"])
	}
	if err != nil || mid == "" {
		return Message{}, false, fmt.Errorf("微信消息编号无效")
	}
	ts := number(item["CreateTime"])
	if ts < 0 {
		return Message{}, false, fmt.Errorf("微信消息时间无效")
	}
	content := "[未读取的图片、语音或其他消息]"
	kind := "unsupported_media"
	if number(item["MsgType"]) == 1 {
		content = text(item["Content"])
		kind = "text"
		if len([]rune(content)) > 50000 {
			content = string([]rune(content)[:50000])
		}
	}
	return Message{MID: mid, TS: ts, Sender: sender, Text: content, Kind: kind}, true, nil
}

func (session *WeixinWebSession) Receive(ctx context.Context, selected, account string) (WeixinUpdates, error) {
	if session.User == "" || session.SyncKey == nil || session.Login.WxUin != account {
		return WeixinUpdates{}, fmt.Errorf("微信所选会话尚未核验")
	}
	value, err := weixinSyncValue(ctx, session)
	if err != nil {
		return WeixinUpdates{}, err
	}
	key, err := parseSyncKey(value["SyncKey"])
	if err != nil {
		return WeixinUpdates{}, err
	}
	rows, _ := value["AddMsgList"].([]any)
	if len(rows) > 5000 {
		return WeixinUpdates{}, fmt.Errorf("微信消息数量超过上限")
	}
	messages := []Message{}
	for _, raw := range rows {
		message, ok, err := weixinMessage(raw, session, selected)
		if err != nil {
			return WeixinUpdates{}, err
		}
		if ok {
			messages = append(messages, message)
		}
	}
	session.SyncKey = key
	return WeixinUpdates{Messages: messages, Session: session.Session()}, nil
}

func (session *WeixinWebSession) Send(ctx context.Context, peer, content, operationID string) (string, error) {
	if session.User == "" {
		return "", fmt.Errorf("微信尚未核验身份")
	}
	sum := sha256.Sum256([]byte(operationID))
	numberValue := new(big.Int)
	numberValue.SetString(hex.EncodeToString(sum[:])[:16], 16)
	numberValue.Mod(numberValue, new(big.Int).Lsh(big.NewInt(1), 63))
	localID := numberValue.String()
	query := map[string]string{"lang": "zh_CN"}
	if session.Login.PassTicket != "" {
		query["pass_ticket"] = session.Login.PassTicket
	}
	value, err := session.json(ctx, "webwxsendmsg", map[string]any{
		"BaseRequest": session.BaseRequest,
		"Msg": map[string]any{
			"Type": 1, "Content": content, "FromUserName": session.User, "ToUserName": peer,
			"LocalID": localID, "ClientMsgId": localID,
		},
		"Scene": 0,
	}, query)
	if err != nil {
		return "", err
	}
	if value["MsgID"] == nil {
		return "", nil
	}
	return messageID(value["MsgID"])
}
