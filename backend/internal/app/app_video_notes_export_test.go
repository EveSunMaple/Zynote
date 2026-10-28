package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoNotesExport(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	directory := filepath.Join(libraryRoot, "Notes", "课程")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	markdown := "# 课程要点\n\n- **核心结论**（00:00:01；原话：第一句）"
	source := filepath.Join(directory, "第一讲-课程要点.md")
	if err := os.WriteFile(source, []byte(markdown), 0o644); err != nil {
		t.Fatal(err)
	}
	points := []map[string]any{{"cue": 0, "quote": "第一句", "point": "核心结论", "topic": "主题", "timestamp": "00:00:01"}}
	encoded, _ := json.Marshal(points)
	if err := os.WriteFile(filepath.Join(directory, "第一讲-课程要点.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"docx", "svg", "pdf"} {
		raw, errReply := f.call("video-ppt:notes-export", map[string]any{"path": source, "format": format})
		if errReply != nil {
			t.Fatalf("notes-export %s failed: %+v", format, errReply)
		}
		var path string
		_ = json.Unmarshal(raw, &path)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		switch format {
		case "docx":
			if len(body) < 4 || string(body[:2]) != "PK" {
				t.Fatalf("docx mismatch: %q", body[:minInt(4, len(body))])
			}
		case "svg":
			if !strings.Contains(string(body), "<svg") || !strings.Contains(string(body), "核心结论") {
				t.Fatalf("svg mismatch: %s", body)
			}
		case "pdf":
			if !strings.HasPrefix(string(body), "%PDF-") {
				t.Fatalf("pdf mismatch: %q", body)
			}
		}
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
