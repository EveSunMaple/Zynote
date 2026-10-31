package research

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (store *Store) legacyDatabase() (*sql.DB, error) {
	path := filepath.Join(store.root, "knowledge.sqlite")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("旧研究数据库路径无效")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("旧研究数据库路径无效")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("旧研究数据库不可用")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func legacyTable(db *sql.DB, name string) bool {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

var legacyRowTables = map[string]struct{}{
	"kb_conversations":               {},
	"kb_conversation_organization":   {},
	"kb_conversation_turns":          {},
	"kb_conversation_research":       {},
	"learning_memories":              {},
	"learning_memory_availability":   {},
	"learning_research_tasks":        {},
	"grounded_workflows":             {},
	"grounded_runs":                  {},
	"learning_agent_extensions":      {},
	"learning_mcp_catalog":           {},
	"learning_memory_reviews":        {},
	"document_note_references":       {},
	"kb_conversation_turn_citations": {},
	"document_saved_notes":           {},
	"document_note_lifecycle_events": {},
}

func legacyRowValue(columns []string, values []any) map[string]any {
	row := map[string]any{}
	for index, column := range columns {
		switch value := values[index].(type) {
		case []byte:
			row[column] = string(value)
		default:
			row[column] = value
		}
	}
	return row
}

func legacyRows(db *sql.DB, table string) ([]map[string]any, error) {
	if _, ok := legacyRowTables[table]; !ok {
		return nil, fmt.Errorf("旧研究数据表无效")
	}
	rows, err := db.Query("SELECT * FROM " + table) // NOSONAR: table is restricted to the fixed allowlist above; no user input reaches this query.
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		result = append(result, legacyRowValue(columns, values))
	}
	return result, rows.Err()
}

type legacyQuestionRow struct {
	ID        string `json:"question_id"`
	Question  string `json:"question"`
	CreatedAt string `json:"created_at"`
}

func legacyQuestionQuery(rootID, id string) (string, []any) {
	statement := "SELECT question_id,question,created_at FROM tutor_questions WHERE kb_root_id=? ORDER BY created_at DESC,question_id LIMIT 80"
	if id == "" {
		return statement, []any{rootID}
	}
	return "SELECT question_id,question,created_at FROM tutor_questions WHERE kb_root_id=? AND question_id=?", []any{rootID, id}
}

func scanLegacyQuestions(rows *sql.Rows) ([]legacyQuestionRow, error) {
	valid := []legacyQuestionRow{}
	for rows.Next() {
		var row legacyQuestionRow
		if err := rows.Scan(&row.ID, &row.Question, &row.CreatedAt); err != nil {
			return nil, err
		}
		if !opPattern.MatchString(row.ID) || strings.TrimSpace(row.Question) == "" ||
			len([]rune(row.Question)) > 2000 || len([]rune(row.CreatedAt)) > 80 {
			continue
		}
		valid = append(valid, row)
	}
	return valid, rows.Err()
}

func questionValue(row legacyQuestionRow) map[string]any {
	encoded, _ := json.Marshal(row)
	value := map[string]any{}
	_ = json.Unmarshal(encoded, &value)
	return value
}

func (store *Store) PracticeQuestions(query, id string) ([]map[string]any, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	if len([]rune(query)) > 1500 || (id != "" && !opPattern.MatchString(id)) {
		return nil, fmt.Errorf("练习题查询参数无效")
	}
	legacy, err := store.legacyDatabase()
	if err != nil || legacy == nil {
		return []map[string]any{}, err
	}
	defer legacy.Close()
	if !legacyTable(legacy, "tutor_questions") {
		return []map[string]any{}, nil
	}
	statement, arguments := legacyQuestionQuery(store.RootID, id)
	rows, err := legacy.Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valid, err := scanLegacyQuestions(rows)
	if err != nil {
		return nil, err
	}
	if id != "" {
		result := []map[string]any{}
		for _, row := range valid {
			if row.ID == id {
				result = append(result, questionValue(row))
			}
		}
		return result, nil
	}
	terms := contextTerms(query)
	sort.SliceStable(valid, func(left, right int) bool {
		leftScore := contextMatchScore(valid[left].Question, terms)
		rightScore := contextMatchScore(valid[right].Question, terms)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if valid[left].CreatedAt != valid[right].CreatedAt {
			return valid[left].CreatedAt > valid[right].CreatedAt
		}
		return valid[left].ID < valid[right].ID
	})
	result := []map[string]any{}
	for _, row := range valid {
		if contextMatchScore(row.Question, terms) == 0 {
			continue
		}
		result = append(result, questionValue(row))
		if len(result) >= 6 {
			break
		}
	}
	return result, nil
}

func (store *Store) existingBaseline() (map[string]any, bool, error) {
	var existing string
	err := store.db.QueryRow("SELECT json FROM research_baselines WHERE name=?", "python-v1").Scan(&existing)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value := map[string]any{}
	_ = json.Unmarshal([]byte(existing), &value)
	return value, true, nil
}

func legacySnapshot(legacy *sql.DB, rootID string) (map[string][]map[string]any, error) {
	tables := []string{
		"kb_conversations", "kb_conversation_organization", "kb_conversation_turns",
		"kb_conversation_research", "learning_memories", "learning_memory_availability",
		"learning_research_tasks", "grounded_workflows", "grounded_runs",
		"learning_agent_extensions", "learning_mcp_catalog", "learning_memory_reviews",
		"document_note_references", "kb_conversation_turn_citations",
	}
	snapshot := map[string][]map[string]any{}
	for _, table := range tables {
		if !legacyTable(legacy, table) {
			continue
		}
		rows, err := legacyRows(legacy, table)
		if err != nil {
			return nil, err
		}
		filtered := []map[string]any{}
		for _, row := range rows {
			if root, ok := row["kb_root_id"]; ok && root != nil && text(root) != rootID {
				continue
			}
			filtered = append(filtered, row)
		}
		snapshot[table] = filtered
	}
	return snapshot, nil
}

func storeLegacySnapshot(tx *sql.Tx, snapshot map[string][]map[string]any) (map[string]int, error) {
	counts := map[string]int{}
	for table, rows := range snapshot {
		counts[table] = len(rows)
		for index, row := range rows {
			encoded, _ := json.Marshal(row)
			if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision,json=excluded.json", "legacy:"+table, fmt.Sprintf("%d", index), 0, string(encoded)); err != nil {
				return nil, err
			}
		}
	}
	return counts, nil
}

func importLegacyConversations(tx *sql.Tx, snapshot map[string][]map[string]any) error {
	organizations := map[string]map[string]any{}
	for _, row := range snapshot["kb_conversation_organization"] {
		organizations[text(row["conversation_id"])] = row
	}
	for _, old := range snapshot["kb_conversations"] {
		id := text(old["conversation_id"])
		if !conversationPattern.MatchString(id) {
			continue
		}
		organization := organizations[id]
		revision := int(number(old["revision"]))
		if value := int(number(organization["revision"])); value > revision {
			revision = value
		}
		row := map[string]any{
			"conversation_id": id, "title": text(old["title"]), "revision": revision,
			"pinned": organization["pinned"] == true, "updated_at": text(old["updated_at"]), "active_turn": nil,
		}
		encoded, _ := json.Marshal(row)
		if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO NOTHING", "conversation", id, revision, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func importLegacyMemories(tx *sql.Tx, snapshot map[string][]map[string]any) error {
	availability := map[string]map[string]any{}
	for _, row := range snapshot["learning_memory_availability"] {
		availability[text(row["memory_id"])] = row
	}
	for _, old := range snapshot["learning_memories"] {
		id := text(old["memory_id"])
		if !opPattern.MatchString(id) {
			continue
		}
		state := availability[id]
		row := map[string]any{
			"memory_id": id, "revision": int(number(old["revision"])), "kind": text(old["kind"]),
			"title": text(old["title"]), "body": text(old["body"]), "source_task": old["source_task"],
			"paused": state["paused"] == true, "expires_at": state["expires_at"],
			"created_at": text(old["created_at"]), "updated_at": text(old["updated_at"]),
			"deleted": old["deleted"] == true || old["deleted"] == int64(1),
		}
		encoded, _ := json.Marshal(row)
		if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO NOTHING", "memory", id, int(number(old["revision"])), string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func importLegacyTasks(tx *sql.Tx, snapshot map[string][]map[string]any, rootID string) error {
	for _, old := range snapshot["learning_research_tasks"] {
		var task map[string]any
		if json.Unmarshal([]byte(text(old["state_json"])), &task) != nil || text(task["task_id"]) == "" ||
			text(task["kb_root_id"]) != rootID {
			return fmt.Errorf("旧研究任务来源不一致")
		}
		encoded, _ := json.Marshal(task)
		if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision,json=excluded.json", "legacy-task", text(task["task_id"]), 0, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) BaselineLegacy() (map[string]any, error) {
	if err := store.baselineLegacyNotes(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if value, found, err := store.existingBaseline(); err != nil || found {
		return value, err
	}
	legacy, err := store.legacyDatabase()
	if err != nil {
		return nil, err
	}
	if legacy == nil {
		return map[string]any{"imported": false, "reason": "no-legacy-database"}, nil
	}
	defer legacy.Close()
	snapshot, err := legacySnapshot(legacy, store.RootID)
	if err != nil {
		return nil, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	counts, err := storeLegacySnapshot(tx, snapshot)
	if err != nil {
		return nil, err
	}
	if err := importLegacyConversations(tx, snapshot); err != nil {
		return nil, err
	}
	if err := importLegacyMemories(tx, snapshot); err != nil {
		return nil, err
	}
	if err := importLegacyTasks(tx, snapshot, store.RootID); err != nil {
		return nil, err
	}
	encodedSnapshot, _ := json.Marshal(snapshot)
	result := map[string]any{
		"imported": true, "root": store.RootID, "counts": counts, "digest": hashBytes(encodedSnapshot),
		"oldDatabaseReadOnly": true, "completedNativeResearch": false,
	}
	encodedResult, _ := json.Marshal(result)
	if _, err := tx.Exec("INSERT INTO research_baselines VALUES(?,?)", "python-v1", string(encodedResult)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func legacyRootRows(legacy *sql.DB, table, rootID string) ([]map[string]any, error) {
	if !legacyTable(legacy, table) {
		return nil, nil
	}
	rows, err := legacyRows(legacy, table)
	if err != nil {
		return nil, err
	}
	filtered := []map[string]any{}
	for _, row := range rows {
		if text(row["kb_root_id"]) == rootID {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func legacyNoteState(events []map[string]any, id string) map[string]any {
	var state map[string]any
	for _, event := range events {
		if text(event["note_id"]) == id {
			state = event
		}
	}
	return state
}

func legacyNoteReceipt(old map[string]any, id, markdown string) (map[string]any, string, error) {
	bodyHash := hashText(markdown)
	var receipt map[string]any
	if json.Unmarshal([]byte(text(old["receipt_json"])), &receipt) != nil ||
		text(receipt["body_sha256"]) != bodyHash || text(receipt["note_id"]) != id ||
		int(number(old["body_bytes"])) != len([]byte(markdown)) {
		return nil, "", fmt.Errorf("旧笔记正文与保存回执不一致，未导入为有效笔记")
	}
	return receipt, bodyHash, nil
}

func insertLegacyNote(tx *sql.Tx, id string, value map[string]any, revision int, bodyHash string) error {
	var existing string
	err := tx.QueryRow("SELECT json FROM research_records WHERE kind=? AND id=?", "note", id).Scan(&existing)
	if err == nil {
		var current map[string]any
		_ = json.Unmarshal([]byte(existing), &current)
		currentReceipt, _ := current["receipt"].(map[string]any)
		if text(currentReceipt["body_sha256"]) != bodyHash {
			return fmt.Errorf("旧笔记编号与新笔记冲突，两个版本保留待确认")
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	encoded, _ := json.Marshal(value)
	_, err = tx.Exec("INSERT INTO research_records VALUES(?,?,?,?)", "note", id, revision, string(encoded))
	return err
}

func (store *Store) baselineLegacyNotes() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM research_baselines WHERE name=?", "legacy-notes-v1").Scan(&count); err == nil && count > 0 {
		return nil
	}
	legacy, err := store.legacyDatabase()
	if err != nil {
		return err
	}
	if legacy == nil {
		return nil
	}
	defer legacy.Close()
	notes, err := legacyRootRows(legacy, "document_saved_notes", store.RootID)
	if err != nil {
		return err
	}
	events, err := legacyRootRows(legacy, "document_note_lifecycle_events", store.RootID)
	if err != nil {
		return err
	}
	sort.SliceStable(events, func(left, right int) bool {
		return int(number(events[left]["revision"])) < int(number(events[right]["revision"]))
	})
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, old := range notes {
		id := text(old["note_id"])
		if !opPattern.MatchString(id) {
			continue
		}
		markdown := text(old["markdown"])
		receipt, bodyHash, err := legacyNoteReceipt(old, id, markdown)
		if err != nil {
			return err
		}
		state := legacyNoteState(events, id)
		if state != nil && (text(state["body_sha256"]) != bodyHash ||
			(text(state["state"]) != "active" && text(state["state"]) != "archived")) {
			return fmt.Errorf("旧笔记归档回执无效")
		}
		lifecycle := map[string]any{"revision": 0, "state": "active"}
		if state != nil {
			lifecycle = map[string]any{"revision": int(number(state["revision"])), "state": text(state["state"])}
		}
		value := map[string]any{
			"receipt": receipt, "markdown": markdown, "lifecycle": lifecycle,
			"task_id": nil, "taskHash": nil, "parent_note_id": receipt["parent_note_id"], "sourceDigest": nil,
			"legacy": map[string]any{"source": "knowledge.sqlite", "originalReceipt": receipt, "referenceStatus": "not_revalidated"},
		}
		if err := insertLegacyNote(tx, id, value, int(number(lifecycle["revision"])), bodyHash); err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(notes)
	result := map[string]any{
		"count": len(notes), "digest": hashBytes(encoded), "legacyReadOnly": true, "sourceCurrentness": "not_revalidated",
	}
	encodedResult, _ := json.Marshal(result)
	if _, err := tx.Exec("INSERT INTO research_baselines VALUES(?,?)", "legacy-notes-v1", string(encodedResult)); err != nil {
		return err
	}
	return tx.Commit()
}
