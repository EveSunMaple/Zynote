package im

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const ilinkBase = "https://ilinkai.weixin.qq.com"

type ILinkSession struct {
	Token    string
	Account  string
	User     string
	Base     string
	Cursor   string
	Contexts map[string]string
	qrcode   string
	qr       string
	verify   string
}

func NewILinkSession(saved any) (*ILinkSession, error) {
	session := &ILinkSession{Contexts: map[string]string{}}
	if saved == nil {
		return session, nil
	}
	value, _ := saved.(map[string]any)
	session.Token = text(value["token"])
	session.Account = text(value["account"])
	session.User = text(value["user"])
	session.Base = text(value["base"])
	session.Cursor = text(value["cursor"])
	if contexts, ok := value["contexts"].(map[string]any); ok {
		for key, item := range contexts {
			session.Contexts[key] = text(item)
		}
	}
	if session.Token == "" || session.Account == "" || session.User == "" ||
		!validILinkBase(session.Base) || len([]rune(session.Cursor)) > 1000000 {
		return nil, fmt.Errorf("微信Bot会话无效")
	}
	return session, nil
}

func validILinkBase(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "ilinkai.weixin.qq.com" &&
		(parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == "" &&
		parsed.User == nil
}

func (session *ILinkSession) Session() map[string]any {
	return map[string]any{
		"token": session.Token, "account": session.Account, "user": session.User,
		"base": session.Base, "cursor": session.Cursor, "contexts": session.Contexts,
	}
}

func randomUin() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return base64.StdEncoding.EncodeToString([]byte("0"))
	}
	value := binary.BigEndian.Uint32(buffer)
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", value)))
}

func (session *ILinkSession) request(ctx context.Context, path, method string, body any, query map[string]string) (map[string]any, error) {
	switch path {
	case "get_bot_qrcode", "get_qrcode_status", "getconfig", "getupdates", "sendmessage":
	default:
		return nil, fmt.Errorf("微信Bot操作无效")
	}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	target := ilinkBase + "/ilink/bot/" + path
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	var reader io.Reader
	if method == http.MethodPost {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("微信Bot请求失败")
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("AuthorizationType", "ilink_bot_token")
	request.Header.Set("iLink-App-Id", "bot")
	request.Header.Set("iLink-App-ClientVersion", "132106")
	request.Header.Set("X-WECHAT-UIN", randomUin())
	if session.Token != "" {
		request.Header.Set("Authorization", "Bearer "+session.Token)
	}
	value, err := PublicJSON(ctx, request, 4*1024*1024)
	if err != nil {
		return nil, fmt.Errorf("微信Bot接口未成功，请稍后检查连接")
	}
	for _, key := range []string{"ret", "errcode"} {
		if raw, ok := value[key]; ok {
			if number(raw) != 0 {
				if int(number(raw)) == -14 {
					return nil, fmt.Errorf("微信Bot会话已失效，请重新扫码")
				}
				return nil, fmt.Errorf("微信Bot接口返回错误，未确认完成")
			}
		}
	}
	return value, nil
}

func (session *ILinkSession) Start(ctx context.Context) (map[string]any, error) {
	value, err := session.request(ctx, "get_bot_qrcode", http.MethodPost, map[string]any{"local_token_list": []any{}}, map[string]string{"bot_type": "3"})
	if err != nil {
		return nil, err
	}
	qrcode := text(value["qrcode"])
	content := text(value["qrcode_img_content"])
	if qrcode == "" {
		return nil, fmt.Errorf("微信Bot二维码无效")
	}
	parsed, err := url.Parse(content)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		!(parsed.Hostname() == "weixin.qq.com" || strings.HasSuffix(parsed.Hostname(), ".weixin.qq.com")) {
		return nil, fmt.Errorf("微信Bot二维码网址未核验")
	}
	session.qrcode = qrcode
	session.qr = parsed.String()
	return map[string]any{"state": "waiting_scan", "qr": session.qr}, nil
}

func (session *ILinkSession) Poll(ctx context.Context) (map[string]any, error) {
	query := map[string]string{"qrcode": session.qrcode}
	if session.verify != "" {
		query["verify_code"] = session.verify
	}
	value, err := session.request(ctx, "get_qrcode_status", http.MethodGet, nil, query)
	if err != nil {
		return nil, err
	}
	status := text(value["status"])
	switch status {
	case "wait":
		return map[string]any{"state": "waiting_scan", "qr": session.qr}, nil
	case "scaned":
		session.verify = ""
		return map[string]any{"state": "scanned", "qr": session.qr}, nil
	case "need_verifycode":
		return map[string]any{"state": "waiting_confirm", "qr": "", "detail": map[string]any{"message": "请输入手机微信显示的数字验证码。"}}, nil
	case "expired":
		return map[string]any{"state": "expired", "qr": ""}, nil
	case "verify_code_blocked", "binded_redirect":
		return nil, fmt.Errorf("微信Bot此次扫码未取得有效会话，请重新开始")
	case "scaned_but_redirect":
		if text(value["redirect_host"]) != "ilinkai.weixin.qq.com" {
			return nil, fmt.Errorf("微信Bot返回不支持的区域地址")
		}
		return map[string]any{"state": "scanned", "qr": session.qr}, nil
	case "confirmed":
		base := text(value["baseurl"])
		if !validILinkBase(base) {
			return nil, fmt.Errorf("微信Bot返回不支持的区域地址")
		}
		session.Token = text(value["bot_token"])
		session.Account = text(value["ilink_bot_id"])
		session.User = text(value["ilink_user_id"])
		session.Base = base
		session.Cursor = ""
		session.Contexts = map[string]string{}
		session.qr = ""
		return map[string]any{"state": "verifying", "qr": ""}, nil
	}
	return nil, fmt.Errorf("微信Bot返回未知登录状态")
}

func (session *ILinkSession) SubmitCode(code string) error {
	if !regexpDigits(code, 1, 12) {
		return fmt.Errorf("微信验证码无效")
	}
	session.verify = code
	return nil
}

func regexpDigits(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, item := range value {
		if item < '0' || item > '9' {
			return false
		}
	}
	return true
}

func (session *ILinkSession) Verify(ctx context.Context, expected string) (map[string]any, error) {
	if session.Token == "" || (expected != "" && session.Account != expected) {
		return nil, fmt.Errorf("微信Bot账号已改变或未确认")
	}
	value, err := session.request(ctx, "getconfig", http.MethodPost, map[string]any{
		"ilink_user_id": session.User,
		"base_info":     map[string]any{"channel_version": "2.4.10", "bot_agent": "Zynote"},
	}, map[string]string{})
	if err != nil {
		return nil, err
	}
	if number(value["ret"]) != 0 {
		return nil, fmt.Errorf("微信Bot未明确确认在线状态")
	}
	return map[string]any{
		"account": session.Account, "session": session.Session(),
		"capabilities": map[string]any{
			"identity": "weixin_ilink_bot", "history": "local_since_connection",
			"messages": "direct_new_events_only", "send": "explicit_confirmed_text",
		},
	}, nil
}

func (session *ILinkSession) Directory(ctx context.Context) (map[string]any, error) {
	if session.Token == "" {
		return nil, fmt.Errorf("微信Bot未登录")
	}
	pending, err := session.Receive(ctx, "", session.Account)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	peers := []map[string]any{}
	for peer := range session.Contexts {
		seen[peer] = true
	}
	for _, peer := range pending.Peers {
		seen[peer] = true
	}
	for peer := range seen {
		peers = append(peers, map[string]any{"peer": peer, "name": peer})
	}
	return map[string]any{"peers": peers, "coverage": "仅本次可取得的会话标识；不保存其他会话正文，不推进消息游标，不代表全部联系人"}, nil
}

type ILinkUpdates struct {
	Messages []Message
	Peers    []string
}

func (session *ILinkSession) Receive(ctx context.Context, selected, account string) (ILinkUpdates, error) {
	if session.Token == "" || session.Account != account {
		return ILinkUpdates{}, fmt.Errorf("微信Bot消息账号不符")
	}
	value, err := session.request(ctx, "getupdates", http.MethodPost, map[string]any{
		"get_updates_buf": session.Cursor,
		"base_info":       map[string]any{"channel_version": "2.4.10", "bot_agent": "Zynote"},
	}, map[string]string{})
	if err != nil {
		return ILinkUpdates{}, err
	}
	rows, _ := value["msgs"].([]any)
	if len(rows) > 5000 {
		return ILinkUpdates{}, fmt.Errorf("微信Bot消息数量超过上限")
	}
	contexts := map[string]string{}
	for key, item := range session.Contexts {
		contexts[key] = item
	}
	messages := []Message{}
	peers := []string{}
	for _, row := range rows {
		item, _ := row.(map[string]any)
		from := text(item["from_user_id"])
		to := text(item["to_user_id"])
		peer := from
		if from == account {
			peer = to
		}
		if from == "" || to == "" {
			continue
		}
		peers = append(peers, peer)
		if peer != selected {
			continue
		}
		if token := text(item["context_token"]); token != "" {
			contexts[peer] = token
		}
		mid := text(item["message_id"])
		if mid == "" || len([]rune(mid)) > 200 {
			return ILinkUpdates{}, fmt.Errorf("微信Bot消息编号无效")
		}
		ts := number(item["create_time_ms"]) / 1000
		if ts < 0 {
			return ILinkUpdates{}, fmt.Errorf("微信Bot消息时间无效")
		}
		parts, _ := item["item_list"].([]any)
		texts := []string{}
		for _, part := range parts {
			partValue, _ := part.(map[string]any)
			if textItem, ok := partValue["text_item"].(map[string]any); ok {
				texts = append(texts, text(textItem["text"]))
			} else {
				texts = append(texts, "[未读取的媒体消息]")
			}
		}
		content := strings.Join(texts, "\n")
		runes := []rune(content)
		if len(runes) > 50000 {
			runes = runes[:50000]
		}
		messages = append(messages, Message{MID: mid, TS: ts, Sender: from, Text: string(runes), Kind: "text"})
	}
	session.Cursor = text(value["get_updates_buf"])
	if session.Cursor == "" {
		session.Cursor = ""
	}
	session.Contexts = contexts
	return ILinkUpdates{Messages: messages, Peers: peers}, nil
}

func (session *ILinkSession) Send(ctx context.Context, peer, content, operationID string) (string, error) {
	token := session.Contexts[peer]
	if token == "" {
		return "", fmt.Errorf("请先同步所选联系人的入站消息再回复")
	}
	value, err := session.request(ctx, "sendmessage", http.MethodPost, map[string]any{
		"msg": map[string]any{
			"from_user_id": "", "to_user_id": peer, "client_id": operationID,
			"message_type": 2, "message_state": 2, "context_token": token,
			"item_list": []any{map[string]any{"type": 1, "text_item": map[string]any{"text": content}}},
		},
		"base_info": map[string]any{"channel_version": "2.4.10", "bot_agent": "Zynote"},
	}, map[string]string{})
	if err != nil {
		return "", err
	}
	if errmsg := text(value["errmsg"]); errmsg != "" && errmsg != "ok" && errmsg != "success" {
		return "", fmt.Errorf("微信Bot未明确确认发送")
	}
	mid := text(value["message_id"])
	if len([]rune(mid)) > 200 {
		return "", fmt.Errorf("微信Bot发送回执无效")
	}
	return mid, nil
}
