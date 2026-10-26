package im

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testCrypt(data []byte, decrypt bool) ([]byte, error) {
	result := make([]byte, len(data))
	for index, value := range data {
		result[index] = value ^ 0x5a
	}
	return result, nil
}

func TestStoreAndQqService(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var packet map[string]any
		_ = json.Unmarshal(body, &packet)
		var data any
		switch request.URL.Path {
		case "/get_login_info":
			data = map[string]any{"user_id": 10001}
		case "/get_status":
			data = map[string]any{"online": true, "good": true}
		case "/get_friend_msg_history":
			data = map[string]any{"messages": []any{map[string]any{
				"message_id": 1001, "time": 1, "sender": map[string]any{"user_id": 20002}, "message": "你好",
			}}}
		case "/send_private_msg":
			sent++
			data = map[string]any{"message_id": 1002}
		default:
			data = map[string]any{}
		}
		encoded, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "data": data})
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(encoded)
	}))
	defer server.Close()
	service, err := NewService(root, "", testCrypt)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	initial, err := service.Store.Listing()
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Execute(context.Background(), "change", map[string]any{
		"action": "create", "expected_revision": initial["revision"],
		"name": "QQ", "platform": "qq_napcat",
		"fields": map[string]any{"mode": "manual", "webui_url": server.URL, "onebot_url": server.URL},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	listing := created.(map[string]any)
	connections := listing["connections"].([]Connection)
	if len(connections) != 1 {
		t.Fatalf("connection mismatch: %+v", listing)
	}
	connection := connections[0]
	bound, err := service.Store.Bind(connection.ID, connection.Revision, "10001", map[string]any{
		"onebot": map[string]any{"url": server.URL, "token": "", "account": "10001"},
	}, map[string]any{"identity": "personal_qq"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{
		ConnectionID: bound.ID, Revision: bound.Revision, Platform: bound.Platform,
		Account: bound.Account, LoginEpoch: bound.LoginEpoch, Peer: "private:20002",
	}
	synced, err := service.Execute(context.Background(), "sync", scopeMap(scope), "")
	if err != nil {
		t.Fatal(err)
	}
	if synced.(map[string]any)["saved"].(int) != 1 {
		t.Fatalf("sync mismatch: %+v", synced)
	}
	read, err := service.Execute(context.Background(), "read", scopeMap(scope), "")
	if err != nil {
		t.Fatal(err)
	}
	messages := read.(map[string]any)["messages"].([]Message)
	if len(messages) != 1 || messages[0].Text != "你好" {
		t.Fatalf("read mismatch: %+v", messages)
	}
	sendInput := scopeMap(scope)
	sendInput["operation_id"] = "11111111111111111111111111111111"
	sendInput["text"] = "收到"
	sendInput["user_confirmed"] = true
	receipt, err := service.Execute(context.Background(), "send", sendInput, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.(map[string]any)["state"] != "sent" || sent != 1 {
		t.Fatalf("send mismatch: %+v %d", receipt, sent)
	}
	cleanup, err := service.Execute(context.Background(), "cleanup", scopeMap(scope), "")
	if err != nil {
		t.Fatal(err)
	}
	preview := cleanup.(map[string]any)
	confirm := scopeMap(scope)
	confirm["preview_id"] = preview["preview_id"]
	confirm["user_confirmed"] = true
	deleted, err := service.Execute(context.Background(), "cleanup", confirm, "")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.(map[string]any)["deleted"].(int) != 1 {
		t.Fatalf("cleanup mismatch: %+v", deleted)
	}
}

func scopeMap(scope Scope) map[string]any {
	return map[string]any{
		"connection_id": scope.ConnectionID, "revision": scope.Revision, "platform": scope.Platform,
		"account": scope.Account, "login_epoch": scope.LoginEpoch, "peer": scope.Peer,
	}
}

func TestLocalJSONRejectsRemote(t *testing.T) {
	if _, err := Loopback("http://example.com:1234"); err == nil {
		t.Fatal("remote loopback must fail")
	}
	if _, err := Loopback("http://127.0.0.1:1234/path"); err == nil {
		t.Fatal("path must fail")
	}
	if value, err := Loopback("http://127.0.0.1:1234"); err != nil || !strings.HasPrefix(value, "http://") {
		t.Fatalf("loopback mismatch: %v", err)
	}
}
