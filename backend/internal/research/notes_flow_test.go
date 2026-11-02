package research

import (
	"context"
	"strings"
	"testing"
)

// 研究笔记页签的完整流程：预览 → 保存 → 修订 → 来源 → 导出 → 归档 → 恢复。
func TestResearchNoteLifecycleFlow(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")})
	engine.Model = func(context.Context, string, string) (string, error) {
		return "老师说“余项有两种形式”[S1@00:00:10]。", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("8", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := createdTask(t, engine, created)
	preview, err := engine.PreviewNote(task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SaveNote(task.TaskID, map[string]any{
		"kb_root_id": preview["kb_root_id"], "note_sha256": preview["note_sha256"], "sources_sha256": preview["sources_sha256"],
	}); err != nil {
		t.Fatal(err)
	}
	listing, err := engine.Store.Notes("", "active", 0)
	if err != nil {
		t.Fatal(err)
	}
	first := listing["entries"].([]any)[0].(noteReceipt)
	original, err := engine.Store.readNote(first.NoteID)
	if err != nil {
		t.Fatal(err)
	}
	// 刚保存、研究未变时，来源核验必须报告“仍可核验”，而不是“已改变”。
	assertNoteSource(t, engine, original.Receipt.NoteID, "research_current")
	revised, err := engine.Store.ReviseNote(map[string]any{
		"action": "manual_revision", "schema_version": "1", "operation_id": strings.Repeat("9", 32),
		"parent_note_id": original.Receipt.NoteID, "parent_body_sha256": original.Receipt.BodySHA256,
		"markdown": original.Markdown + "\n\n补充：先看拉格朗日余项。",
	})
	if err != nil {
		t.Fatal(err)
	}
	next, ok := revised.(note)
	if !ok || next.Receipt.NoteID == "" || next.ParentNoteID == nil || *next.ParentNoteID != original.Receipt.NoteID {
		t.Fatalf("revision must return the new note: %#v", revised)
	}
	assertNoteSource(t, engine, next.Receipt.NoteID, "research_current")
	if _, err := engine.Store.ExportNote(next.Receipt.NoteID); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"archive", "restore"} {
		current, err := engine.Store.readNote(next.Receipt.NoteID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Store.LifecycleNote(map[string]any{
			"note_id": next.Receipt.NoteID, "body_sha256": next.Receipt.BodySHA256,
			"expected_revision": current.Lifecycle.Revision, "operation_id": strings.Repeat(string(rune('a'+index)), 32), "action": action,
		}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
}

func assertNoteSource(t *testing.T, engine *Engine, noteID, want string) {
	t.Helper()
	source, err := engine.Store.NoteSource(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if status := source.(map[string]any)["status"]; status != want {
		t.Fatalf("note source status = %v, want %s", status, want)
	}
}
