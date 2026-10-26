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
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
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

func legacyRows(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query("SELECT * FROM " + table)
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
		row := map[string]any{}
		for index, column := range columns {
			switch value := values[index].(type) {
			case []byte:
				row[column] = string(value)
			default:
				row[column] = value
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (store *Store) PracticeQuestions(query, id string) ([]map[string]any, error) {
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
	statement := "SELECT question_id,question,created_at FROM tutor_questions WHERE kb_root_id=? ORDER BY created_at DESC,question_id LIMIT 80"
	arguments := []any{store.RootID}
	if id != "" {
		statement = "SELECT question_id,question,created_at FROM tutor_questions WHERE kb_root_id=? AND question_id=?"
		arguments = append(arguments, id)
	}
	rows, err := legacy.Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type questionRow struct {
		ID        string `json:"question_id"`
		Question  string `json:"question"`
		CreatedAt string `json:"created_at"`
	}
	valid := []questionRow{}
	for rows.Next() {
		var row questionRow
		if err := rows.Scan(&row.ID, &row.Question, &row.CreatedAt); err != nil {
			return nil, err
		}
		if !opPattern.MatchString(row.ID) || strings.TrimSpace(row.Question) == "" ||
			len([]rune(row.Question)) > 2000 || len([]rune(row.CreatedAt)) > 80 {
			continue
		}
		valid = append(valid, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	terms := contextTerms(query)
	score := func(value string) int {
		lower := strings.ToLower(value)
		total := 0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				total++
			}
		}
		return total
	}
	if id != "" {
		result := []map[string]any{}
		for _, row := range valid {
			if row.ID == id {
				encoded, _ := json.Marshal(row)
				value := map[string]any{}
				_ = json.Unmarshal(encoded, &value)
				result = append(result, value)
			}
		}
		return result, nil
	}
	sort.SliceStable(valid, func(left, right int) bool {
		leftScore, rightScore := score(valid[left].Question), score(valid[right].Question)
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
		if score(row.Question) == 0 {
			continue
		}
		encoded, _ := json.Marshal(row)
		value := map[string]any{}
		_ = json.Unmarshal(encoded, &value)
		result = append(result, value)
		if len(result) >= 6 {
			break
		}
	}
	return result, nil
}

func (store *Store) BaselineLegacy() (map[string]any, error) {
	if err := store.baselineLegacyNotes(); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var existing string
	if err := store.db.QueryRow("SELECT json FROM research_baselines WHERE name=?", "python-v1").Scan(&existing); err == nil {
		value := map[string]any{}
		_ = json.Unmarshal([]byte(existing), &value)
		return value, nil
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	legacy, err := store.legacyDatabase()
	if err != nil {
		return nil, err
	}
	if legacy == nil {
		return map[string]any{"imported": false, "reason": "no-legacy-database"}, nil
	}
	defer legacy.Close()
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
			if root, ok := row["kb_root_id"]; ok && root != nil && text(root) != store.RootID {
				continue
			}
			filtered = append(filtered, row)
		}
		snapshot[table] = filtered
	}
	tx, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
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
			return nil, err
		}
	}
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
			return nil, err
		}
	}
	for _, old := range snapshot["learning_research_tasks"] {
		encodedTask := text(old["state_json"])
		var task map[string]any
		if json.Unmarshal([]byte(encodedTask), &task) != nil || text(task["task_id"]) == "" ||
			text(task["kb_root_id"]) != store.RootID {
			return nil, fmt.Errorf("旧研究任务来源不一致")
		}
		encoded, _ := json.Marshal(task)
		if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision,json=excluded.json", "legacy-task", text(task["task_id"]), 0, string(encoded)); err != nil {
			return nil, err
		}
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
	notes := []map[string]any{}
	events := []map[string]any{}
	if legacyTable(legacy, "document_saved_notes") {
		notes, err = legacyRows(legacy, "document_saved_notes")
		if err != nil {
			return err
		}
		filtered := []map[string]any{}
		for _, row := range notes {
			if text(row["kb_root_id"]) == store.RootID {
				filtered = append(filtered, row)
			}
		}
		notes = filtered
	}
	if legacyTable(legacy, "document_note_lifecycle_events") {
		events, err = legacyRows(legacy, "document_note_lifecycle_events")
		if err != nil {
			return err
		}
		filtered := []map[string]any{}
		for _, row := range events {
			if text(row["kb_root_id"]) == store.RootID {
				filtered = append(filtered, row)
			}
		}
		events = filtered
		sort.SliceStable(events, func(left, right int) bool {
			return int(number(events[left]["revision"])) < int(number(events[right]["revision"]))
		})
	}
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
		bodyHash := hashText(markdown)
		var receipt map[string]any
		if json.Unmarshal([]byte(text(old["receipt_json"])), &receipt) != nil ||
			text(receipt["body_sha256"]) != bodyHash || text(receipt["note_id"]) != id ||
			int(number(old["body_bytes"])) != len([]byte(markdown)) {
			return fmt.Errorf("旧笔记正文与保存回执不一致，未导入为有效笔记")
		}
		var state map[string]any
		for _, event := range events {
			if text(event["note_id"]) == id {
				state = event
			}
		}
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
		var existing string
		err := tx.QueryRow("SELECT json FROM research_records WHERE kind=? AND id=?", "note", id).Scan(&existing)
		if err == nil {
			var current map[string]any
			_ = json.Unmarshal([]byte(existing), &current)
			currentReceipt, _ := current["receipt"].(map[string]any)
			if text(currentReceipt["body_sha256"]) != bodyHash {
				return fmt.Errorf("旧笔记编号与新笔记冲突，两个版本保留待确认")
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		encoded, _ := json.Marshal(value)
		if _, err := tx.Exec("INSERT INTO research_records VALUES(?,?,?,?)", "note", id, int(number(lifecycle["revision"])), string(encoded)); err != nil {
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
