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

func TestQuestionsSplit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := `[{"year":"2025","type":"选择题","stem":"以下说法正确的是？","answer":"A","knowledgePoints":["基础"]}]`
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, payload)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{"past-exam":"拆题提示词"},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "拆题课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	fileID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO course_files(id,course_id,kind,filename,path,text,chunks_json,needs_ocr) VALUES(?,?,?,?,?,?,?,?)",
		fileID, courseID, "past_exam", "期末.pdf", "/tmp/期末.pdf", "1. 以下说法正确的是？\nA. 甲\nB. 乙", "[]", 0,
	); err != nil {
		t.Fatal(err)
	}
	lectureID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if _, err = db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待转写')", lectureID, courseID, "课次"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)",
		lectureID, "[]", "{}"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, errReply := f.call("questions:split", map[string]any{"courseId": courseID, "id": fileID})
	if errReply != nil {
		t.Fatalf("split failed: %+v", errReply)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 || rows[0]["stem"] == "" ||
		rows[0]["courseId"] != courseID || rows[0]["sourceFile"] != "期末.pdf" || len(rows[0]["id"].(string)) != 36 {
		t.Fatalf("split rows mismatch: %s %v", raw, err)
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, practice int
	if err := db.QueryRow("SELECT count(*) FROM past_questions WHERE course_id=? AND file_id=?", courseID, fileID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("question count mismatch: %d %v", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM practice_sets").Scan(&practice); err != nil || practice != 0 {
		t.Fatalf("practice reset mismatch: %d %v", practice, err)
	}
	if _, errReply := f.call("questions:split", map[string]any{"courseId": courseID, "id": "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}); errReply == nil ||
		!strings.Contains(errReply.Message, "历年卷") {
		t.Fatalf("missing file mismatch: %+v", errReply)
	}
}
