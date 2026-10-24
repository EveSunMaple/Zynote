package app_test

import (
	"strings"
	"testing"
)

func TestVisualListEmpty(t *testing.T) {
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
	raw, errReply := f.call("visual:list", nil)
	if errReply != nil || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("visual:list mismatch: %s %+v", raw, errReply)
	}
	if _, errReply := f.call("visual:recover-output", map[string]any{"id": "bad"}); errReply == nil {
		t.Fatal("invalid visual id must fail")
	}
	if _, errReply := f.call("visual:prepare-resume", map[string]any{"id": strings.Repeat("a", 32)}); errReply == nil ||
		!strings.Contains(errReply.Message, "不存在") {
		t.Fatalf("missing task mismatch: %+v", errReply)
	}
}
