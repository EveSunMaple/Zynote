package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

func TestMediaDiscussionImport(t *testing.T) {
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
	store, err := bilibili.NewDiscussionStore(filepath.Join(libraryRoot, "MediaDownloads"))
	if err != nil {
		t.Fatal(err)
	}
	text := "这里说错了，应该是另一个值，可以补充参考。"
	digest, _ := bilibili.DiscussionDigest(text)
	saved, err := store.Save(bilibili.DiscussionPack{
		Schema: "video-discussion-v1", Bvid: "BV1xx411c7mD", Cid: 9, Page: 1, FetchedAt: 1700000000,
		ClassificationMethod: "heuristic_candidates_v1", Policy: bilibili.DiscussionPolicy,
		Coverage: bilibili.DiscussionCoverage{
			Comments: map[string]any{"status": "complete", "complete": true, "errors": []map[string]any{}},
			Danmaku:  map[string]any{"status": "complete", "complete": true, "errors": []map[string]any{}},
		},
		Items: []bilibili.DiscussionItem{{
			ID: "comment:1", Kind: "comment", Text: text, TextSHA256: digest,
			Labels: []string{"correction"}, Score: 6, Verification: "unverified",
			URL: "https://www.bilibili.com/video/BV1xx411c7mD#reply1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, errReply := f.call("media:discussion-import", map[string]any{
		"ref": saved.Ref,
		"changes": []map[string]any{
			{"id": "comment:1", "selected": true, "label": "correction", "note": "备注"},
		},
	})
	if errReply != nil {
		t.Fatalf("discussion import failed: %+v", errReply)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	path := result["path"].(string)
	if !strings.HasSuffix(path, ".md") || !strings.Contains(path, "观众讨论") {
		t.Fatalf("path mismatch: %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "这里说错了") || !strings.Contains(string(body), "人工备注") {
		t.Fatalf("note mismatch: %v", err)
	}
	raw, errReply = f.call("knowledge:list", nil)
	if errReply != nil || !strings.Contains(string(raw), "BV1xx411c7mD · P1 · 评论与弹幕") ||
		!strings.Contains(string(raw), "观众讨论") {
		t.Fatalf("knowledge list mismatch: %s %+v", raw, errReply)
	}
}
