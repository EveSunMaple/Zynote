package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVectorSemanticIntegration(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	resources := filepath.Join(root, "resources")
	if _, err := os.Stat(filepath.Join(resources, "knowledge", "onnx-bge", "acquisition.json")); err != nil {
		t.Skip("语义模型未安装")
	}
	native := filepath.Join(root, "native", "target", "debug", "zynote-native")
	if _, err := os.Stat(native); err != nil {
		native = filepath.Join(root, "native", "target", "release", "zynote-native")
	}
	if _, err := os.Stat(native); err != nil {
		t.Skip("原生助手未构建")
	}
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	f := startCore(t, dataRoot, resources)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resources, "promptsRoot": filepath.Join(root, "src"), "nativePath": native,
		"portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("knowledge:import", map[string]any{
		"title": "语义课", "text": "机器学习课堂笔记：特征工程与数据清洗非常重要。",
		"locator": "https://example.com/semantic", "sourceHash": strings.Repeat("c", 64), "category": "课堂",
	}); errReply != nil {
		t.Fatalf("import failed: %+v", errReply)
	}
	if _, errReply := f.call("knowledge:vector-enable", nil); errReply != nil {
		t.Fatalf("enable failed: %+v", errReply)
	}
	raw, errReply := f.call("knowledge:vector-rebuild", nil)
	if errReply != nil || !strings.Contains(string(raw), `"indexed":1`) {
		t.Fatalf("rebuild failed: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:search", map[string]any{"query": "特征工程"})
	if errReply != nil {
		t.Fatalf("search failed: %+v", errReply)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	trace := result["trace"].(map[string]any)["vector"].(map[string]any)
	hits := result["hits"].([]any)
	if trace["state"] != "ready" || len(hits) == 0 {
		t.Fatalf("semantic search mismatch: %s", raw)
	}
}
