package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchesSubtitleLanguage(t *testing.T) {
	if !MatchesSubtitleLanguage("zh-Hans", "zh.*,en.*") {
		t.Fatal("zh-Hans should match")
	}
	if !MatchesSubtitleLanguage("en", "zh.*,en.*") {
		t.Fatal("en should match")
	}
	if MatchesSubtitleLanguage("ja", "zh.*,en.*") {
		t.Fatal("ja should not match")
	}
	if MatchesSubtitleLanguage("en-US", "zh.*,en.*,-en-US") {
		t.Fatal("exclusion should win")
	}
}

func TestNativeVttCues(t *testing.T) {
	cues, err := NativeVttCues("\uFEFFWEBVTT\n\nNOTE x\n\n00:00:01.500 --> 00:00:02.750 line:0\n<b>你好</b> &amp; 世界\n")
	if err != nil || len(cues) != 1 {
		t.Fatalf("vtt mismatch: %+v %v", cues, err)
	}
	if cues[0].Start != 1500 || cues[0].End != 2750 || cues[0].Text != "你好 & 世界" {
		t.Fatalf("vtt cue mismatch: %+v", cues[0])
	}
	if _, err := NativeVttCues("not vtt"); err == nil {
		t.Fatal("invalid vtt must fail")
	}
}

func TestSubtitleParsers(t *testing.T) {
	cues, err := parseSubtitleCues("", []byte(`{"body":[{"from":0.5,"to":1.25,"content":"你好"}]}`))
	if err != nil || len(cues) != 1 || cues[0].Start != 500 || cues[0].Text != "你好" {
		t.Fatalf("bili cues mismatch: %+v %v", cues, err)
	}
	cues, err = parseSubtitleCues("youtube-json3", []byte(`{"events":[
		{"tStartMs":1000,"dDurationMs":500,"segs":[{"utf8":"he"},{"utf8":"llo"}]},
		{"tStartMs":2000,"segs":[{"utf8":"skip"}]}
	]}`))
	if err != nil || len(cues) != 1 || cues[0].Start != 1000 || cues[0].End != 1500 || cues[0].Text != "hello" {
		t.Fatalf("json3 cues mismatch: %+v %v", cues, err)
	}
	if _, err := parseSubtitleCues("", []byte(`[]`)); err == nil {
		t.Fatal("array json must fail")
	}
}

func TestSubtitleFormats(t *testing.T) {
	cues := []SubtitleCue{{Start: 500, End: 1250, Text: "你好"}}
	formats, srt, err := subtitleFormats(cues)
	if err != nil {
		t.Fatal(err)
	}
	if srt != "1\n00:00:00,500 --> 00:00:01,250\n你好\n" {
		t.Fatalf("srt mismatch: %q", srt)
	}
	if !strings.HasPrefix(formats["vtt"].Text, "WEBVTT\n\n00:00:00.500 --> 00:00:01.250") {
		t.Fatalf("vtt mismatch: %q", formats["vtt"].Text)
	}
	if formats["lrc"].Text != "[00:00.50]你好" {
		t.Fatalf("lrc mismatch: %q", formats["lrc"].Text)
	}
	if !strings.Contains(formats["ass"].Text, "Dialogue: 0,0:00:00.50,0:00:01.25,Default,,0,0,0,,你好") {
		t.Fatalf("ass mismatch: %q", formats["ass"].Text)
	}
}

func TestRetimeCues(t *testing.T) {
	cues := []SubtitleCue{{Start: 0, End: 2000, Text: "a"}, {Start: 2000, End: 4000, Text: "b"}}
	result := RetimeCues(cues, []MediaInterval{{Start: 0, End: 1}, {Start: 3, End: 4}})
	if len(result) != 2 || result[0].Start != 0 || result[0].End != 1000 ||
		result[1].Start != 1000 || result[1].End != 2000 {
		t.Fatalf("retime mismatch: %+v", result)
	}
}

func TestDownloadSubtitles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/ai" {
			_, _ = writer.Write([]byte(`{"body":[{"from":0,"to":1,"content":"自动"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"body":[{"from":0.5,"to":1.25,"content":"你好"}]}`))
	}))
	defer server.Close()
	directory := resolvedTemp(t)
	duration := 10.0
	source := Source{
		Catalog:         Catalog{Duration: &duration},
		DownloadHeaders: map[string]string{"Referer": "https://www.bilibili.com/"},
		SubtitleTracks: []SubtitleTrack{
			{Language: "zh-Hans", URL: server.URL + "/zh"},
			{Language: "ai-zh", URL: server.URL + "/ai", Automatic: true},
		},
	}
	fetcher := func(target string, headers map[string]string, ctx context.Context) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(request)
	}
	options, _ := MediaOptions(map[string]any{"subtitles": true, "embed_subtitles": true})
	files, err := DownloadSubtitles(source, options, directory, fetcher, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Relative != "subtitle-zh-Hans.srt" || files[0].EmbedRelative != "embed-zh-Hans.srt" {
		t.Fatalf("files mismatch: %+v", files)
	}
	body, err := os.ReadFile(filepath.Join(directory, files[0].Relative))
	if err != nil || string(body) != "1\n00:00:00,500 --> 00:00:01,250\n你好\n" {
		t.Fatalf("srt content mismatch: %q %v", string(body), err)
	}
	if _, err := DownloadSubtitles(source, options, directory, fetcher, context.Background()); err != nil {
		t.Fatalf("idempotent save failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, files[0].Relative), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DownloadSubtitles(source, options, directory, fetcher, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "内容不同") {
		t.Fatalf("changed artifact mismatch: %v", err)
	}
	onlyAutomatic := Source{Catalog: Catalog{Duration: &duration}, SubtitleTracks: source.SubtitleTracks[1:]}
	if _, err := DownloadSubtitles(onlyAutomatic, options, directory, fetcher, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "所选语言没有可下载字幕") {
		t.Fatalf("automatic mismatch: %v", err)
	}
	duplicate := Source{Catalog: Catalog{Duration: &duration}, SubtitleTracks: []SubtitleTrack{
		{Language: "zh-Hans", URL: server.URL + "/a"}, {Language: "zh-Hans", URL: server.URL + "/b"},
	}}
	if _, err := DownloadSubtitles(duplicate, options, resolvedTemp(t), fetcher, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "字幕语言编号无效或重复") {
		t.Fatalf("duplicate mismatch: %v", err)
	}
}
