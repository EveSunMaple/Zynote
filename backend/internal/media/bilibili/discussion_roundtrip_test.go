package bilibili

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func crossLanguagePack() DiscussionPack {
	text := "这里说错了，应该是「五」不是三。\n补充：<参考> & 出处"
	digest, _ := DiscussionDigest(text)
	replyText := "同意，补充一个反例。"
	replyDigest, _ := DiscussionDigest(replyText)
	label := "correction"
	likes := int64(7)
	author := "观众"
	createdAt := int64(1700000000)
	empty := ""
	return DiscussionPack{
		Schema: "video-discussion-v1", Bvid: "BV1xx411c7mD", Cid: 99, Page: 1, FetchedAt: 1700000000,
		ClassificationMethod: "heuristic_candidates_v1", Policy: DiscussionPolicy,
		Coverage: DiscussionCoverage{
			Comments: map[string]any{"status": "complete", "complete": true, "errors": []map[string]any{}},
			Danmaku: map[string]any{
				"status": "partial", "complete": false, "errors": []map[string]any{},
				"segments_total": 1, "segments_requested": []int{1}, "segments_ok": []int{1},
			},
		},
		Items: []DiscussionItem{
			{
				ID: "comment:1", Kind: "comment", Text: text, TextSHA256: digest,
				Labels: []string{"correction", "supplement"}, Score: 9.5, Verification: "unverified",
				URL:    "https://www.bilibili.com/video/BV1xx411c7mD#reply1",
				Author: &author, Likes: &likes, IsUploader: boolPointer(false), Pinned: boolPointer(false),
				CreatedAt: &createdAt, RootID: &empty, ParentID: &empty,
				ReplyCount:      func() *int64 { value := int64(1); return &value }(),
				RepliesComplete: func() *bool { value := true; return &value }(),
			},
			{
				ID: "reply:2", Kind: "reply", Text: replyText, TextSHA256: replyDigest,
				Labels: []string{label}, Score: 1, Verification: "unverified",
				URL:    "https://www.bilibili.com/video/BV1xx411c7mD#reply2",
				RootID: stringPointer("comment:1"), ParentID: stringPointer("comment:1"),
				Author: stringPointer("观众"), Likes: &likes, IsUploader: boolPointer(false),
				CreatedAt: &createdAt,
			},
		},
	}
}

func TestDiscussionCrossLanguage(t *testing.T) {
	directory := os.Getenv("ZYNOTE_DISCUSSION_DIR")
	if directory == "" {
		t.Skip("未请求跨语言讨论证据测试")
	}
	store, err := NewDiscussionStore(filepath.Join(directory, "MediaDownloads"))
	if err != nil {
		t.Fatal(err)
	}
	mode := os.Getenv("ZYNOTE_DISCUSSION_MODE")
	result := map[string]any{}
	if mode == "select" {
		pack, text, err := store.Select(os.Getenv("ZYNOTE_DISCUSSION_REF"), []DiscussionChange{
			{ID: "comment:1", Selected: true, Label: "correction", Note: "跨语言备注"},
		})
		if err != nil {
			t.Fatal(err)
		}
		result["ref"] = pack.Ref
		result["text"] = text
	} else {
		saved, err := store.Save(crossLanguagePack())
		if err != nil {
			t.Fatal(err)
		}
		result["ref"] = saved.Ref
		result["digest"] = saved.Items[0].TextSHA256
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go-result.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}
