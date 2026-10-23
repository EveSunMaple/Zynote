package app_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPracticeSubmitAndReset(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "练习课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	lectureID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lectures(id,course_id,title,status) VALUES(?,?,?,'字幕完成')", lectureID, courseID, "练习"); err != nil {
		t.Fatal(err)
	}
	practice := map[string]any{
		"past": []any{}, "shortage": 0,
		"extended": []any{map[string]any{
			"id": "ffffffff-ffff-4fff-8fff-ffffffffffff", "courseId": courseID,
			"stem": "以下正确的是？\nA. 甲\nB. 乙\nC. 丙", "answer": "A", "type": "选择题",
			"knowledgePoints": []any{"基础"}, "answerSource": "imported",
		}},
	}
	encoded, _ := json.Marshal(practice)
	if _, err = db.Exec("INSERT INTO practice_sets(lecture_id,past_ids_json,extended_json) VALUES(?,?,?)", lectureID, "[]", string(encoded)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, errReply := f.call("practice:submit", map[string]any{
		"id": lectureID, "group": "extended", "index": 0, "response": "A",
	})
	if errReply != nil {
		t.Fatalf("submit failed: %+v", errReply)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	question := result["extended"].([]any)[0].(map[string]any)
	attempt := question["attempt"].(map[string]any)
	if attempt["verdict"] != "correct" || attempt["feedback"] != "选项正确。" {
		t.Fatalf("attempt mismatch: %s", raw)
	}
	raw, errReply = f.call("practice:submit", map[string]any{
		"id": lectureID, "group": "extended", "index": 0, "response": "B",
	})
	_ = json.Unmarshal(raw, &result)
	question = result["extended"].([]any)[0].(map[string]any)
	if question["attempt"].(map[string]any)["verdict"] != "incorrect" {
		t.Fatalf("incorrect verdict mismatch: %s", raw)
	}
	raw, errReply = f.call("practice:reset", map[string]any{"id": lectureID, "group": "extended", "index": 0})
	if errReply != nil {
		t.Fatalf("reset failed: %+v", errReply)
	}
	_ = json.Unmarshal(raw, &result)
	question = result["extended"].([]any)[0].(map[string]any)
	if _, ok := question["attempt"]; ok {
		t.Fatalf("attempt not cleared: %s", raw)
	}
	if _, errReply := f.call("practice:submit", map[string]any{
		"id": lectureID, "group": "extended", "index": 9, "response": "A",
	}); errReply == nil || errReply.Message != "题目不存在" {
		t.Fatalf("missing question mismatch: %+v", errReply)
	}
}
