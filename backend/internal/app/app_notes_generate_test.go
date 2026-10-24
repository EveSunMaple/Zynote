package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNotesGenerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := `{"keyPoints":[{"level":"必考","topic":"重点","detail":"解释","quote":"这是重点内容","timestamp":"00:00:02","pptRef":""}],"notTested":[{"topic":"不考","quote":"不考这个","timestamp":"00:00:03"}],"uncertain":[]}`
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, payload)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{"notes":"笔记提示词"},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "笔记生成课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "13131313-1313-4313-8313-131313131313"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'字幕完成')", lectureID, courseID, "第一讲"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)", lectureID, "[]", "{}"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	srt := "1\n00:00:01,000 --> 00:00:05,000\n这是重点内容 不考这个\n"
	if _, errReply := f.call("subtitles:save", map[string]any{"id": lectureID, "srt": srt}); errReply != nil {
		t.Fatalf("subtitles:save failed: %+v", errReply)
	}
	raw, errReply := f.call("notes:generate", map[string]any{"id": lectureID})
	if errReply != nil {
		t.Fatalf("notes:generate failed: %+v", errReply)
	}
	var notes map[string]any
	_ = json.Unmarshal(raw, &notes)
	key := notes["keyPoints"].([]any)[0].(map[string]any)
	notTested := notes["notTested"].([]any)[0].(map[string]any)
	if key["verified"] != true || notTested["verified"] != true {
		t.Fatalf("verification mismatch: %s", raw)
	}
	path := filepath.Join(libraryRoot, "Notes", "笔记生成课", "第一讲-"+lectureID+"-notes.md")
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("note file missing: %v", err)
	}
	if _, errReply := f.call("notes:generate", map[string]any{"id": "12121212-1212-4212-8212-121212121212"}); errReply == nil {
		t.Fatal("missing lecture must fail")
	}
}
