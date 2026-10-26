package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportSubtitlesNotesPractice(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
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
	courseRaw, _ := f.call("course:create", map[string]any{"name": "导出课"})
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "12121212-1212-4212-8212-121212121212"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'字幕完成')", lectureID, courseID, "第一讲"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	srt := "1\n00:00:01,000 --> 00:00:03,000\n第一句\n"
	if _, errReply := f.call("subtitles:save", map[string]any{"id": lectureID, "srt": srt}); errReply != nil {
		t.Fatal(errReply)
	}
	notes := map[string]any{
		"keyPoints": []any{map[string]any{"level": "必考", "topic": "主题", "detail": "解释", "quote": "第一句", "timestamp": "00:00:01"}},
		"notTested": []any{}, "uncertain": []any{},
	}
	if _, errReply := f.call("notes:save", map[string]any{"id": lectureID, "notes": notes}); errReply != nil {
		t.Fatalf("notes:save failed: %+v", errReply)
	}
	practice := `{"past":[{"stem":"1. 题\nA. 甲\nB. 乙","answer":"A","explanation":"解析","type":"单选","topic":"主题","reason":"依据"}],"extended":[],"generated":[],"shortage":0}`
	db, _ = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if _, err := db.Exec("INSERT OR REPLACE INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)", lectureID, "[]", practice); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, input := range []map[string]any{
		{"id": lectureID, "kind": "evidence-bundle", "format": "json"},
		{"id": lectureID, "kind": "evidence-bundle", "format": "md"},
		{"id": lectureID, "kind": "subtitles", "format": "srt"},
		{"id": lectureID, "kind": "subtitles", "format": "txt"},
		{"id": lectureID, "kind": "notes", "format": "md"},
		{"id": lectureID, "kind": "practice", "format": "docx"},
	} {
		raw, errReply := f.call("export", input)
		if errReply != nil {
			t.Fatalf("export %v failed: %+v", input, errReply)
		}
		var path string
		_ = json.Unmarshal(raw, &path)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		switch input["format"] {
		case "json":
			var bundle map[string]any
			if json.Unmarshal(body, &bundle) != nil || bundle["schemaVersion"] != "1.0.0" {
				t.Fatalf("evidence json mismatch: %s", body)
			}
		case "srt":
			if string(body) != srt {
				t.Fatalf("srt mismatch: %q", body)
			}
		case "txt":
			if !strings.Contains(string(body), "[00:00:01] 第一句") {
				t.Fatalf("txt mismatch: %q", body)
			}
		case "md":
			if !strings.Contains(string(body), "教师明确重点") && !strings.Contains(string(body), "课程学习证据包") {
				t.Fatalf("md mismatch: %q", body)
			}
		case "docx":
			if len(body) < 2 || string(body[:2]) != "PK" {
				t.Fatalf("docx mismatch: %q", body[:2])
			}
		}
	}
}
