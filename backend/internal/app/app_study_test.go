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

	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func TestStudyRegisteredTextbook(t *testing.T) {
	dataRoot := t.TempDir()
	f := startCore(t, dataRoot, t.TempDir())
	if _, failure := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10", "platform": testPlatform(),
		"dataRoot": dataRoot, "libraryRoot": t.TempDir(), "resourcesRoot": t.TempDir(), "promptsRoot": t.TempDir(),
	}); failure != nil {
		t.Fatal(failure)
	}
	if _, failure := f.call("db:bootstrap", nil); failure != nil {
		t.Fatal(failure)
	}
	write, err := storage.OpenWrite(filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer write.Close()
	path := filepath.Join(t.TempDir(), "已移动教材.pdf")
	bookID, err := write.RegisterTextbook(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := write.RegisterTextbook(path); err != nil || again != bookID {
		t.Fatalf("reselect: %s %v", again, err)
	}
	entry := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "weekly": true,
		"title": "高等数学", "courseId": "", "chapter": "", "endsAt": "", "textbookId": bookID}
	if _, failure := f.call("study:save", map[string]any{"entries": []any{entry}}); failure != nil {
		t.Fatal(failure)
	}
	for _, invalid := range []any{"33333333-3333-4333-8333-333333333333", path, 1} {
		entry["textbookId"] = invalid
		if _, failure := f.call("study:save", map[string]any{"entries": []any{entry}}); failure == nil {
			t.Fatal("accepted a forged textbook link")
		}
		if _, failure := f.call("textbook:resolve", map[string]any{"id": invalid, "path": path}); failure == nil {
			t.Fatal("accepted an unregistered textbook reference")
		}
	}
	if rows, failure := f.call("study:get", nil); failure != nil || !strings.Contains(string(rows), bookID) {
		t.Fatalf("invalid update lost the saved textbook: %s %+v", rows, failure)
	}
	if _, failure := f.call("textbook:resolve", map[string]any{"id": bookID}); failure == nil || !strings.Contains(failure.Message, "重新选择") {
		t.Fatalf("moved PDF: %+v", failure)
	}
}

func TestStudyGetAndSave(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "课表课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "88888888-8888-4888-8888-888888888888"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待转写')", lectureID, courseID, "旧标题"); err != nil {
		t.Fatal(err)
	}
	sessionID := "99999999-9999-4999-8999-999999999999"
	if _, err = db.Exec(
		"INSERT INTO chat_sessions(id,course_id,title,lecture_id) VALUES(?,?,?,?)",
		sessionID, courseID, "旧标题", lectureID,
	); err != nil {
		t.Fatal(err)
	}
	db.Close()
	entries := []map[string]any{
		{
			"id": "11111111-1111-4111-8111-111111111111", "courseId": courseID,
			"title": " 第一讲 ", "chapter": " 第一章 ", "endsAt": "2026-10-23T10:00:00+08:00",
			"zhiyunCourseId": "123", "subId": "456",
		},
		{
			"id": "22222222-2222-4222-8222-222222222222", "courseId": courseID,
			"lectureId": lectureID, "title": "新标题", "chapter": "第二章", "endsAt": "",
		},
	}
	raw, errReply := f.call("study:save", map[string]any{"entries": entries})
	if errReply != nil {
		t.Fatalf("study:save failed: %+v", errReply)
	}
	var saved []map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved) != 2 {
		t.Fatalf("save result mismatch: %s %v", raw, err)
	}
	if saved[0]["title"] != "第一讲" || saved[0]["checkAt"] != "2026-10-23T06:00:00.000Z" || saved[1]["checkAt"] != "" {
		t.Fatalf("checkAt mismatch: %+v", saved)
	}
	raw, errReply = f.call("study:get", nil)
	if errReply != nil || !containsAll(string(raw), "第一讲", "新标题", "2026-10-23T06:00:00.000Z") {
		t.Fatalf("study:get mismatch: %s %+v", raw, errReply)
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var lectureTitle, sessionTitle string
	if err := db.QueryRow("SELECT title FROM lectures WHERE id=?", lectureID).Scan(&lectureTitle); err != nil || lectureTitle != "新标题" {
		t.Fatalf("lecture title mismatch: %s %v", lectureTitle, err)
	}
	if err := db.QueryRow("SELECT title FROM chat_sessions WHERE id=?", sessionID).Scan(&sessionTitle); err != nil || sessionTitle != "新标题" {
		t.Fatalf("session title mismatch: %s %v", sessionTitle, err)
	}
	if _, errReply := f.call("study:save", map[string]any{"entries": []map[string]any{
		{"id": "11111111-1111-4111-8111-111111111111", "courseId": courseID, "title": "a", "chapter": "b", "endsAt": ""},
		{"id": "11111111-1111-4111-8111-111111111111", "courseId": courseID, "title": "c", "chapter": "d", "endsAt": ""},
	}}); errReply == nil || errReply.Message != "课表编号重复" {
		t.Fatalf("duplicate mismatch: %+v", errReply)
	}
	if _, errReply := f.call("study:save", map[string]any{"entries": []map[string]any{
		{"id": "11111111-1111-4111-8111-111111111111", "courseId": courseID, "title": "a", "chapter": "b", "endsAt": "bad"},
	}}); errReply == nil {
		t.Fatal("invalid endsAt must fail")
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func startStudyParseFixture(t *testing.T, serverURL string) *fixture {
	t.Helper()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+serverURL+`/v1","model":"test","visionModel":"vision-test","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
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
	return f
}

func TestStudyParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.Messages) != 2 {
			t.Error("missing model messages")
			return
		}
		var input struct {
			Courses []struct {
				ID string `json:"id"`
			} `json:"courses"`
			Text     string `json:"text"`
			Timezone string `json:"timezone"`
		}
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil || len(input.Courses) != 1 || input.Timezone != "Asia/Shanghai" || !strings.Contains(input.Text, "2026年10月23日") {
			t.Error("missing calendar/catalog context", err)
			return
		}
		parsedCourseID := input.Courses[0].ID
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"[{\"id\":\"33333333-3333-4333-8333-333333333333\",\"courseId\":\"%s\",\"title\":\" 第一讲 \",\"chapter\":\" 第一章 \",\"endsAt\":\"2026-10-23T10:00:00+08:00\"}]"}}]}`, parsedCourseID)
	}))
	defer server.Close()
	f := startStudyParseFixture(t, server.URL)
	_, courseErr := f.call("course:create", map[string]any{"name": "解析课"})
	if courseErr != nil {
		t.Fatalf("course:create failed: %+v", courseErr)
	}
	raw, errReply := f.call("study:parse", map[string]any{"text": "2026年10月23日 解析课 第一讲 第一章 第1-2节；第1节08:00-08:45，第2节09:15-10:00", "timezone": "Asia/Shanghai"})
	if errReply != nil {
		t.Fatalf("study:parse failed: %+v", errReply)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 || rows[0]["title"] != "第一讲" {
		t.Fatalf("parse rows mismatch: %s %v", raw, err)
	}
}
