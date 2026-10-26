package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/im"
)

func TestImDraftFromSavedMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"草稿回复"}}]}`)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"qwen-default","theme":"light","browserWidth":420,"browserOpen":true,"maxMinutes":60,"maxGB":1.5,"compress":false,"prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"m1","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	root := filepath.Join(libraryRoot, "知识库数据")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := im.Open(root, func(data []byte, decrypt bool) ([]byte, error) {
		result := make([]byte, len(data))
		for index, value := range data {
			result[index] = value ^ 0x33
		}
		return result, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	listing, err := store.Listing()
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Change(map[string]any{
		"action": "create", "expected_revision": listing["revision"], "name": "微信",
		"platform": "weixin_web", "fields": map[string]any{}, "secrets": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := created.(map[string]any)["connections"].([]im.Connection)[0]
	bound, err := store.Bind(connection.ID, connection.Revision, "wxid", map[string]any{
		"domain":      "wx.qq.com",
		"login":       map[string]any{"wxuin": "12345", "wxsid": "sid", "skey": "skey"},
		"BaseRequest": map[string]any{"Uin": 12345, "Sid": "sid", "Skey": "skey", "DeviceID": "e123456789012345"},
		"cookies":     []any{},
	}, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := map[string]any{
		"connection_id": bound.ID, "revision": bound.Revision, "platform": bound.Platform,
		"account": bound.Account, "login_epoch": bound.LoginEpoch, "peer": "peer1",
	}
	if _, err := store.SaveMessages(scope, []im.Message{
		{MID: "m1", TS: 1, Sender: "peer1", Text: "你好", Kind: "text"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	raw, errReply := f.call("im:draft", map[string]any{"scope": scope, "instruction": "简短回复"})
	if errReply != nil {
		t.Fatalf("im:draft failed: %+v", errReply)
	}
	var reply string
	if err := json.Unmarshal(raw, &reply); err != nil || strings.TrimSpace(reply) != "草稿回复" {
		t.Fatalf("im:draft mismatch: %s %v", raw, err)
	}
}
