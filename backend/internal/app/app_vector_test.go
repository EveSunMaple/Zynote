package app_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVectorStatusAndGuards(t *testing.T) {
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
	raw, errReply := f.call("knowledge:vector-status", nil)
	if errReply != nil {
		t.Fatalf("status failed: %+v", errReply)
	}
	var status map[string]any
	_ = json.Unmarshal(raw, &status)
	if status["active"] != false || status["index"] != "not_built" || status["bundled"] != false ||
		status["model"] != "bge-small-zh-v1-5" {
		t.Fatalf("status mismatch: %s", raw)
	}
	if _, errReply := f.call("knowledge:vector-enable", nil); errReply == nil ||
		!strings.Contains(errReply.Message, "未安装") {
		t.Fatalf("enable guard mismatch: %+v", errReply)
	}
	if _, errReply := f.call("knowledge:vector-rebuild", nil); errReply == nil ||
		!strings.Contains(errReply.Message, "请先启用") {
		t.Fatalf("rebuild guard mismatch: %+v", errReply)
	}
}
