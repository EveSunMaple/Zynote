package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var qqNumber = regexp.MustCompile(`^\d{1,18}$`)

type QqConfig struct {
	URL     string
	Token   string
	Account string
}

type QqSession struct {
	Config QqConfig
}

func parseQqConfig(raw any) (QqConfig, error) {
	value, _ := raw.(map[string]any)
	origin, err := Loopback(text(value["url"]))
	if err != nil {
		return QqConfig{}, err
	}
	token := text(value["token"])
	if len([]rune(token)) > 8192 {
		return QqConfig{}, fmt.Errorf("QQ 接口令牌无效")
	}
	account := text(value["account"])
	if account != "" && !qqNumber.MatchString(account) {
		return QqConfig{}, fmt.Errorf("QQ 账号无效")
	}
	return QqConfig{URL: origin, Token: token, Account: account}, nil
}

func NewQqSession(saved any) (*QqSession, error) {
	value, _ := saved.(map[string]any)
	onebot, ok := value["onebot"]
	if !ok {
		onebot = value
	}
	config, err := parseQqConfig(onebot)
	if err != nil {
		return nil, err
	}
	return &QqSession{Config: config}, nil
}

func (session *QqSession) action(ctx context.Context, name string, body any) (any, error) {
	switch name {
	case "get_login_info", "get_status", "get_friend_list", "get_group_list",
		"get_friend_msg_history", "get_group_msg_history", "send_private_msg", "send_group_msg":
	default:
		return nil, fmt.Errorf("此 QQ 操作未启用")
	}
	value, err := LocalJSON(ctx, session.Config.URL, "/"+name, body, session.Config.Token)
	if err != nil {
		return nil, err
	}
	if text(value["status"]) != "ok" || int(number(value["retcode"])) != 0 {
		return nil, fmt.Errorf("QQ 消息接口未确认请求")
	}
	return value["data"], nil
}

func qqAccount(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		if qqNumber.MatchString(typed) {
			return typed, nil
		}
	case float64:
		textValue := fmt.Sprintf("%.0f", typed)
		if typed > 0 && qqNumber.MatchString(textValue) {
			return textValue, nil
		}
	case json.Number:
		if qqNumber.MatchString(typed.String()) {
			return typed.String(), nil
		}
	}
	return "", fmt.Errorf("QQ 账号无效")
}

func (session *QqSession) Verify(ctx context.Context, expected string) (string, error) {
	login, err := session.action(ctx, "get_login_info", map[string]any{})
	if err != nil {
		return "", err
	}
	loginValue, _ := login.(map[string]any)
	account, err := qqAccount(loginValue["user_id"])
	if err != nil {
		return "", err
	}
	status, err := session.action(ctx, "get_status", map[string]any{})
	if err != nil {
		return "", err
	}
	statusValue, _ := status.(map[string]any)
	if statusValue["online"] != true {
		return "", fmt.Errorf("QQ 账号或在线状态已改变，未读取或发送消息")
	}
	if good, ok := statusValue["good"].(bool); ok && !good {
		return "", fmt.Errorf("QQ 账号或在线状态已改变，未读取或发送消息")
	}
	if account != expected || (session.Config.Account != "" && session.Config.Account != expected) {
		return "", fmt.Errorf("QQ 账号或在线状态已改变，未读取或发送消息")
	}
	return account, nil
}

func (session *QqSession) Identity(ctx context.Context) (string, error) {
	login, err := session.action(ctx, "get_login_info", map[string]any{})
	if err != nil {
		return "", err
	}
	value, _ := login.(map[string]any)
	account, err := qqAccount(value["user_id"])
	if err != nil {
		return "", err
	}
	if _, err := session.Verify(ctx, account); err != nil {
		return "", err
	}
	return account, nil
}

func qqDirectoryPeer(row any, group bool) (map[string]any, error) {
	value, _ := row.(map[string]any)
	idKey, nameKey := "user_id", "nickname"
	if group {
		idKey, nameKey = "group_id", "group_name"
	}
	id, err := qqAccount(value[idKey])
	if err != nil {
		return nil, err
	}
	label := text(value[nameKey])
	if label == "" {
		label = id
	}
	labelRunes := []rune(label)
	if len(labelRunes) > 200 {
		labelRunes = labelRunes[:200]
	}
	prefix := "private:"
	if group {
		prefix = "group:"
	}
	return map[string]any{"peer": prefix + id, "name": string(labelRunes)}, nil
}

func (session *QqSession) directoryPage(ctx context.Context, group bool) ([]map[string]any, error) {
	name := "get_friend_list"
	if group {
		name = "get_group_list"
	}
	raw, err := session.action(ctx, name, map[string]any{})
	if err != nil {
		return nil, err
	}
	rows, _ := raw.([]any)
	if len(rows) > 5000 {
		return nil, fmt.Errorf("QQ 联系人数量超过上限")
	}
	peers := []map[string]any{}
	for _, row := range rows {
		peer, err := qqDirectoryPeer(row, group)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func (session *QqSession) Directory(ctx context.Context) (map[string]any, error) {
	peers := []map[string]any{}
	for _, group := range []bool{false, true} {
		page, err := session.directoryPage(ctx, group)
		if err != nil {
			return nil, err
		}
		peers = append(peers, page...)
	}
	return map[string]any{"peers": peers}, nil
}

func qqPeer(value string) (bool, string, error) {
	match := regexp.MustCompile(`^(private|group):(\d{1,18})$`).FindStringSubmatch(value)
	if match == nil {
		return false, "", fmt.Errorf("请选择 QQ 好友或群")
	}
	return match[1] == "group", match[2], nil
}

func qqMessageText(raw any) (string, error) {
	if value, ok := raw.(string); ok {
		return value, nil
	}
	parts, ok := raw.([]any)
	if !ok {
		return "", fmt.Errorf("QQ 消息格式无效")
	}
	values := []string{}
	for _, part := range parts {
		value, _ := part.(map[string]any)
		kind := text(value["type"])
		data, _ := value["data"].(map[string]any)
		if kind == "text" {
			values = append(values, text(data["text"]))
		} else {
			values = append(values, "["+kind+"]")
		}
	}
	return strings.Join(values, ""), nil
}

func qqHistoryRequest(group bool, peerID string) (string, map[string]any) {
	body := map[string]any{"count": 50}
	if group {
		body["group_id"] = peerID
		return "get_group_msg_history", body
	}
	body["user_id"] = peerID
	return "get_friend_msg_history", body
}

func qqHistoryMessage(row any, group bool, account, peerID string) (Message, error) {
	item, _ := row.(map[string]any)
	if self, ok := item["self_id"]; ok && self != nil {
		parsed, err := qqAccount(self)
		if err != nil || parsed != account {
			return Message{}, fmt.Errorf("QQ 历史回包账号或所选会话不一致，未保存")
		}
	}
	key := "user_id"
	if group {
		key = "group_id"
	}
	if value, ok := item[key]; ok && value != nil {
		parsed, err := qqAccount(value)
		if err != nil || parsed != peerID {
			return Message{}, fmt.Errorf("QQ 历史回包账号或所选会话不一致，未保存")
		}
	}
	textValue, err := qqMessageText(item["message"])
	if err != nil {
		return Message{}, err
	}
	mid, err := messageID(item["message_id"])
	if err != nil {
		return Message{}, fmt.Errorf("QQ 消息编号无效")
	}
	ts := number(item["time"])
	if ts < 0 {
		return Message{}, fmt.Errorf("QQ 消息时间无效")
	}
	sender := ""
	if value, ok := item["sender"].(map[string]any); ok {
		sender = text(value["user_id"])
	}
	textRunes := []rune(textValue)
	if len(textRunes) > 50000 {
		textRunes = textRunes[:50000]
	}
	return Message{MID: mid, TS: ts, Sender: sender, Text: string(textRunes), Kind: "text"}, nil
}

func (session *QqSession) Receive(ctx context.Context, selected, account string) ([]Message, error) {
	group, peerID, err := qqPeer(selected)
	if err != nil {
		return nil, err
	}
	action, body := qqHistoryRequest(group, peerID)
	raw, err := session.action(ctx, action, body)
	if err != nil {
		return nil, err
	}
	value, _ := raw.(map[string]any)
	rows, _ := value["messages"].([]any)
	if len(rows) > 5000 {
		return nil, fmt.Errorf("QQ 历史消息数量超过上限")
	}
	messages := []Message{}
	for _, row := range rows {
		message, err := qqHistoryMessage(row, group, account, peerID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func messageID(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		if len([]rune(typed)) > 200 {
			return "", fmt.Errorf("消息编号无效")
		}
		return typed, nil
	case float64:
		return fmt.Sprintf("%.0f", typed), nil
	case json.Number:
		return typed.String(), nil
	}
	return "", fmt.Errorf("消息编号无效")
}

func (session *QqSession) Send(ctx context.Context, selected, content string) (string, error) {
	group, peerID, err := qqPeer(selected)
	if err != nil {
		return "", err
	}
	body := map[string]any{"message": []any{map[string]any{"type": "text", "data": map[string]any{"text": content}}}}
	action := "send_private_msg"
	if group {
		body["group_id"] = peerID
		action = "send_group_msg"
	} else {
		body["user_id"] = peerID
	}
	raw, err := session.action(ctx, action, body)
	if err != nil {
		return "", err
	}
	value, _ := raw.(map[string]any)
	switch mid := value["message_id"].(type) {
	case string:
		if mid == "" || len([]rune(mid)) > 200 {
			return "", fmt.Errorf("QQ 发送回执无效")
		}
		return mid, nil
	case float64:
		return fmt.Sprintf("%.0f", mid), nil
	}
	return "", fmt.Errorf("QQ 发送回执无效")
}

type QqLogin struct {
	URL        string
	Token      string
	Onebot     QqConfig
	Managed    bool
	Credential string
	configured bool
}

func NewQqLogin(webuiURL, token string, onebot QqConfig, managed bool) (*QqLogin, error) {
	origin, err := Loopback(webuiURL)
	if err != nil {
		return nil, err
	}
	return &QqLogin{URL: origin, Token: token, Onebot: onebot, Managed: managed}, nil
}

func (login *QqLogin) action(ctx context.Context, name string, body any, authenticated bool) (any, error) {
	switch name {
	case "auth/login", "QQLogin/CheckLoginStatus", "QQLogin/GetQQLoginInfo", "OB11Config/GetConfig", "OB11Config/SetConfig":
	default:
		return nil, fmt.Errorf("QQ 登录操作无效")
	}
	token := ""
	if authenticated {
		token = login.Credential
	}
	value, err := LocalJSON(ctx, login.URL, "/api/"+name, body, token)
	if err != nil {
		return nil, err
	}
	if int(number(value["code"])) != 0 {
		return nil, fmt.Errorf("QQ 组件未确认登录操作")
	}
	return value["data"], nil
}

func (login *QqLogin) Start(ctx context.Context) (map[string]any, error) {
	sum := sha256.Sum256([]byte(login.Token + ".napcat"))
	raw, err := login.action(ctx, "auth/login", map[string]any{"hash": hex.EncodeToString(sum[:])}, false)
	if err != nil {
		return nil, err
	}
	value, _ := raw.(map[string]any)
	credential := text(value["Credential"])
	if credential == "" || len([]rune(credential)) > 8192 {
		return nil, fmt.Errorf("QQ 登录未取得凭据")
	}
	login.Credential = credential
	login.Token = ""
	return login.Poll(ctx)
}

func (login *QqLogin) Poll(ctx context.Context) (map[string]any, error) {
	raw, err := login.action(ctx, "QQLogin/CheckLoginStatus", map[string]any{}, true)
	if err != nil {
		return nil, err
	}
	value, _ := raw.(map[string]any)
	if value["isLogin"] == true {
		return map[string]any{"state": "verifying", "qr": ""}, nil
	}
	if value["loginError"] != nil {
		return nil, fmt.Errorf("QQ 组件拒绝此次扫码登录")
	}
	qr := text(value["qrcodeurl"])
	if qr != "" {
		parsed, err := url.Parse(qr)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
			!(parsed.Hostname() == "qq.com" || strings.HasSuffix(parsed.Hostname(), ".qq.com")) {
			return nil, fmt.Errorf("QQ 二维码地址未通过核验")
		}
	}
	state := "waiting_scan"
	if value["qrLoginAccepted"] == true {
		state = "scanned"
	}
	return map[string]any{"state": state, "qr": qr}, nil
}

func (login *QqLogin) configureOneBot(ctx context.Context) error {
	if login.configured {
		return nil
	}
	raw, err := login.action(ctx, "OB11Config/GetConfig", map[string]any{}, true)
	if err != nil {
		return err
	}
	value, _ := raw.(map[string]any)
	network, _ := value["network"].(map[string]any)
	servers, _ := network["httpServers"].([]any)
	owned := []map[string]any{}
	for _, item := range servers {
		server, _ := item.(map[string]any)
		if text(server["name"]) != "zynote-native-http" {
			continue
		}
		owned = append(owned, server)
	}
	if len(owned) > 1 {
		return fmt.Errorf("QQ 消息接口存在重名配置，未覆盖")
	}
	parsed, err := url.Parse(login.Onebot.URL)
	if err != nil || parsed.Port() == "" {
		return fmt.Errorf("QQ 消息接口地址无效")
	}
	kept := []any{}
	for _, item := range servers {
		server, _ := item.(map[string]any)
		if text(server["name"]) != "zynote-native-http" {
			kept = append(kept, item)
		}
	}
	port, _ := strconv.Atoi(parsed.Port())
	kept = append(kept, map[string]any{
		"name": "zynote-native-http", "enable": true, "host": "127.0.0.1", "port": port,
		"enableCors": false, "enableWebsocket": false, "messagePostFormat": "array",
		"token": login.Onebot.Token, "debug": false,
	})
	network["httpServers"] = kept
	value["network"] = network
	encoded, _ := json.Marshal(value)
	if _, err := login.action(ctx, "OB11Config/SetConfig", map[string]any{"config": string(encoded)}, true); err != nil {
		return err
	}
	login.configured = true
	return nil
}

func (login *QqLogin) Verify(ctx context.Context) (map[string]any, error) {
	if login.Managed {
		if err := login.configureOneBot(ctx); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	var account string
	var lastErr error
	for time.Now().Before(deadline) {
		session := &QqSession{Config: login.Onebot}
		account, lastErr = session.Identity(ctx)
		if lastErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if account == "" {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("QQ 消息接口未就绪")
	}
	raw, err := login.action(ctx, "QQLogin/GetQQLoginInfo", map[string]any{}, true)
	if err != nil {
		return nil, err
	}
	control, _ := raw.(map[string]any)
	uin, err := qqAccount(control["uin"])
	if err != nil || uin != account || (login.Onebot.Account != "" && login.Onebot.Account != account) {
		return nil, fmt.Errorf("QQ 管理端和消息接口账号不同，未绑定")
	}
	return map[string]any{
		"account": account,
		"session": map[string]any{"onebot": map[string]any{"url": login.Onebot.URL, "token": login.Onebot.Token, "account": account}},
		"capabilities": map[string]any{
			"identity": "personal_qq", "history": "onebot_extension_unverified",
			"messages": "selected_conversation", "send": "explicit_confirmed_text",
		},
	}, nil
}
