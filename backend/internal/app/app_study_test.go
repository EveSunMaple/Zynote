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
)

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

func TestStudyParse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"[{\"id\":\"33333333-3333-4333-8333-333333333333\",\"courseId\":\"44444444-4444-4444-8444-444444444444\",\"title\":\" 第一讲 \",\"chapter\":\" 第一章 \",\"endsAt\":\"2026-10-23T10:00:00+08:00\"}]"}}]}`)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
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
	if _, errReply := f.call("course:create", map[string]any{"name": "解析课"}); errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	raw, errReply := f.call("study:parse", map[string]any{"text": "周一一二节 第一讲", "timezone": "Asia/Shanghai"})
	if errReply != nil {
		t.Fatalf("study:parse failed: %+v", errReply)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 || rows[0]["title"] != "第一讲" {
		t.Fatalf("parse rows mismatch: %s %v", raw, err)
	}
}
