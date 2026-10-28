package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

func TestChatStreamsAndPersists(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","browserWidth":420,"browserOpen":true,"maxMinutes":60,"maxGB":1.5,"compress":false,"prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "聊天课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	raw, errReply := f.call("chat", map[string]any{
		"courseId": courseID, "content": "你好", "requestId": "req-1",
	})
	if errReply != nil {
		t.Fatalf("chat failed: %+v", errReply)
	}
	var reply string
	if err := json.Unmarshal(raw, &reply); err != nil || reply != "你好" {
		t.Fatalf("chat reply mismatch: %s %v", raw, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM messages WHERE course_id=?", courseID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("message count mismatch: %d %v", count, err)
	}
	var role, content string
	if err := db.QueryRow("SELECT role,content FROM messages WHERE course_id=? ORDER BY rowid LIMIT 1", courseID).Scan(&role, &content); err != nil ||
		role != "user" || content != "你好" {
		t.Fatalf("user message mismatch: %s %s %v", role, content, err)
	}
	chatEvents := 0
	jobEvents := 0
	for _, event := range f.events {
		var payload map[string]any
		_ = json.Unmarshal(event.Params, &payload)
		switch payload["kind"] {
		case "chat":
			if payload["requestId"] == "req-1" {
				chatEvents++
			}
		case "job":
			jobEvents++
		}
	}
	if chatEvents != 2 || jobEvents != 1 {
		t.Fatalf("event mismatch: chat=%d job=%d %+v", chatEvents, jobEvents, f.events)
	}
	if strings.Contains(string(raw), "event") {
		t.Fatalf("reply polluted: %s", raw)
	}
}

func TestSettingsProbeAndModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/chat/completions":
			fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"连接成功。"}}]}`)
		case "/v1/models":
			fmt.Fprint(writer, `{"data":[{"id":"model-a"},{"id":"model-b"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	profile := map[string]any{
		"id": "qwen-default", "name": "通义千问", "provider": "qwen",
		"baseUrl": server.URL + "/v1", "model": "model-a", "visionModel": "",
		"apiFormat": "chat_completions", "apiKey": "test-key",
	}
	raw, errReply := f.call("settings:probe", map[string]any{"profile": profile})
	if errReply != nil || !strings.Contains(string(raw), "连接成功") {
		t.Fatalf("probe mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("settings:models", map[string]any{"profile": profile, "persist": true})
	if errReply != nil || !strings.Contains(string(raw), "model-b") {
		t.Fatalf("models mismatch: %s %+v", raw, errReply)
	}
}
