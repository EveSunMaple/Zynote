package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryOpenActions(t *testing.T) {
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
	path := filepath.Join(libraryRoot, "Notes", "测试.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, errReply := f.call("library:open", nil)
	if errReply != nil {
		t.Fatalf("library:open failed: %+v", errReply)
	}
	if strings.TrimSpace(string(raw)) != `"`+libraryRoot+`"` {
		t.Fatalf("library root mismatch: %s", raw)
	}
	if _, errReply := f.call("library:open-file", map[string]any{"path": path}); errReply != nil {
		t.Fatalf("library:open-file failed: %+v", errReply)
	}
	if _, errReply := f.call("library:reveal", map[string]any{"path": path}); errReply != nil {
		t.Fatalf("library:reveal failed: %+v", errReply)
	}
	if _, errReply := f.call("library:open-file", map[string]any{"path": "/etc/passwd"}); errReply == nil {
		t.Fatal("outside path must fail")
	}
	bad := filepath.Join(libraryRoot, "bad.exe")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errReply := f.call("library:open-file", map[string]any{"path": bad}); errReply == nil ||
		!strings.Contains(errReply.Message, "仅支持") {
		t.Fatalf("extension guard mismatch: %+v", errReply)
	}
}
