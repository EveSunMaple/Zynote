package app_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestResearchStorageActions(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	raw, errReply := f.call("tools:request", map[string]any{"action": "conversations"})
	if errReply != nil {
		t.Fatalf("conversations failed: %+v", errReply)
	}
	var conversations map[string]any
	if json.Unmarshal(raw, &conversations) != nil || len(conversations["entries"].([]any)) != 0 {
		t.Fatalf("conversations mismatch: %s", raw)
	}
	raw, errReply = f.call("tools:request", map[string]any{
		"action": "conversation-create", "args": map[string]any{"title": "第一讲"},
	})
	if errReply != nil {
		t.Fatalf("conversation-create failed: %+v", errReply)
	}
	var conversation map[string]any
	_ = json.Unmarshal(raw, &conversation)
	if conversation["title"] != "第一讲" {
		t.Fatalf("conversation mismatch: %s", raw)
	}
	raw, errReply = f.call("tools:request", map[string]any{
		"action": "memory-change", "args": map[string]any{
			"value": map[string]any{
				"op_id": "11111111111111111111111111111111", "memory_id": nil, "revision": 0,
				"kind": "preference", "title": "偏好", "body": "先看原文", "paused": false, "expires_at": nil,
			},
		},
	})
	if errReply != nil {
		t.Fatalf("memory-change failed: %+v", errReply)
	}
	raw, errReply = f.call("tools:request", map[string]any{"action": "memories"})
	if errReply != nil {
		t.Fatalf("memories failed: %+v", errReply)
	}
	var memories map[string]any
	if json.Unmarshal(raw, &memories) != nil || len(memories["records"].([]any)) != 1 {
		t.Fatalf("memories mismatch: %s", raw)
	}
	raw, errReply = f.call("tools:request", map[string]any{"action": "materials"})
	if errReply != nil {
		t.Fatalf("materials failed: %+v", errReply)
	}
	var materials map[string]any
	if json.Unmarshal(raw, &materials) != nil || materials["total"] != float64(0) {
		t.Fatalf("materials mismatch: %s", raw)
	}
	if _, errReply := f.call("tools:request", map[string]any{"action": "agent-create"}); errReply == nil {
		t.Fatal("unsupported research action must fail explicitly")
	}
	raw, errReply = f.call("tools:request", map[string]any{
		"action": "im", "args": map[string]any{"action": "status", "value": map[string]any{}},
	})
	if errReply != nil {
		t.Fatalf("im status failed: %+v", errReply)
	}
	var imStatus map[string]any
	if json.Unmarshal(raw, &imStatus) != nil || len(imStatus["connections"].([]any)) != 0 {
		t.Fatalf("im status mismatch: %s", raw)
	}
}
