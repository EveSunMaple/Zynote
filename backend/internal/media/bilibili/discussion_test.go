package bilibili

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func discussionPackFixture() DiscussionPack {
	labels, score := ClassifyDiscussion("这里说错了，应该是另一个值，可以补充参考。")
	text := "这里说错了，应该是另一个值，可以补充参考。"
	digest, _ := DiscussionDigest(text)
	parentID := ""
	rootID := ""
	return DiscussionPack{
		Schema: "video-discussion-v1", Bvid: "BV1xx411c7mD", Cid: 99, Page: 1, FetchedAt: 1700000000,
		ClassificationMethod: "heuristic_candidates_v1", Policy: DiscussionPolicy,
		Coverage: DiscussionCoverage{
			Comments: map[string]any{"status": "complete", "complete": true, "errors": []map[string]any{}},
			Danmaku:  map[string]any{"status": "complete", "complete": true, "errors": []map[string]any{}},
		},
		Items: []DiscussionItem{
			{
				ID: "comment:1", Kind: "comment", Text: text, TextSHA256: digest, Labels: labels, Score: score,
				Verification: "unverified", URL: "https://www.bilibili.com/video/BV1xx411c7mD#reply1",
				RootID: &rootID, ParentID: &parentID,
			},
			{
				ID: "reply:2", Kind: "reply", Text: "同意，补充一个反例。", TextSHA256: "x",
				Verification: "unverified", URL: "https://www.bilibili.com/video/BV1xx411c7mD#reply2",
				RootID: stringPointer("comment:1"), ParentID: stringPointer("comment:1"),
			},
		},
	}
}

func TestClassifyDiscussion(t *testing.T) {
	labels, score := ClassifyDiscussion("这里说错了，应该是五不是三")
	if len(labels) != 1 || labels[0] != "correction" || score != 6 {
		t.Fatalf("correction mismatch: %v %v", labels, score)
	}
	labels, score = ClassifyDiscussion("前排")
	if len(labels) != 1 || labels[0] != "discussion" || score != 0 {
		t.Fatalf("noise mismatch: %v %v", labels, score)
	}
	labels, _ = ClassifyDiscussion("论文出处见 https://example.com，重点是结论")
	if len(labels) != 2 || labels[0] != "supplement" || labels[1] != "marker" {
		t.Fatalf("multi label order mismatch: %v", labels)
	}
	_, score = ClassifyDiscussion(strings.Repeat("这", 30))
	if score != 1 {
		t.Fatalf("long discussion score mismatch: %v", score)
	}
}

func TestCanonicalJSON(t *testing.T) {
	encoded, err := CanonicalJSON(map[string]any{"b": 1, "a": "<x>", "list": []any{true, nil}})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != `{"a":"<x>","b":1,"list":[true,null]}` {
		t.Fatalf("canonical mismatch: %s", encoded)
	}
	first, _ := DiscussionDigest(map[string]any{"b": 1, "a": "<x>"})
	second, _ := DiscussionDigest(map[string]any{"a": "<x>", "b": 1})
	if first != second || len(first) != 64 {
		t.Fatalf("digest mismatch: %s %s", first, second)
	}
}

func TestRenderDiscussionIncludesRootContext(t *testing.T) {
	pack := discussionPackFixture()
	pack.Items[1].Selected = true
	text, err := renderDiscussion(&pack)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "## 简介与讨论证据") || !strings.Contains(text, "重要 · 子评论") ||
		!strings.Contains(text, "上下文 · 主评论") || !strings.Contains(text, "> 这里说错了") {
		t.Fatalf("render mismatch:\n%s", text)
	}
	empty := discussionPackFixture()
	text, _ = renderDiscussion(&empty)
	if text != "" {
		t.Fatalf("empty render mismatch: %s", text)
	}
}

func TestParseDanmakuXML(t *testing.T) {
	rows, closed, err := parseDanmaku([]byte(`<i><d p="1.5,1,25,16777215,1700000000,0,abc,dan1">hello</d><d p="2.5,8,25,16777215,1700000001,0,abc,dan2">skip</d></i>`), 0)
	if err != nil || closed || len(rows) != 1 {
		t.Fatalf("xml parse mismatch: %+v %v %v", rows, closed, err)
	}
	if rows[0].id != "dan1" || rows[0].text != "hello" || rows[0].timeSec != 1.5 || rows[0].createdAt != 1700000000 {
		t.Fatalf("xml row mismatch: %+v", rows[0])
	}
	if _, _, err := parseDanmaku([]byte(`<!DOCTYPE i><i></i>`), 0); err == nil || err.Error() != "invalid_xml" {
		t.Fatalf("doctype mismatch: %v", err)
	}
	if _, _, err := parseDanmaku([]byte(`{"a":1}`), 0); err == nil || err.Error() != "json_error_not_danmaku" {
		t.Fatalf("json mismatch: %v", err)
	}
}

func TestParseDanmakuProtobuf(t *testing.T) {
	inner := []byte{}
	inner = append(inner, protoVarintField(2, 1500)...)
	inner = append(inner, protoVarintField(3, 1)...)
	inner = append(inner, protoBytesField(7, []byte("你好"))...)
	inner = append(inner, protoBytesField(12, []byte("dm-1"))...)
	inner = append(inner, protoVarintField(8, 1700000000)...)
	outer := append(protoBytesField(1, inner), protoVarintField(2, 1)...)
	rows, closed, err := parseDanmaku(outer, 0)
	if err != nil || !closed || len(rows) != 1 {
		t.Fatalf("protobuf parse mismatch: %+v %v %v", rows, closed, err)
	}
	if rows[0].id != "dm-1" || rows[0].text != "你好" || rows[0].timeSec != 1.5 || rows[0].createdAt != 1700000000 {
		t.Fatalf("protobuf row mismatch: %+v", rows[0])
	}
}

func protoVarintField(number int, value uint64) []byte {
	result := appendVarint(nil, uint64(number)<<3)
	return appendVarint(result, value)
}

func protoBytesField(number int, value []byte) []byte {
	result := appendVarint(nil, uint64(number)<<3|2)
	result = appendVarint(result, uint64(len(value)))
	return append(result, value...)
}

func appendVarint(target []byte, value uint64) []byte {
	for value >= 128 {
		target = append(target, byte(value)|128)
		value >>= 7
	}
	return append(target, byte(value))
}

func TestDiscussionStoreRoundTrip(t *testing.T) {
	mediaRoot := filepath.Join(t.TempDir(), "MediaDownloads")
	store, err := NewDiscussionStore(mediaRoot)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := store.Save(discussionPackFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Ref) != 64 {
		t.Fatalf("ref mismatch: %s", pack.Ref)
	}
	if pack.Items[0].Selected {
		t.Fatal("unreviewed item must not stay selected")
	}
	selected, text, err := store.Select(pack.Ref, []DiscussionChange{
		{ID: "comment:1", Selected: true, Label: "correction", Note: "人工备注"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Ref == pack.Ref || !strings.Contains(text, "重要 · 主评论") || !strings.Contains(text, "人工备注") {
		t.Fatalf("select mismatch: %s\n%s", selected.Ref, text)
	}
	if _, _, err := store.Select(pack.Ref, []DiscussionChange{
		{ID: "comment:1", Selected: true, Label: "correction"},
	}); err == nil || err.Error() != "讨论列表已有更新，请重新读取" {
		t.Fatalf("stale ref mismatch: %v", err)
	}
	resaved, err := store.Save(discussionPackFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !resaved.Items[0].Selected || resaved.Items[0].Note != "人工备注" || !resaved.Items[0].Reviewed {
		t.Fatalf("review merge mismatch: %+v", resaved.Items[0])
	}
	if _, _, err := store.Select(pack.Ref, []DiscussionChange{{ID: "missing", Selected: true, Label: "marker"}}); err == nil {
		t.Fatal("missing item must fail")
	}
}

func TestCollectDiscussion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/x/web-interface/view":
			fmt.Fprint(writer, `{"code":0,"data":{"bvid":"BV1xx411c7mD","aid":42,"desc":"简介","owner":{"mid":7},"pages":[{"page":1,"cid":99,"duration":400}]}}`)
		case "/x/v2/reply":
			fmt.Fprint(writer, `{"code":0,"data":{"replies":[{"rpid_str":"1","member":{"uname":"观众","mid":5},"content":{"message":"这里说错了，应该是五"},"like":7,"ctime":1700000000,"replies":[]}],"top_replies":[],"top":{},"page":{"count":1,"size":20},"cursor":{}}}`)
		case "/x/v2/dm/web/seg.so":
			writer.Header().Set("Content-Type", "text/xml")
			fmt.Fprintf(writer, `<i><d p="1.5,1,25,16777215,1700000000,0,abc,dan-%s">弹幕 %s</d></i>`, request.URL.Query().Get("segment_index"), request.URL.Query().Get("segment_index"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	resources := testResources(t, server.URL, nil)
	store, err := NewDiscussionStore(root)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := NewDiscussion(resources, store).Collect("https://www.bilibili.com/video/BV1xx411c7mD/?p=1", 2)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	danmakuID := ""
	for _, item := range pack.Items {
		kinds[item.Kind]++
		if item.Kind == "danmaku" && danmakuID == "" {
			danmakuID = item.ID
		}
	}
	if kinds["description"] != 1 || kinds["comment"] != 1 || kinds["danmaku"] != 2 || danmakuID == "" {
		t.Fatalf("item kinds mismatch: %v", kinds)
	}
	if pack.Bvid != "BV1xx411c7mD" || pack.Cid != 99 || pack.Page != 1 || len(pack.Ref) != 64 {
		t.Fatalf("pack mismatch: %+v", pack)
	}
	selected, text, err := store.Select(pack.Ref, []DiscussionChange{
		{ID: danmakuID, Selected: true, Label: "marker", Note: ""},
	})
	if err != nil || selected.Ref == pack.Ref || !strings.Contains(text, "重要 · 弹幕") {
		t.Fatalf("select mismatch: %v\n%s", err, text)
	}
}
