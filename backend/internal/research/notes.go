package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type noteReceipt struct {
	NoteID     string `json:"note_id"`
	Title      string `json:"title"`
	BodySHA256 string `json:"body_sha256"`
	CreatedAt  string `json:"created_at"`
}

type noteLifecycle struct {
	Revision int    `json:"revision"`
	State    string `json:"state"`
}

type note struct {
	Receipt      noteReceipt   `json:"receipt"`
	Markdown     string        `json:"markdown"`
	Lifecycle    noteLifecycle `json:"lifecycle"`
	TaskID       *string       `json:"task_id"`
	TaskHash     *string       `json:"taskHash"`
	ParentNoteID *string       `json:"parent_note_id"`
	SourceDigest *string       `json:"sourceDigest"`
	Legacy       any           `json:"legacy,omitempty"`
}

func parseNote(value map[string]any) (note, error) {
	encoded, _ := json.Marshal(value)
	var parsed note
	if json.Unmarshal(encoded, &parsed) != nil || !opPattern.MatchString(parsed.Receipt.NoteID) {
		return note{}, fmt.Errorf("笔记数据无效")
	}
	return parsed, nil
}

func (store *Store) readNote(id string) (note, error) {
	if !opPattern.MatchString(id) {
		return note{}, fmt.Errorf("笔记标识无效")
	}
	row, err := store.Read("note", id)
	if err != nil {
		return note{}, err
	}
	value, err := parseNote(row)
	if err != nil {
		return note{}, err
	}
	if value.Receipt.NoteID != id || value.Receipt.BodySHA256 != hashText(value.Markdown) {
		return note{}, fmt.Errorf("笔记正文与保存回执不一致，原记录保留")
	}
	return value, nil
}

func (store *Store) Notes(query, state string, offset int) (map[string]any, error) {
	if len([]rune(query)) > 200 || (state != "active" && state != "archived" && state != "all") ||
		offset < 0 || offset > 100000 {
		return map[string]any{}, fmt.Errorf("笔记查询参数无效")
	}
	rows, err := store.List("note")
	if err != nil {
		return nil, err
	}
	lowerQuery := strings.ToLower(query)
	filtered := []note{}
	for _, row := range rows {
		value, err := parseNote(row)
		if err != nil {
			continue
		}
		if state != "all" && value.Lifecycle.State != state {
			continue
		}
		if !strings.Contains(strings.ToLower(value.Receipt.Title+"\n"+value.Markdown), lowerQuery) {
			continue
		}
		filtered = append(filtered, value)
	}
	sort.SliceStable(filtered, func(left, right int) bool {
		return filtered[left].Receipt.CreatedAt > filtered[right].Receipt.CreatedAt
	})
	end := offset + 25
	if end > len(filtered) {
		end = len(filtered)
	}
	entries := []any{}
	if offset < len(filtered) {
		for _, value := range filtered[offset:end] {
			entries = append(entries, value.Receipt)
		}
	}
	return map[string]any{"entries": entries, "has_more": offset+25 < len(filtered)}, nil
}

func (store *Store) NoteRead(id string) (any, error) {
	value, err := store.readNote(id)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (store *Store) ReviseNote(input map[string]any) (any, error) {
	if text(input["action"]) != "manual_revision" || text(input["schema_version"]) != "1" {
		return nil, fmt.Errorf("笔记修订参数无效")
	}
	op := text(input["operation_id"])
	parentID := text(input["parent_note_id"])
	parentHash := text(input["parent_body_sha256"])
	markdown := strings.TrimSpace(text(input["markdown"]))
	if !opPattern.MatchString(op) || !opPattern.MatchString(parentID) || !shaPattern.MatchString(parentHash) ||
		markdown == "" || len([]rune(markdown)) > 40000 {
		return nil, fmt.Errorf("笔记修订参数无效")
	}
	return store.Operation(op, input, func() (any, error) {
		prior, err := store.readNote(parentID)
		if err != nil {
			return nil, err
		}
		if prior.Receipt.BodySHA256 != parentHash || prior.Lifecycle.State != "active" {
			return nil, fmt.Errorf("原笔记已改变或归档，修订未保存")
		}
		nextID := randomID()
		next := note{
			Receipt: noteReceipt{
				NoteID: nextID, Title: prior.Receipt.Title, BodySHA256: hashText(markdown),
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
			Markdown: markdown, Lifecycle: noteLifecycle{Revision: 0, State: "active"},
			TaskID: prior.TaskID, TaskHash: prior.TaskHash, ParentNoteID: &prior.Receipt.NoteID,
			SourceDigest: prior.SourceDigest,
		}
		if err := store.putNote(next); err != nil {
			return nil, err
		}
		return map[string]any{"id": nextID}, nil
	})
}

func (store *Store) putNote(value note) error {
	encoded, _ := json.Marshal(value)
	var row map[string]any
	if json.Unmarshal(encoded, &row) != nil {
		return fmt.Errorf("笔记数据无效")
	}
	return store.Put("note", value.Receipt.NoteID, value.Lifecycle.Revision, row)
}

func (store *Store) LifecycleNote(input map[string]any) (any, error) {
	noteID := text(input["note_id"])
	bodyHash := text(input["body_sha256"])
	operation := text(input["operation_id"])
	action := text(input["action"])
	if !opPattern.MatchString(noteID) || !shaPattern.MatchString(bodyHash) || !opPattern.MatchString(operation) ||
		(action != "archive" && action != "restore") {
		return nil, fmt.Errorf("笔记状态参数无效")
	}
	return store.Operation(operation, input, func() (any, error) {
		value, err := store.readNote(noteID)
		if err != nil {
			return nil, err
		}
		if value.Receipt.BodySHA256 != bodyHash || value.Lifecycle.Revision != int(number(input["expected_revision"])) {
			return nil, fmt.Errorf("笔记状态已改变，请刷新")
		}
		state := "archived"
		if action == "restore" {
			state = "active"
		}
		if value.Lifecycle.State == state {
			return nil, fmt.Errorf("笔记已经处于所选状态")
		}
		value.Lifecycle.Revision++
		value.Lifecycle.State = state
		if err := store.putNote(value); err != nil {
			return nil, err
		}
		return value, nil
	})
}

func (store *Store) NoteSource(id string) (any, error) {
	value, err := store.readNote(id)
	if err != nil {
		return nil, err
	}
	manual := value.ParentNoteID != nil
	if value.TaskID == nil {
		return map[string]any{"status": "legacy_not_revalidated", "manualRevision": manual}, nil
	}
	task, err := store.Read("task", *value.TaskID)
	if err != nil {
		return map[string]any{"status": "research_changed", "manualRevision": manual}, nil
	}
	status := "research_changed"
	if value.TaskHash != nil && text(task["answerHash"]) == *value.TaskHash {
		status = "research_current"
	}
	return map[string]any{"status": status, "manualRevision": manual}, nil
}

func (store *Store) ExportNote(id string) (any, error) {
	value, err := store.readNote(id)
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(store.root, "笔记导出")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("笔记导出目录无效")
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("笔记导出目录无效")
	}
	path := filepath.Join(directory, value.Receipt.NoteID+".md")
	if _, err := os.Stat(path); err == nil {
		body, readErr := os.ReadFile(path)
		if readErr != nil || hashText(string(body)) != value.Receipt.BodySHA256 {
			return nil, fmt.Errorf("导出文件已被修改，保留当前文件")
		}
		return path, nil
	}
	if err := os.WriteFile(path, []byte(value.Markdown), 0o644); err != nil {
		return nil, fmt.Errorf("笔记导出失败")
	}
	body, err := os.ReadFile(path)
	if err != nil || hashText(string(body)) != value.Receipt.BodySHA256 {
		_ = os.Remove(path)
		return nil, fmt.Errorf("笔记导出失败")
	}
	return path, nil
}

func (engine *Engine) PreviewNote(id string) (map[string]any, error) {
	task, err := engine.VerifyResult(id)
	if err != nil {
		return nil, err
	}
	type sourceRef struct {
		ID      string   `json:"id"`
		Binding *Binding `json:"binding"`
		Hash    string   `json:"hash"`
	}
	refs := []sourceRef{}
	for _, source := range task.Sources {
		refs = append(refs, sourceRef{ID: source.SourceID, Binding: source.Binding, Hash: source.SourceHash})
	}
	encodedRefs, _ := json.Marshal(refs)
	sourceDigest := hashText(string(encodedRefs))
	markdown := "# " + task.Question + "\n\n" + task.Note + "\n\n## 范围与限制\n\n" + task.Limitations + "\n\n## 来源版本\n\n"
	sourceLines := []string{}
	for _, source := range task.Sources {
		generation := "external-snapshot"
		if source.Binding != nil && source.Binding.GenerationID != "" {
			generation = source.Binding.GenerationID
		}
		sourceLines = append(sourceLines, fmt.Sprintf("- [%s] %s\n  来源：%s\n  代际：%s\n  原文 SHA256：%s",
			source.SourceID, source.Title, source.URL, generation, source.SourceHash))
	}
	markdown += strings.Join(sourceLines, "\n") + "\n"
	return map[string]any{
		"kb_root_id": engine.Store.RootID, "task_id": id, "note_sha256": hashText(markdown),
		"sources_sha256": sourceDigest, "markdown": markdown,
	}, nil
}

func (engine *Engine) SaveNote(id string, expected map[string]any) (any, error) {
	if text(expected["kb_root_id"]) == "" || !shaPattern.MatchString(text(expected["note_sha256"])) ||
		!shaPattern.MatchString(text(expected["sources_sha256"])) {
		return nil, fmt.Errorf("笔记保存参数无效")
	}
	preview, err := engine.PreviewNote(id)
	if err != nil {
		return nil, err
	}
	if text(expected["kb_root_id"]) != text(preview["kb_root_id"]) ||
		text(expected["note_sha256"]) != text(preview["note_sha256"]) ||
		text(expected["sources_sha256"]) != text(preview["sources_sha256"]) {
		return nil, fmt.Errorf("预览之后研究内容或来源已改变，请重新预览")
	}
	operation := hashText(string(mustJSON([]any{"save-research-note", id, expected})))[:32]
	result, err := engine.Store.Operation(operation, map[string]any{"id": id, "value": expected}, func() (any, error) {
		task, err := engine.VerifyResult(id)
		if err != nil {
			return nil, err
		}
		noteID := randomID()
		taskID := id
		answerHash := text(task.AnswerHash)
		value := note{
			Receipt: noteReceipt{
				NoteID: noteID, Title: task.Question, BodySHA256: text(preview["note_sha256"]),
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			},
			Markdown: text(preview["markdown"]), Lifecycle: noteLifecycle{Revision: 0, State: "active"},
			TaskID: &taskID, TaskHash: &answerHash, SourceDigest: func() *string {
				value := text(preview["sources_sha256"])
				return &value
			}(),
		}
		if err := engine.Store.putNote(value); err != nil {
			return nil, err
		}
		return map[string]any{"id": noteID}, nil
	})
	if err != nil {
		return nil, err
	}
	noteID := text(result.(map[string]any)["id"])
	return engine.Store.readNote(noteID)
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
