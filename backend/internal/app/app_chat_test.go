package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

func TestChatStreamsAndPersists(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	chunks := []string{`公式：\(`, `\frac{1}{2}`, `\)；金额 $5 and $10。`}
	wantReply := strings.Join(chunks, "")
	question := "请给一个分数公式"
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		bodies <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": chunk}}}})
			fmt.Fprintf(writer, "data: %s\n\n", payload)
		}
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
		"courseId": courseID, "content": question, "requestId": "req-1",
	})
	if errReply != nil {
		t.Fatalf("chat failed: %+v", errReply)
	}
	var reply string
	if err := json.Unmarshal(raw, &reply); err != nil || reply != wantReply {
		t.Fatalf("chat reply mismatch: %s %v", raw, err)
	}
	var requestBody struct {
		Messages []struct {
			Role    string
			Content string
		}
	}
	if err := json.Unmarshal(<-bodies, &requestBody); err != nil {
		t.Fatal(err)
	}
	if len(requestBody.Messages) < 2 || requestBody.Messages[0].Role != "system" ||
		!strings.Contains(requestBody.Messages[0].Content, "数学呈现格式") ||
		!strings.Contains(requestBody.Messages[0].Content, "除非用户要求展示源码") {
		t.Fatal("actual HTTP model request missing math presentation contract")
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
		role != "user" || content != question {
		t.Fatalf("user message mismatch: %s %s %v", role, content, err)
	}
	if err := db.QueryRow("SELECT content FROM messages WHERE course_id=? AND role='assistant'", courseID).Scan(&content); err != nil || content != wantReply {
		t.Fatalf("assistant raw LaTeX persistence mismatch: %q %v", content, err)
	}
	chatEvents := 0
	jobEvents := 0
	var emitted strings.Builder
	for _, event := range f.events {
		var payload map[string]any
		_ = json.Unmarshal(event.Params, &payload)
		switch payload["kind"] {
		case "chat":
			if payload["requestId"] == "req-1" {
				chatEvents++
				text, _ := payload["text"].(string)
				emitted.WriteString(text)
			}
		case "job":
			jobEvents++
		}
	}
	if chatEvents != len(chunks) || jobEvents != 1 || emitted.String() != wantReply {
		t.Fatalf("event mismatch: chat=%d job=%d %+v", chatEvents, jobEvents, f.events)
	}
	if strings.Contains(string(raw), "event") {
		t.Fatalf("reply polluted: %s", raw)
	}
}

func TestChatIncludesLectureMaterials(t *testing.T) {
	bodies := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		raw, _ := io.ReadAll(request.Body)
		bodies <- string(raw)
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"已结合课程资料\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	writeFixtureFile(t, dataRoot, "lecture.txt", "[00:01] 今天讲复合函数链式法则\n[00:02] 先对中间变量求导\n")
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "上下文课程"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureRaw, errReply := f.call("lecture:create", map[string]any{
		"courseId": courseID, "title": "链式法则", "transcriptPath": filepath.Join(dataRoot, "lecture.txt"),
	})
	if errReply != nil {
		t.Fatalf("lecture:create failed: %+v", errReply)
	}
	var lecture map[string]any
	_ = json.Unmarshal(lectureRaw, &lecture)
	lectureID := lecture["id"].(string)
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE lectures SET ppt_text=? WHERE id=?", "课件文字：复合函数求导公式与例题", lectureID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if _, errReply := f.call("chat", map[string]any{
		"courseId": courseID, "lectureId": lectureID, "content": "这一讲重点是什么？", "requestId": "req-context",
	}); errReply != nil {
		t.Fatalf("chat failed: %+v", errReply)
	}
	body := <-bodies
	for _, want := range []string{"语音识别原文", "复合函数链式法则", "PPT/课件文字", "复合函数求导公式", "链式法则"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat request missing %q: %s", want, body)
		}
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
