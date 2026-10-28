package app_test

import (
	"encoding/json"
	"testing"
)

func TestMediaReadOnlyActions(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	raw, errReply := f.call("media:options", nil)
	if errReply != nil {
		t.Fatalf("media:options failed: %+v", errReply)
	}
	var options map[string]any
	if json.Unmarshal(raw, &options) != nil || options["defaults"] == nil {
		t.Fatalf("media options mismatch: %s", raw)
	}
	raw, errReply = f.call("media:list", nil)
	if errReply != nil {
		t.Fatalf("media:list failed: %+v", errReply)
	}
	var mediaList []any
	if json.Unmarshal(raw, &mediaList) != nil || len(mediaList) != 0 {
		t.Fatalf("media list mismatch: %s", raw)
	}
	if _, errReply := f.call("media:auth-list", nil); errReply != nil {
		t.Fatalf("media:auth-list failed: %+v", errReply)
	}
}
