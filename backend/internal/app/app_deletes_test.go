package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

func TestP2bDeletes(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()

	f := startCore(t, dataRoot, resourcesRoot)
	_, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	})
	if errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply = f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("db:bootstrap failed: %+v", errReply)
	}

	var course json.RawMessage
	course, errReply = f.call("course:create", map[string]any{"name": "删除课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var created map[string]any
	if err := json.Unmarshal(course, &created); err != nil {
		t.Fatal(err)
	}
	courseID, _ := created["id"].(string)

	lectureID := "33333333-3333-4333-8333-333333333333"
	messageID := "55555555-5555-4555-8555-555555555555"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待转写')",
		lectureID, courseID, "第一课",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO messages(id,course_id,lecture_id,session_id,role,content) VALUES(?,?,?,?,?,?)",
		messageID, courseID, lectureID, "44444444-4444-4444-8444-444444444444", "user", "你好",
	); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	first, errReply := f.call("session:create", map[string]any{"courseId": courseID, "lectureId": lectureID})
	if errReply != nil {
		t.Fatalf("session:create failed: %+v", errReply)
	}
	var session map[string]any
	if err = json.Unmarshal(first, &session); err != nil {
		t.Fatal(err)
	}

	if _, errReply = f.call("sessions", nil); errReply != nil {
		t.Fatalf("sessions failed: %+v", errReply)
	}
	var allSessions []any
	if err = json.Unmarshal(f.lastResult, &allSessions); err != nil {
		t.Fatal(err)
	}
	var defaultID string
	for _, item := range allSessions {
		if item.(map[string]any)["title"] == "新对话" {
			defaultID = item.(map[string]any)["id"].(string)
		}
	}

	if _, errReply = f.call("session:delete", map[string]any{"courseId": courseID, "id": defaultID}); errReply == nil || errReply.Message != "请先确认删除会话" {
		t.Fatalf("unconfirmed delete should fail, got %+v", errReply)
	}

	if _, errReply = f.call("session:delete", map[string]any{"courseId": courseID, "id": defaultID, "confirmed": true}); errReply != nil {
		t.Fatalf("session:delete failed: %+v", errReply)
	}
	var remaining []any
	if err = json.Unmarshal(f.lastResult, &remaining); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].(map[string]any)["title"] != "第一课" {
		t.Fatalf("unexpected remaining sessions: %v", remaining)
	}
	lastID := remaining[0].(map[string]any)["id"].(string)
	if _, errReply = f.call("session:delete", map[string]any{"courseId": courseID, "id": lastID, "confirmed": true}); errReply != nil {
		t.Fatalf("session:delete last failed: %+v", errReply)
	}
	if err = json.Unmarshal(f.lastResult, &remaining); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].(map[string]any)["title"] != "第一课" {
		t.Fatalf("auto-recreated session should take first lecture title: %v", remaining)
	}

	if _, errReply = f.call("lecture:delete", map[string]any{"id": lectureID, "confirmed": true}); errReply != nil {
		t.Fatalf("lecture:delete failed: %+v", errReply)
	}
	if _, errReply = f.call("lecture:detail", map[string]any{"id": lectureID}); errReply == nil || errReply.Message != "课次不存在" {
		t.Fatalf("deleted lecture detail should be NOT_FOUND, got %+v", errReply)
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	var messageCount int
	if err = db.QueryRow("SELECT COUNT(*) FROM messages WHERE lecture_id=?", lectureID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != 0 {
		t.Fatalf("messages should be cascade deleted, got %d", messageCount)
	}
	_ = db.Close()

	if _, errReply = f.call("course:delete", map[string]any{"id": courseID}); errReply == nil || errReply.Message != "请先确认删除课程" {
		t.Fatalf("unconfirmed course delete should fail, got %+v", errReply)
	}
	if _, errReply = f.call("course:delete", map[string]any{"id": courseID, "confirmed": true}); errReply != nil {
		t.Fatalf("course:delete failed: %+v", errReply)
	}
	var deleted bool
	if err = json.Unmarshal(f.lastResult, &deleted); err != nil || deleted != true {
		t.Fatalf("course:delete should return true, got %v", f.lastResult)
	}
	if _, errReply = f.call("course:detail", map[string]any{"id": courseID}); errReply == nil || errReply.Message != "课程不存在" {
		t.Fatalf("deleted course detail should be NOT_FOUND, got %+v", errReply)
	}
}

func TestP2bLectureDetail(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()

	f := startCore(t, dataRoot, resourcesRoot)
	_, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	})
	if errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply = f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("db:bootstrap failed: %+v", errReply)
	}

	lectureID := "33333333-3333-4333-8333-333333333333"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待校订')",
		lectureID, "11111111-1111-4111-8111-111111111111", "字幕课",
	); err != nil {
		t.Fatal(err)
	}
	segments := `[{"start":0,"end":3000,"text":"第一句"},{"start":3000,"end":6000,"text":"第二句"}]`
	if _, err = db.Exec(
		"INSERT INTO subtitles(lecture_id,srt_text,segments_json) VALUES(?,?,?)",
		lectureID, "原始SRT\n", segments,
	); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	_, errReply = f.call("lecture:detail", map[string]any{"id": lectureID})
	if errReply != nil {
		t.Fatalf("lecture:detail failed: %+v", errReply)
	}
	var detail map[string]any
	if err = json.Unmarshal(f.lastResult, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["srt"] != "原始SRT\n" {
		t.Fatalf("subtitles srt passthrough broken: %v", detail["srt"])
	}
	if len(detail["segments"].([]any)) != 2 {
		t.Fatalf("segments parse broken: %v", detail["segments"])
	}
	if _, hasCorrection := detail["correction"]; hasCorrection {
		t.Fatalf("no checkpoint should mean no correction field: %v", detail)
	}

	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO correction_checkpoints(lecture_id,fingerprint,total,segments_json) VALUES(?,?,?,?)",
		lectureID, "fp", 2, segments,
	); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, errReply = f.call("lecture:detail", map[string]any{"id": lectureID})
	if errReply != nil {
		t.Fatalf("lecture:detail with checkpoint failed: %+v", errReply)
	}
	if err = json.Unmarshal(f.lastResult, &detail); err != nil {
		t.Fatal(err)
	}
	expectedSrt := "1\n00:00:00,000 --> 00:00:03,000\n第一句\n\n2\n00:00:03,000 --> 00:00:06,000\n第二句\n"
	if detail["srt"] != expectedSrt {
		t.Fatalf("checkpoint srt regenerate broken: %q", detail["srt"])
	}
	correction := detail["correction"].(map[string]any)
	if correction["completed"] != float64(2) || correction["total"] != float64(2) {
		t.Fatalf("correction summary broken: %v", correction)
	}
	_ = os.Getpid
}
