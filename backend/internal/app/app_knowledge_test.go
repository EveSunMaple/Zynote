package app_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKnowledgeActions(t *testing.T) {
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
