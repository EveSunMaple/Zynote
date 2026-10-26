package research

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreConversationsMemoriesOperations(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conversation, err := store.CreateConversation("第一讲")
	if err != nil {
		t.Fatal(err)
	}
	id := conversation["conversation_id"].(string)
	organized, err := store.OrganizeConversation(map[string]any{
		"id": id, "op": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "revision": 0,
		"title": "改名", "pinned": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if organized.(map[string]any)["state"].(map[string]any)["title"] != "改名" {
		t.Fatalf("organize mismatch: %+v", organized)
	}
	list, err := store.Conversations(0)
	if err != nil || len(list["entries"].([]any)) != 1 {
		t.Fatalf("conversations mismatch: %+v %v", list, err)
	}
	memory, err := store.ChangeMemory(map[string]any{
		"op_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "memory_id": nil, "revision": 0,
		"kind": "preference", "title": "偏好", "body": "先看原文", "paused": false, "expires_at": nil,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	memoryID := memory.(map[string]any)["memory_id"].(string)
	memories, err := store.Memories()
	if err != nil || len(memories["records"].([]map[string]any)) != 1 {
		t.Fatalf("memories mismatch: %+v %v", memories, err)
	}
	changing, err := store.ChangeMemory(map[string]any{
		"op_id": "cccccccccccccccccccccccccccccccc", "memory_id": memoryID, "revision": 1,
		"kind": "preference", "title": "偏好", "body": "先看原文再回答", "paused": false, "expires_at": nil,
	}, false)
	if err != nil || number(changing.(map[string]any)["revision"]) != 2 {
		t.Fatalf("memory revision mismatch: %+v %v", changing, err)
	}
	first, err := store.Operation("dddddddddddddddddddddddddddddddd", map[string]any{"x": 1}, func() (any, error) {
		return map[string]any{"ok": true}, nil
	})
	if err != nil || first.(map[string]any)["ok"] != true {
		t.Fatalf("operation mismatch: %+v %v", first, err)
	}
	second, err := store.Operation("dddddddddddddddddddddddddddddddd", map[string]any{"x": 1}, func() (any, error) {
		t.Fatal("operation should not rerun")
		return nil, nil
	})
	if err != nil || second.(map[string]any)["ok"] != true {
		t.Fatalf("idempotent operation mismatch: %+v %v", second, err)
	}
	if _, err := os.Stat(filepath.Join(root, "research-native.sqlite")); err != nil {
		t.Fatal(err)
	}
}
