package download

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type legacyFixture struct {
	root         string
	id           string
	scope        string
	downloadRoot string
	output       string
	legacy       *LegacyDownloads
}

func newLegacyFixture(t *testing.T, id string, state string, options map[string]any) legacyFixture {
	t.Helper()
	root := resolvedTemp(t)
	knowledge := filepath.Join(root, "knowledge_base")
	if err := os.MkdirAll(knowledge, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := NewLegacyDownloads(root)
	scope, err := legacy.scope()
	if err != nil {
		t.Fatal(err)
	}
	downloadRoot := filepath.Join(root, "downloads")
	output := filepath.Join(downloadRoot, "legacy_BV1xx411c7mD")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": 2, "managed_by": "bilisum", "bvid": "BV1xx411c7mD", "ownership_id": id,
		"download_root": strings.ToLower(downloadRoot), "directory": strings.ToLower(output), "media_kind": "video",
	}
	writeLegacyJSON(t, filepath.Join(output, ".bilisum-download.json"), manifest)
	if err := os.WriteFile(filepath.Join(output, "result.mp4"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	row := map[string]any{
		"id": id, "kb_root_id": scope, "title": "旧任务", "state": state, "stage": "已完成",
		"progress": map[string]any{"downloaded": 5, "total": 5}, "error": "", "warning": "",
		"source": map[string]any{
			"url": "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", "storage_id": "BV1xx411c7mD",
			"bvid": "BV1xx411c7mD", "cid": 111, "page": 1,
		},
		"options": options, "download_root": downloadRoot, "output": output, "files": []string{"result.mp4"},
	}
	directory := filepath.Join(root, "records", scope, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLegacyJSON(t, filepath.Join(directory, "state.json"), row)
	return legacyFixture{root: root, id: id, scope: scope, downloadRoot: downloadRoot, output: output, legacy: legacy}
}

func writeLegacyJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyListFileAndRecipe(t *testing.T) {
	id := strings.Repeat("a", 32)
	fixture := newLegacyFixture(t, id, "completed", map[string]any{})
	rows, err := fixture.legacy.List()
	if err != nil || len(rows) != 1 {
		t.Fatalf("list mismatch: %+v %v", rows, err)
	}
	if rows[0]["id"] != id || rows[0]["state"] != "completed" || rows[0]["error_message"] != "" {
		t.Fatalf("row mismatch: %+v", rows[0])
	}
	path, err := fixture.legacy.File(id)
	if err != nil || path != fixture.output {
		t.Fatalf("file mismatch: %s %v", path, err)
	}
	recipe, err := fixture.legacy.Recipe(id)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Identity != "BV1xx411c7mD:111:1" ||
		recipe.URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=1" {
		t.Fatalf("recipe mismatch: %+v", recipe)
	}
}

func TestLegacyInterruptedAndUnreadableRows(t *testing.T) {
	id := strings.Repeat("b", 32)
	fixture := newLegacyFixture(t, id, "running", map[string]any{})
	broken := strings.Repeat("c", 32)
	brokenDirectory := filepath.Join(fixture.root, "records", fixture.scope, broken)
	if err := os.MkdirAll(brokenDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDirectory, "state.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := fixture.legacy.List()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[row["id"].(string)] = row
	}
	if len(rows) != 2 || byID[id]["state"] != "interrupted" || byID[id]["error_message"] != "" {
		t.Fatalf("interrupted mismatch: %+v", rows)
	}
	if byID[broken]["state"] != "error" || !strings.Contains(byID[broken]["error_message"].(string), "无法核对") {
		t.Fatalf("unreadable mismatch: %+v", byID[broken])
	}
}

func TestLegacyFileRejectsPartialOutput(t *testing.T) {
	id := strings.Repeat("d", 32)
	fixture := newLegacyFixture(t, id, "completed", map[string]any{})
	if err := os.WriteFile(filepath.Join(fixture.output, "video.mp4.part"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.legacy.File(id); err == nil || err.Error() != "旧成果仍有未完成分段" {
		t.Fatalf("partial mismatch: %v", err)
	}
}

func TestLegacyRecipeRejectsPreciseFormats(t *testing.T) {
	id := strings.Repeat("e", 32)
	fixture := newLegacyFixture(t, id, "interrupted", map[string]any{"format_ids": "30280"})
	if _, err := fixture.legacy.Recipe(id); err == nil ||
		!strings.Contains(err.Error(), "精确格式编号") {
		t.Fatalf("format mismatch: %v", err)
	}
}

func TestLegacyOtherScopeConflict(t *testing.T) {
	id := strings.Repeat("f", 32)
	fixture := newLegacyFixture(t, id, "completed", map[string]any{})
	otherScope := "kbr_" + strings.Repeat("0", 32)
	if err := os.Rename(
		filepath.Join(fixture.root, "records", fixture.scope),
		filepath.Join(fixture.root, "records", otherScope),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.legacy.List(); err == nil ||
		!strings.Contains(err.Error(), "另一资料路径") {
		t.Fatalf("scope conflict mismatch: %v", err)
	}
}
