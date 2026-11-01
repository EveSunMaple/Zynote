package research

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponsesStaySmallWithHugeMaterials(t *testing.T) {
	texts := []string{}
	for index := 0; index < 6; index++ {
		texts = append(texts, strings.Repeat("泰勒公式余项的长篇讲义正文。", 20000))
	}
	engine, bindings := citationEngine(t, texts)
	longNote := "依据讲义“泰勒公式余项的长篇讲义正文”[S1]。" + strings.Repeat("展开说明。", 5000)
	engine.Model = func(context.Context, string, string) (string, error) { return longNote, nil }
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("e", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(created)
	if len(encoded) > 200000 {
		t.Fatalf("create response is %d bytes; source text must not be sent back", len(encoded))
	}
	listed := engine.List()[0].(map[string]any)
	if listed["note_truncated"] != true || len([]rune(text(listed["note"]))) != listNoteLimit {
		t.Fatalf("list must carry a shortened answer: truncated=%v", listed["note_truncated"])
	}
	full, err := engine.ReadSlim(text(listed["task_id"]))
	if err != nil || text(full.(map[string]any)["note"]) != longNote {
		t.Fatal("reading one task must return the whole answer")
	}
}

func TestRecoveryRetriesAfterFailure(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	broken, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"task_id": strings.Repeat("f", 32), "status": "running", "question": "余项"}
	if err := broken.Put("task", strings.Repeat("f", 32), 1, row); err != nil {
		t.Fatal(err)
	}
	broken.Close()
	(&Engine{Store: broken}).initialize() // 库已关闭，恢复失败，不应标记为已恢复
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task, err := (&Engine{Store: store}).readTask(strings.Repeat("f", 32))
	if err != nil || task.Status != "paused" {
		t.Fatalf("recovery must be retried after a failure: %v %v", task.Status, err)
	}
}

// 列表回包带渠道和资料编号（供“分类建议”入口），但仍不含原文。
func TestTaskRowCarriesChannelsAndMaterialIDs(t *testing.T) {
	row := map[string]any{
		"materials": []any{map[string]any{"text": "原文很长", "binding": map[string]any{"bvid": "LVabc"}}},
		"context":   map[string]any{"channels": []any{"library", "organize"}, "rows": []any{"x"}},
	}
	slim := taskRow(row, 0)
	if _, has := slim["materials"]; has {
		t.Fatal("materials text must not be returned")
	}
	if ids, _ := slim["material_ids"].([]string); len(ids) != 1 || ids[0] != "LVabc" {
		t.Fatalf("material ids missing: %+v", slim)
	}
	if channels, _ := slim["channels"].([]any); len(channels) != 2 {
		t.Fatalf("channels missing: %+v", slim)
	}
}
