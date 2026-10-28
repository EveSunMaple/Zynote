package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotesSaveVerifiesAndWrites(t *testing.T) {
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "笔记课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "12121212-1212-4212-8212-121212121212"
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
	notes := map[string]any{
		"keyPoints": []any{map[string]any{
			"level": "必考", "topic": "重点", "detail": "解释", "quote": "这是重点内容",
			"timestamp": "00:00:02", "pptRef": "第 1 页",
		}},
		"notTested": []any{map[string]any{
			"topic": "不考", "quote": "不考这个", "timestamp": "00:00:03",
		}},
		"uncertain": []any{map[string]any{
			"topic": "含糊", "quote": "学生说的", "timestamp": "00:00:01",
		}},
	}
	raw, errReply := f.call("notes:save", map[string]any{"id": lectureID, "notes": notes})
	if errReply != nil {
		t.Fatalf("notes:save failed: %+v", errReply)
	}
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	key := saved["keyPoints"].([]any)[0].(map[string]any)
	notTested := saved["notTested"].([]any)[0].(map[string]any)
	uncertain := saved["uncertain"].([]any)[0].(map[string]any)
	if key["verified"] != true || notTested["verified"] != true || uncertain["verified"] != false {
		t.Fatalf("verification mismatch: %s", raw)
	}
	path := filepath.Join(libraryRoot, "Notes", "笔记课", "第一讲-"+lectureID+"-notes.md")
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "## 教师明确重点") || !strings.Contains(string(body), "原话已核验") ||
		!strings.Contains(string(body), "> 这是重点内容") || strings.Contains(string(body), "课件：第 1 页") == false {
		t.Fatalf("markdown mismatch: %v\n%s", err, string(body))
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var practice int
	if err := db.QueryRow("SELECT count(*) FROM practice_sets WHERE lecture_id=?", lectureID).Scan(&practice); err != nil || practice != 0 {
		t.Fatalf("practice should reset: %d %v", practice, err)
	}
}
