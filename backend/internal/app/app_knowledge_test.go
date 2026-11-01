package app_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startHelloCore 启动后端并完成握手，供知识库测试共用。
func startHelloCore(t *testing.T) *fixture {
	t.Helper()
	dataRoot, libraryRoot, resourcesRoot := t.TempDir(), t.TempDir(), t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	hello := map[string]any{"protocol": 1, "platform": testPlatform(), "portable": false, "packaged": false}
	hello["appVersion"], hello["electronVersion"] = "0.0.1", "42.11.10"
	hello["dataRoot"], hello["libraryRoot"] = dataRoot, libraryRoot
	hello["resourcesRoot"], hello["promptsRoot"] = resourcesRoot, resourcesRoot
	if _, errReply := f.call("core.hello", hello); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	return f
}

func TestKnowledgeActions(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	f := startHelloCore(t)
	text := "课堂笔记：机器学习的第一课，重点是特征工程。"
	raw, errReply := f.call("knowledge:import", map[string]any{
		"title": "机器学习", "text": text, "locator": "https://example.com/knowledge",
		"sourceHash": strings.Repeat("a", 64), "category": "课堂", "origin": "local",
	})
	if errReply != nil {
		t.Fatalf("import failed: %+v", errReply)
	}
	var imported map[string]any
	if err := json.Unmarshal(raw, &imported); err != nil {
		t.Fatal(err)
	}
	entry := imported["entry"].(map[string]any)
	id := entry["bvid"].(string)
	generation := entry["generation_id"].(string)

	raw, errReply = f.call("knowledge:generation", map[string]any{"locator": "https://example.com/knowledge"})
	if errReply != nil || !strings.Contains(string(raw), generation) {
		t.Fatalf("generation mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:list", nil)
	if errReply != nil || !strings.Contains(string(raw), `"totalVideos":1`) {
		t.Fatalf("list mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:search", map[string]any{"query": "特征工程"})
	if errReply != nil || !strings.Contains(string(raw), `"evidence_id":"evi_`) {
		t.Fatalf("search mismatch: %s %+v", raw, errReply)
	}
	var search map[string]any
	_ = json.Unmarshal(raw, &search)
	hit := search["hits"].([]any)[0].(map[string]any)
	raw, errReply = f.call("knowledge:citation", map[string]any{"evidenceId": hit["evidence_id"]})
	if errReply != nil || !strings.Contains(string(raw), "课堂笔记") {
		t.Fatalf("citation mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:classification-options", nil)
	if errReply != nil || !strings.Contains(string(raw), `"difficulty"`) {
		t.Fatalf("options mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:classification-read", map[string]any{"id": id})
	if errReply != nil {
		t.Fatalf("classification read failed: %+v", errReply)
	}
	var draft map[string]any
	_ = json.Unmarshal(raw, &draft)
	token := draft["token"].(string)
	raw, errReply = f.call("knowledge:classification-save", map[string]any{
		"token": token,
		"selection": map[string]any{
			"type": "教育", "topic": "机器学习", "subtopics": []string{"特征"},
			"difficulty": "入门", "duration": "短视频", "language": "中文",
			"keywords": []string{"课堂"}, "quality_tier": "A",
		},
	})
	if errReply != nil || !strings.Contains(string(raw), `"persisted":true`) {
		t.Fatalf("classification save mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:organize", map[string]any{
		"items": []map[string]any{{"id": id, "generation": generation}}, "target": "复习",
	})
	if errReply != nil || !strings.Contains(string(raw), `"changed":1`) {
		t.Fatalf("organize mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:delete", map[string]any{"id": id, "generation": generation})
	if errReply != nil || !strings.Contains(string(raw), `"deleted":true`) {
		t.Fatalf("delete mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:rebuild", nil)
	if errReply != nil || !strings.Contains(string(raw), `"rebuilt":true`) {
		t.Fatalf("rebuild mismatch: %s %+v", raw, errReply)
	}
}

type pickedImportItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Result *struct {
		Unchanged bool           `json:"unchanged"`
		Entry     map[string]any `json:"entry"`
	} `json:"result"`
}

func importPicked(f *fixture, t *testing.T) []pickedImportItem {
	t.Helper()
	raw, errReply := f.call("knowledge:import", map[string]any{"category": " 牙体牙髓病学 "})
	if errReply != nil {
		t.Fatalf("picked import failed: %+v", errReply)
	}
	var results []pickedImportItem
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatalf("picked import result is not a list: %s", raw)
	}
	return results
}

func TestKnowledgeImportPickedFiles(t *testing.T) {
	f := startHelloCore(t)
	sources := t.TempDir()
	names := []string{"讲义.txt", "笔记.md", "课堂.srt", "空白.txt", "表格.csv"}
	bodies := []string{
		"第一讲：根管治疗的适应证与禁忌证。",
		"# 复习\n\n牙髓炎的分类要点。",
		"1\n00:00:01,000 --> 00:00:03,000\n今天讲根尖周炎。\n",
		"   \n",
		"a,b\n1,2\n",
	}
	paths := []string{}
	for index, name := range names {
		path := filepath.Join(sources, name)
		if err := os.WriteFile(path, []byte(bodies[index]), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	paths = append(paths, filepath.Join(sources, "缺失.txt"))
	f.picks.set(paths)

	results := importPicked(f, t)
	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %+v", len(paths), results)
	}
	for index := 0; index < 3; index++ {
		got := results[index]
		if got.Name != names[index] || !got.OK || got.Result == nil || got.Result.Unchanged {
			t.Fatalf("import %s mismatch: %+v", names[index], got)
		}
		entry := got.Result.Entry
		if entry["title"] != names[index] || entry["folder"] != "牙体牙髓病学" || entry["source_locator"] != paths[index] {
			t.Fatalf("entry %s mismatch: %+v", names[index], entry)
		}
		sum := sha256.Sum256([]byte(bodies[index]))
		if entry["sourceHash"] != hex.EncodeToString(sum[:]) {
			t.Fatalf("source hash %s mismatch: %+v", names[index], entry)
		}
	}
	for index, want := range map[int]string{3: "资料文字为空", 4: "资料只支持", 5: "资料文件不可读"} {
		if results[index].OK || !strings.Contains(results[index].Error, want) {
			t.Fatalf("result %d should fail with %q: %+v", index, want, results[index])
		}
	}

	raw, errReply := f.call("knowledge:list", nil)
	if errReply != nil || !strings.Contains(string(raw), `"totalVideos":3`) {
		t.Fatalf("list mismatch: %s %+v", raw, errReply)
	}
	raw, errReply = f.call("knowledge:search", map[string]any{"query": "根尖周炎"})
	if errReply != nil || !strings.Contains(string(raw), "根尖周炎") {
		t.Fatalf("search mismatch: %s %+v", raw, errReply)
	}

	f.picks.set(paths[:1])
	again := importPicked(f, t)
	if len(again) != 1 || !again[0].OK || again[0].Result == nil || !again[0].Result.Unchanged {
		t.Fatalf("re-import should be unchanged: %+v", again)
	}

	f.picks.set(nil)
	if cancelled := importPicked(f, t); len(cancelled) != 0 {
		t.Fatalf("cancelled picker should import nothing: %+v", cancelled)
	}
}
