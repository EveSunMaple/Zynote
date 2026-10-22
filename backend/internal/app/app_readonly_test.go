package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/app"
	"github.com/EveSunMaple/Zynote/backend/internal/transport"
	_ "modernc.org/sqlite"
)

func TestP1Readonly(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema := `
 CREATE TABLE courses(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE course_files(id TEXT PRIMARY KEY,course_id TEXT NOT NULL,kind TEXT NOT NULL,filename TEXT,path TEXT,text TEXT,chunks_json TEXT,needs_ocr INTEGER DEFAULT 0);
 CREATE TABLE past_questions(id TEXT PRIMARY KEY,course_id TEXT NOT NULL,file_id TEXT,question_json TEXT NOT NULL);
 CREATE TABLE lectures(id TEXT PRIMARY KEY,course_id TEXT NOT NULL,title TEXT,video_path TEXT,ppt_path TEXT,ppt_text TEXT,status TEXT DEFAULT '待转写',created_at TEXT DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE messages(id TEXT PRIMARY KEY,course_id TEXT NOT NULL,lecture_id TEXT,session_id TEXT,role TEXT,content TEXT,created_at TEXT DEFAULT CURRENT_TIMESTAMP);
 CREATE TABLE chat_sessions(id TEXT PRIMARY KEY,course_id TEXT NOT NULL,title TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	courseID := "11111111-1111-4111-8111-111111111111"
	fileID := "22222222-2222-4222-8222-222222222222"
	lectureID := "33333333-3333-4333-8333-333333333333"
	sessionID := "44444444-4444-4444-8444-444444444444"
	messageID := "55555555-5555-4555-8555-555555555555"
	if _, err = db.Exec("INSERT INTO courses(id,name,created_at) VALUES(?,?,'2026-10-01T00:00:00.000Z')", courseID, "课程A"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO course_files(id,course_id,kind,filename,path,text,chunks_json,needs_ocr) VALUES(?,?,?,?,?,?,?,?)",
		fileID, courseID, "past_exam", "真题.pdf", "Library/历年卷/课程A/真题.pdf", "正文", `["块1","块2"]`, 0,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO past_questions(id,course_id,file_id,question_json) VALUES(?,?,?,?)",
		"66666666-6666-4666-8666-666666666666", courseID, fileID,
		`{"id":"66666666-6666-4666-8666-666666666666","courseId":"`+courseID+`","sourceFile":"真题.pdf","type":"填空","stem":"题目","knowledgePoints":["导数"]}`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO lectures(id,course_id,title,video_path,ppt_path,ppt_text,status,created_at) VALUES(?,?,?,?,?,?,?,?)",
		lectureID, courseID, "第一课", "Library/媒体下载/a.mp4", "", "", "待转写", "2026-10-02T00:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO chat_sessions(id,course_id,title,created_at) VALUES(?,?,?,?)",
		sessionID, courseID, "第一课", "2026-10-02T00:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO messages(id,course_id,lecture_id,session_id,role,content,created_at) VALUES(?,?,?,?,?,?,?)",
		messageID, courseID, lectureID, sessionID, "user", "你好", "2026-10-02T00:00:01.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	writeLibraryFile(t, libraryRoot, "语音原文/课程A/notes.txt", "内容")

	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}

	_, errReply := f.call("course:detail", map[string]any{"id": courseID})
	if errReply != nil {
		t.Fatalf("course:detail failed: %+v", errReply)
	}
	var detail map[string]any
	if err = json.Unmarshal(f.lastResult, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["course"].(map[string]any)["name"] != "课程A" {
		t.Fatalf("unexpected course: %v", detail["course"])
	}
	if len(detail["sessions"].([]any)) != 1 || len(detail["lectures"].([]any)) != 1 || len(detail["messages"].([]any)) != 1 {
		t.Fatalf("unexpected counts: %v", detail)
	}
	file := detail["files"].([]any)[0].(map[string]any)
	if file["chunks_json"] != `["块1","块2"]` {
		t.Fatalf("chunks_json passthrough broken: %v", file)
	}
	chunks := file["chunks"].([]any)
	if len(chunks) != 2 || chunks[0] != "块1" {
		t.Fatalf("chunks parse broken: %v", chunks)
	}
	question := detail["questions"].([]any)[0].(map[string]any)
	if question["stem"] != "题目" {
		t.Fatalf("question parse broken: %v", question)
	}

	if _, errReply = f.call("course:detail", map[string]any{"id": "99999999-9999-4999-8999-999999999999"}); errReply == nil || errReply.Code != "NOT_FOUND" {
		t.Fatalf("missing course should be NOT_FOUND, got %+v", errReply)
	}

	if _, errReply = f.call("course:detail", map[string]any{"id": "not-a-uuid"}); errReply == nil || errReply.Code != "INPUT_INVALID" {
		t.Fatalf("invalid id should be INPUT_INVALID, got %+v", errReply)
	}

	if _, errReply := f.call("library:list", nil); errReply != nil {
		t.Fatalf("library:list failed: %+v", errReply)
	}
	var files []any
	if err = json.Unmarshal(f.lastResult, &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("unexpected library files: %v", files)
	}
	entry := files[0].(map[string]any)
	if entry["course"] != "课程A" || entry["category"] != "语音原文" || entry["name"] != "notes.txt" || entry["size"] != float64(6) {
		t.Fatalf("unexpected entry: %v", entry)
	}

	_ = app.New()
	_ = transport.MaxFrameBytes
}

func writeLibraryFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
