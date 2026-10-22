package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestP2Writes(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()

	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}

	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("db:bootstrap failed: %+v", errReply)
	}

	course, errReply := f.call("course:create", map[string]any{"name": "  课程B  "})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var created map[string]any
	if err := json.Unmarshal(course, &created); err != nil {
		t.Fatal(err)
	}
	courseID, _ := created["id"].(string)
	if len(courseID) != 36 || created["name"] != "课程B" {
		t.Fatalf("unexpected course: %v", created)
	}
	createdAt, _ := created["created_at"].(string)
	if len(createdAt) != 19 {
		t.Fatalf("created_at format mismatch: %q", createdAt)
	}
	for _, category := range []string{"PPT", "Transcripts", "Notes", "PastExams"} {
		if _, err := os.Stat(filepath.Join(libraryRoot, category, "课程B")); err != nil {
			t.Fatalf("library dir missing for %s: %v", category, err)
		}
	}

	if _, errReply = f.call("sessions", nil); errReply != nil {
		t.Fatalf("sessions failed: %+v", errReply)
	}
	var sessions []any
	if err := json.Unmarshal(f.lastResult, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].(map[string]any)["title"] != "新对话" {
		t.Fatalf("expected one default session, got %v", sessions)
	}

	lectureID := "33333333-3333-4333-8333-333333333333"
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
	_ = db.Close()

	first, errReply := f.call("session:create", map[string]any{"courseId": courseID, "lectureId": lectureID})
	if errReply != nil {
		t.Fatalf("session:create failed: %+v", errReply)
	}
	var session map[string]any
	if err = json.Unmarshal(first, &session); err != nil {
		t.Fatal(err)
	}
	if session["title"] != "第一课" || session["lecture_id"] != lectureID {
		t.Fatalf("unexpected session: %v", session)
	}

	second, errReply := f.call("session:create", map[string]any{"courseId": courseID})
	if errReply != nil {
		t.Fatalf("session:create dedup failed: %+v", errReply)
	}
	var duplicated map[string]any
	if err = json.Unmarshal(second, &duplicated); err != nil {
		t.Fatal(err)
	}
	if duplicated["title"] != "第一课 · 2" {
		t.Fatalf("session dedup broken: %v", duplicated)
	}

	if _, errReply = f.call("session:create", map[string]any{"courseId": "99999999-9999-4999-8999-999999999999"}); errReply == nil || errReply.Code != "NOT_FOUND" {
		t.Fatalf("missing course should be NOT_FOUND, got %+v", errReply)
	}

	if _, errReply = f.call("course:create", map[string]any{"name": "   "}); errReply == nil {
		t.Fatalf("empty name should fail")
	}
	if errReply.Message != "课程名称不能为空" {
		t.Fatalf("unexpected error message: %+v", errReply)
	}
}
