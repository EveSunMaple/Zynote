package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMiscActions(t *testing.T) {
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
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	if _, errReply := f.call("course:create", map[string]any{"name": "课程"}); errReply != nil {
		t.Fatal(errReply)
	}
	raw, errReply := f.call("courses", nil)
	if errReply != nil {
		t.Fatalf("courses failed: %+v", errReply)
	}
	var courses []any
	if json.Unmarshal(raw, &courses) != nil || len(courses) != 1 {
		t.Fatalf("courses mismatch: %s", raw)
	}
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}
	testPickPath = path
	defer func() { testPickPath = "" }()
	for _, action := range []string{"appearance:image", "identity:avatar"} {
		raw, errReply = f.call(action, nil)
		if errReply != nil {
			t.Fatalf("%s failed: %+v", action, errReply)
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || value != "data:image/png;base64,eHh4" {
			t.Fatalf("%s mismatch: %s", action, raw)
		}
	}
	if _, errReply := f.call("zhiyun:login", nil); errReply != nil {
		t.Fatalf("zhiyun login failed: %+v", errReply)
	}
	if _, errReply := f.call("zhiyun:bounds", map[string]any{
		"bounds": map[string]any{"x": 0, "y": 0, "width": 100, "height": 100}, "visible": true,
	}); errReply != nil {
		t.Fatalf("zhiyun bounds failed: %+v", errReply)
	}
}

func TestSkillOpen(t *testing.T) {
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
	path := filepath.Join(t.TempDir(), "skill.md")
	content := "---\nname: test-skill\ndescription: 测试方法\n---\n先读资料再回答。"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	testPickPath = path
	defer func() { testPickPath = "" }()
	raw, errReply := f.call("tools:skill-open", nil)
	if errReply != nil {
		t.Fatalf("skill-open failed: %+v", errReply)
	}
	var skill map[string]any
	if json.Unmarshal(raw, &skill) != nil || skill["name"] != "test-skill" ||
		skill["description"] != "测试方法" || skill["body"] != "先读资料再回答。" || skill["enabled"] != false {
		t.Fatalf("skill mismatch: %s", raw)
	}
}
