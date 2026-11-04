package app_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestSubtitlesGenerateHostileReplacementPreservesOriginalAndDoesNotRepair(t *testing.T) {
	fake := &correctionServer{hostile: true}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	originals := []string{"前三篇主要对应的是牙体，后两片对应的是牙髓。", "呃，你不要拍照片啊，这个现在这个手机都很好，高清照片，一个照片，这是个AM。", "呃，然后转化成PDF文件提交到这个平台上面去。"}
	transcript := "1\n00:00:12,000 --> 00:00:15,000\n" + originals[0] + "\n\n2\n00:00:27,000 --> 00:00:30,000\n" + originals[1] + "\n\n3\n00:00:31,000 --> 00:00:33,000\n" + originals[2] + "\n"
	f := startSubtitleFixtureSource(t, server.URL, false, transcript, true)
	for attempt := 0; attempt < 2; attempt++ {
		raw, errReply := f.core.call("subtitles:generate", map[string]any{"id": f.lectureID, "mode": "text"})
		if errReply != nil {
			t.Fatal(errReply)
		}
		var detail map[string]any
		if json.Unmarshal(raw, &detail) != nil {
			t.Fatal("invalid detail")
		}
		segments := detail["segments"].([]any)
		if len(segments) != 3 {
			t.Fatal("coverage changed", segments)
		}
		for i, item := range segments {
			row := item.(map[string]any)
			if row["text"] != originals[i] || row["originalText"] != originals[i] {
				t.Fatal("source lost", segments)
			}
			if i < 2 && (row["uncertain"] != true || row["reason"] == "") {
				t.Fatal("unsafe correction not marked", segments)
			}
		}
		calls := fake.callsSnapshot()
		if len(calls) != 3 {
			t.Fatal("safety guard caused repair/replay or planner changed", calls)
		}
		for _, call := range calls {
			if call.Compact {
				t.Fatal("withdrawn compact used for fresh default")
			}
		}
	}
}
