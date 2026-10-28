package app_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSubtitlesSave(t *testing.T) {
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "字幕课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'待转写')", lectureID, courseID, "第一课"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lecture_notes(lecture_id,notes_json) VALUES(?,?)", lectureID, "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)", lectureID, "[]", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO correction_checkpoints(lecture_id,fingerprint,total,segments_json) VALUES(?,?,?,?)", lectureID, "fp", 1, "[]"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	srt := "1\n00:00:01,000 --> 00:00:02,000\n第一句\n\n2\n00:00:02,000 --> 00:00:03,000\n第二句\n"
	raw, errReply := f.call("subtitles:save", map[string]any{"id": lectureID, "srt": srt})
	if errReply != nil {
		t.Fatalf("subtitles:save failed: %+v", errReply)
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	lecture := detail["lecture"].(map[string]any)
	if lecture["status"] != "字幕完成" || detail["srt"] != srt || len(detail["segments"].([]any)) != 2 ||
		detail["notes"] != nil || detail["practice"] != nil {
		t.Fatalf("detail mismatch: %s", raw)
	}
	if _, exists := detail["correction"]; exists {
		t.Fatalf("correction checkpoint should be cleared: %s", raw)
	}
	if _, errReply := f.call("subtitles:save", map[string]any{"id": lectureID, "srt": "bad"}); errReply == nil {
		t.Fatal("invalid srt must fail")
	}
}
