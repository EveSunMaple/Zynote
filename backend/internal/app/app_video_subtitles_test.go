package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoSubtitleSaveAndExport(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
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
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "字幕课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	srt := "1\n00:00:01,000 --> 00:00:02,500\n第一句\n\n2\n00:00:03,000 --> 00:00:04,000\n第二 --> 句\n"
	raw, errReply := f.call("video-ppt:subtitle-save", map[string]any{
		"url": "https://www.bilibili.com/video/BV1xx411c7mD/?p=2", "title": "第一讲",
		"srt": srt, "courseId": courseID,
	})
	if errReply != nil {
		t.Fatalf("subtitle-save failed: %+v", errReply)
	}
	var saved string
	if json.Unmarshal(raw, &saved) != nil || !strings.HasSuffix(saved, "BV1xx411c7mD-P2-第一讲.srt") {
		t.Fatalf("saved path mismatch: %s", raw)
	}
	if body, err := os.ReadFile(saved); err != nil || string(body) != srt {
		t.Fatalf("saved body mismatch: %v %q", err, body)
	}
	raw, errReply = f.call("video-ppt:subtitle-save", map[string]any{
		"url": "https://www.bilibili.com/video/BV1xx411c7mD/?p=2", "title": "第一讲",
		"srt": srt, "courseId": courseID,
	})
	if errReply != nil {
		t.Fatalf("second subtitle-save failed: %+v", errReply)
	}
	if json.Unmarshal(raw, &saved) != nil || !strings.HasSuffix(saved, "第一讲 (2).srt") {
		t.Fatalf("reserve collision mismatch: %s", raw)
	}
	raw, errReply = f.call("video-ppt:subtitle-export", map[string]any{
		"format": "md", "title": "第一讲", "srt": srt, "courseId": courseID,
	})
	if errReply != nil {
		t.Fatalf("subtitle-export failed: %+v", errReply)
	}
	var exported string
	_ = json.Unmarshal(raw, &exported)
	body, err := os.ReadFile(exported)
	if err != nil || !strings.HasPrefix(string(body), "# 视频字幕\n\n[00:00:01] 第一句\n\n[00:00:03] 第二 --> 句\n") {
		t.Fatalf("export mismatch: %v %q", err, body)
	}
	raw, errReply = f.call("video-ppt:subtitle-export", map[string]any{
		"format": "vtt", "title": "第一讲", "srt": srt, "courseId": courseID,
	})
	if errReply != nil {
		t.Fatalf("vtt export failed: %+v", errReply)
	}
	_ = json.Unmarshal(raw, &exported)
	body, _ = os.ReadFile(exported)
	if !strings.HasPrefix(string(body), "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.500\n第一句") ||
		!strings.Contains(string(body), "第二 → 句") {
		t.Fatalf("vtt mismatch: %q", body)
	}
	if _, err := os.Stat(filepath.Join(libraryRoot, "Transcripts", "字幕课")); err != nil {
		t.Fatalf("transcript directory missing: %v", err)
	}
}
