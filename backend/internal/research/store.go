package research

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

var opPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var conversationPattern = regexp.MustCompile(`^(?:cv_)?[a-f0-9]{32}$`)

type Store struct {
	db     *sql.DB
	root   string
	RootID string
	mu     sync.Mutex
}

func digestValue(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func randomID() string { return strings.ReplaceAll(ids.New(), "-", "") }

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("研究资料根目录无效")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("研究资料根目录无效")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.EqualFold(resolved, root) {
		return nil, fmt.Errorf("研究资料根目录无效")
	}
	databasePath := filepath.Join(root, "research-native.sqlite")
	if info, err := os.Lstat(databasePath); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("研究资料数据库路径无效")
	}
	knowledgeRoot := filepath.Join(root, "knowledge_base")
	canonical := knowledgeRoot
	if resolvedKnowledge, err := filepath.EvalSymlinks(knowledgeRoot); err == nil {
		canonical = resolvedKnowledge
	}
	rootID := "kbr_" + strings.ToLower(digestValue([]any{"kbr", strings.ToLower(canonical)}))[:32]
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("研究资料数据库不可用")
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("研究资料数据库不可用")
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS research_records(kind TEXT NOT NULL,id TEXT NOT NULL,revision INTEGER NOT NULL,json TEXT NOT NULL,PRIMARY KEY(kind,id));
		CREATE TABLE IF NOT EXISTS research_operations(op TEXT PRIMARY KEY,signature TEXT NOT NULL,result TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS research_baselines(name TEXT PRIMARY KEY,json TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("研究资料数据库不可用")
	}
	return &Store{db: db, root: root, RootID: rootID}, nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) List(kind string) ([]map[string]any, error) {
	rows, err := store.db.Query("SELECT json FROM research_records WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal([]byte(raw), &value) == nil {
			result = append(result, value)
		}
	}
	return result, rows.Err()
}

func (store *Store) Read(kind, id string) (map[string]any, error) {
	var raw string
	err := store.db.QueryRow("SELECT json FROM research_records WHERE kind=? AND id=?", kind, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("所选学习记录不存在")
	}
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return nil, fmt.Errorf("研究记录无效")
	}
	return value, nil
}

func (store *Store) Put(kind, id string, revision int, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(
		"INSERT INTO research_records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET revision=excluded.revision,json=excluded.json",
		kind, id, revision, string(encoded),
	)
	return err
}

func (store *Store) Operation(op string, request any, run func() (any, error)) (any, error) {
	if !opPattern.MatchString(op) {
		return nil, fmt.Errorf("操作编号无效")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	signature := digestValue(request)
	var priorSignature, priorResult string
	err := store.db.QueryRow("SELECT signature,result FROM research_operations WHERE op=?", op).Scan(&priorSignature, &priorResult)
	if err == nil {
		if priorSignature != signature {
			return nil, fmt.Errorf("操作编号已用于不同的修改")
		}
		var value any
		_ = json.Unmarshal([]byte(priorResult), &value)
		return value, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	result, err := run()
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(result)
	if _, err := store.db.Exec("INSERT INTO research_operations VALUES(?,?,?)", op, signature, string(encoded)); err != nil {
		return nil, err
	}
	return result, nil
}

func memoryAvailability(paused bool, expiresAt any) string {
	if paused {
		return "paused"
	}
	if expiresAt != nil {
		switch value := expiresAt.(type) {
		case float64:
			if value <= float64(time.Now().Unix()) {
				return "expired"
			}
		}
	}
	return "active"
}

func (store *Store) Memories() (map[string]any, error) {
	rows, err := store.List("memory")
	if err != nil {
		return nil, err
	}
	records := []map[string]any{}
	for _, row := range rows {
		if deleted, _ := row["deleted"].(bool); deleted {
			continue
		}
		records = append(records, map[string]any{
			"memory_id": row["memory_id"], "revision": row["revision"], "kind": row["kind"],
			"title": row["title"], "body": row["body"], "source_task": row["source_task"],
			"paused": row["paused"], "expires_at": row["expires_at"],
			"created_at": row["created_at"], "updated_at": row["updated_at"],
			"availability": memoryAvailability(row["paused"] == true, row["expires_at"]),
		})
	}
	sort.SliceStable(records, func(left, right int) bool {
		return number(records[left]["updated_at"]) > number(records[right]["updated_at"])
	})
	return map[string]any{
		"kb_root_id": store.RootID, "records": records, "reviewed_pairs": []any{},
		"capacity": map[string]any{
			"used": len(records), "limit": 100, "remaining": maxInt(0, 100-len(records)),
		},
	}, nil
}

func (store *Store) ChangeMemory(input any, deleting bool) (any, error) {
	value, ok := input.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("记忆数据无效")
	}
	op := text(value["op_id"])
	if !opPattern.MatchString(op) {
		return nil, fmt.Errorf("操作编号无效")
	}
	memoryID := text(value["memory_id"])
	if memoryID != "" && !opPattern.MatchString(memoryID) {
		return nil, fmt.Errorf("记忆标识无效")
	}
	revision := int(number(value["revision"]))
	return store.Operation(op, map[string]any{"deleting": deleting, "value": value}, func() (any, error) {
		var prior map[string]any
		if memoryID != "" {
			loaded, err := store.Read("memory", memoryID)
			if err != nil {
				return nil, err
			}
			if loaded["deleted"] == true || int(number(loaded["revision"])) != revision {
				return nil, fmt.Errorf("记忆版本已改变，请刷新后保存")
			}
			prior = loaded
		} else if revision != 0 || deleting {
			return nil, fmt.Errorf("新记忆的版本无效")
		}
		if prior == nil {
			memories, err := store.Memories()
			if err != nil {
				return nil, err
			}
			if int(number(memories["capacity"].(map[string]any)["used"])) >= 100 {
				return nil, fmt.Errorf("学习记忆最多保存100条")
			}
		}
		now := float64(time.Now().Unix())
		id := memoryID
		if id == "" {
			id = randomID()
		}
		nextRevision := revision + 1
		if deleting {
			next := map[string]any{
				"memory_id": id, "revision": nextRevision, "kind": prior["kind"], "title": "", "body": "",
				"source_task": nil, "paused": prior["paused"], "expires_at": prior["expires_at"],
				"created_at": prior["created_at"], "updated_at": now, "deleted": true,
			}
			if err := store.Put("memory", id, nextRevision, next); err != nil {
				return nil, err
			}
			return map[string]any{"memory_id": id, "revision": nextRevision, "deleted": true}, nil
		}
		kind := text(value["kind"])
		title := text(value["title"])
		body := text(value["body"])
		if kind == "" || title == "" || body == "" {
			return nil, fmt.Errorf("记忆类型、标题和正文不能为空")
		}
		sourceTask := value["source_task"]
		if prior != nil && sourceTask != nil && text(sourceTask) != text(prior["source_task"]) {
			return nil, fmt.Errorf("记忆来源必须由已核验的研究结果确认，不接受任意任务编号")
		}
		created := now
		if prior != nil {
			created = number(prior["created_at"])
		}
		paused := prior != nil && prior["paused"] == true
		if value["paused"] != nil {
			paused = value["paused"] == true
		}
		expires := any(nil)
		if prior != nil {
			expires = prior["expires_at"]
		}
		if raw, exists := value["expires_at"]; exists {
			expires = raw
		}
		next := map[string]any{
			"memory_id": id, "revision": nextRevision, "kind": kind, "title": title, "body": body,
			"source_task": sourceTask, "paused": paused, "expires_at": expires,
			"created_at": created, "updated_at": now, "deleted": false,
		}
		if err := store.Put("memory", id, nextRevision, next); err != nil {
			return nil, err
		}
		return map[string]any{"memory_id": id, "revision": nextRevision, "deleted": false}, nil
	})
}

func (store *Store) CreateConversation(title string) (map[string]any, error) {
	if strings.TrimSpace(title) == "" || len([]rune(title)) > 80 {
		return nil, fmt.Errorf("会话标题无效")
	}
	row := map[string]any{
		"conversation_id": "cv_" + randomID(), "title": strings.TrimSpace(title), "revision": 0,
		"pinned": false, "updated_at": time.Now().UTC().Format(time.RFC3339Nano), "active_turn": nil,
	}
	if err := store.Put("conversation", row["conversation_id"].(string), 0, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (store *Store) Conversations(offset int) (map[string]any, error) {
	if offset < 0 || offset > 100000 {
		return nil, fmt.Errorf("偏移无效")
	}
	rows, err := store.List("conversation")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(left, right int) bool {
		leftPinned := rows[left]["pinned"] == true
		rightPinned := rows[right]["pinned"] == true
		if leftPinned != rightPinned {
			return leftPinned
		}
		return text(rows[left]["updated_at"]) > text(rows[right]["updated_at"])
	})
	end := offset + 50
	if end > len(rows) {
		end = len(rows)
	}
	entries := []any{}
	if offset < len(rows) {
		for _, row := range rows[offset:end] {
			entries = append(entries, row)
		}
	}
	return map[string]any{"entries": entries, "has_more": offset+50 < len(rows)}, nil
}

func (store *Store) Conversation(id string) (map[string]any, error) {
	if !conversationPattern.MatchString(id) {
		return nil, fmt.Errorf("会话标识无效")
	}
	row, err := store.Read("conversation", id)
	if err != nil {
		return nil, err
	}
	turns := []any{}
	if historical, err := store.List("legacy:kb_conversation_turns"); err == nil {
		for _, turn := range historical {
			if text(turn["conversation_id"]) == id {
				turns = append(turns, turn)
			}
		}
	}
	if current, err := store.List("turn"); err == nil {
		for _, turn := range current {
			if text(turn["conversation_id"]) == id {
				turns = append(turns, turn)
			}
		}
	}
	researchTasks := []any{}
	for _, kind := range []string{"task", "legacy-task"} {
		if rows, err := store.List(kind); err == nil {
			for _, task := range rows {
				conversationID := text(task["conversation_id"])
				if conversation, ok := task["conversation"].(map[string]any); ok {
					conversationID = text(conversation["conversation_id"])
				}
				if conversationID == id {
					researchTasks = append(researchTasks, task)
				}
			}
		}
	}
	row["turns"] = turns
	row["research_tasks"] = researchTasks
	return row, nil
}

func (store *Store) OrganizeConversation(input map[string]any) (any, error) {
	op := text(input["op"])
	id := text(input["id"])
	if !opPattern.MatchString(op) || !conversationPattern.MatchString(id) {
		return nil, fmt.Errorf("会话参数无效")
	}
	return store.Operation(op, input, func() (any, error) {
		row, err := store.Read("conversation", id)
		if err != nil {
			return nil, err
		}
		if int(number(row["revision"])) != int(number(input["revision"])) {
			return nil, fmt.Errorf("会话版本已改变，请刷新后保存")
		}
		title := strings.TrimSpace(text(input["title"]))
		if title == "" || len([]rune(title)) > 80 {
			return nil, fmt.Errorf("会话标题无效")
		}
		row["title"] = title
		row["pinned"] = input["pinned"] == true
		row["revision"] = int(number(row["revision"])) + 1
		row["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.Put("conversation", id, int(number(row["revision"])), row); err != nil {
			return nil, err
		}
		return map[string]any{"state": row}, nil
	})
}

func text(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	}
	return 0
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
