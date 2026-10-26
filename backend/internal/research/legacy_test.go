package research

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestLegacyBaselineAndPractice(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy, err := sql.Open("sqlite", filepath.Join(root, "knowledge.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	for _, statement := range []string{
		`CREATE TABLE tutor_questions(question_id TEXT, question TEXT, created_at TEXT, kb_root_id TEXT)`,
		`CREATE TABLE kb_conversations(conversation_id TEXT, title TEXT, revision INTEGER, updated_at TEXT, kb_root_id TEXT)`,
		`CREATE TABLE learning_memories(memory_id TEXT, revision INTEGER, kind TEXT, title TEXT, body TEXT, source_task TEXT, created_at TEXT, updated_at TEXT, deleted INTEGER, kb_root_id TEXT)`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	questionID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := legacy.Exec("INSERT INTO tutor_questions VALUES(?,?,?,?)", questionID, "特征工程有哪些步骤", "2026-01-01", store.RootID); err != nil {
		t.Fatal(err)
	}
	conversationID := "cv_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := legacy.Exec("INSERT INTO kb_conversations VALUES(?,?,?,?,?)", conversationID, "旧会话", 3, "2026-01-01", store.RootID); err != nil {
		t.Fatal(err)
	}
	memoryID := "cccccccccccccccccccccccccccccccc"
	if _, err := legacy.Exec("INSERT INTO learning_memories VALUES(?,?,?,?,?,?,?,?,?,?)",
		memoryID, 1, "preference", "偏好", "先讲例子", nil, "2026-01-01", "2026-01-01", 0, store.RootID); err != nil {
		t.Fatal(err)
	}
	result, err := store.BaselineLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != true {
		t.Fatalf("baseline mismatch: %+v", result)
	}
	conversation, err := store.Conversation(conversationID)
	if err != nil || conversation["title"] != "旧会话" {
		t.Fatalf("legacy conversation mismatch: %+v %v", conversation, err)
	}
	memories, err := store.Memories()
	if err != nil || len(memories["records"].([]map[string]any)) != 1 {
		t.Fatalf("legacy memory mismatch: %+v %v", memories, err)
	}
	questions, err := store.PracticeQuestions("特征工程", "")
	if err != nil || len(questions) != 1 || questions[0]["question_id"] != questionID {
		t.Fatalf("practice mismatch: %+v %v", questions, err)
	}
	context, err := store.ContextSnapshot([]string{"practice"}, "特征工程", nil, "")
	if err != nil || len(context.Rows) != 1 || context.Rows[0].Kind != "practice" {
		t.Fatalf("practice context mismatch: %+v %v", context, err)
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) == 0 {
		t.Fatal("baseline digest missing")
	}
}
