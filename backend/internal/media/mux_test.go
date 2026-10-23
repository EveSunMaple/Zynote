package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outsideTrack(t *testing.T) string {
	t.Helper()
	directory := resolvedTemp(t)
	path := filepath.Join(directory, "outside.partial")
	if err := os.WriteFile(path, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckedMediaIntervals(t *testing.T) {
	valid, err := CheckedMediaIntervals([]MediaInterval{{Start: 0, End: 2}, {Start: 2, End: 4}}, 10)
	if err != nil || len(valid) != 2 {
		t.Fatalf("valid intervals mismatch: %+v %v", valid, err)
	}
	cases := []struct {
		ranges   []MediaInterval
		duration float64
		message  string
	}{
		{[]MediaInterval{}, 10, "媒体保留时间范围无效"},
		{[]MediaInterval{{Start: 0, End: 20}}, 10, "媒体保留时间范围越界或重叠"},
		{[]MediaInterval{{Start: 2, End: 1}}, 10, "媒体保留时间范围越界或重叠"},
		{[]MediaInterval{{Start: 2, End: 4}, {Start: 3, End: 5}}, 10, "媒体保留时间范围越界或重叠"},
	}
	for _, testCase := range cases {
		if _, err := CheckedMediaIntervals(testCase.ranges, testCase.duration); err == nil ||
			err.Error() != testCase.message {
			t.Fatalf("interval error mismatch: %v", err)
		}
	}
}

func TestRemovalTimelineAndRetime(t *testing.T) {
	result, err := RemovalTimeline(10, []MediaInterval{{Start: 2, End: 4}, {Start: 6, End: 8}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Kept) != 3 || result.Kept[0].End != 2 || result.Kept[1].Start != 4 || result.Kept[2].Start != 8 {
		t.Fatalf("kept mismatch: %+v", result.Kept)
	}
	if result.Duration != 6 {
		t.Fatalf("duration mismatch: %v", result.Duration)
	}
	if _, err := RemovalTimeline(10, []MediaInterval{{Start: 0, End: 10}}, nil); err == nil ||
		!strings.Contains(err.Error(), "没有剩余内容") {
		t.Fatalf("full removal mismatch: %v", err)
	}
	retimed := RetimeChapters([]MuxChapter{{Start: 0, End: 5, Title: "A"}, {Start: 5, End: 10, Title: "B"}}, result.Kept)
	if len(retimed) != 4 || retimed[0].Start != 0 || retimed[0].End != 2 ||
		retimed[1].Start != 2 || retimed[1].End != 3 ||
		retimed[2].Start != 3 || retimed[2].End != 4 ||
		retimed[3].Start != 4 || retimed[3].End != 6 {
		t.Fatalf("retime mismatch: %+v", retimed)
	}
}

func fakeMediaTools(t *testing.T) string {
	t.Helper()
	resources := resolvedTemp(t)
	ffmpeg := "#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\nprintf 'fake-media-bytes' > \"$out\"\n"
	ffprobe := `#!/bin/sh
cat <<'EOF'
{"format":{"duration":"10.0"},"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"chapters":[]}
EOF
`
	fakeTool(t, resources, "ffmpeg", ffmpeg)
	fakeTool(t, resources, "ffprobe", ffprobe)
	return resources
}

func TestMuxWithFakeTools(t *testing.T) {
	resources := fakeMediaTools(t)
	directory := resolvedTemp(t)
	track := filepath.Join(directory, "track-0.partial")
	if err := os.WriteFile(track, []byte("track-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	options, _ := MediaOptions(map[string]any{})
	mux := &NativeMediaMux{Resources: resources, Platform: "darwin"}
	result, err := mux.Mux(MuxInput{
		Directory: directory,
		Tracks:    []LocalTrack{{Path: track, Kind: "video"}},
		Options:   options, ExpectedDuration: 10, Title: "标题", Ctx: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Relative != "result.mkv" || result.Probe.Video != 1 || result.Probe.Audio != 1 || result.Reused {
		t.Fatalf("mux result mismatch: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(directory, "result.mkv")); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "result.mkv.receipt.json")); err != nil {
		t.Fatalf("receipt missing: %v", err)
	}

	reused, err := mux.Mux(MuxInput{
		Directory: directory,
		Tracks:    []LocalTrack{{Path: track, Kind: "video"}},
		Options:   options, ExpectedDuration: 10, Title: "标题", Ctx: context.Background(),
	})
	if err != nil || !reused.Reused {
		t.Fatalf("reuse mismatch: %+v %v", reused, err)
	}
}

func TestMuxValidation(t *testing.T) {
	resources := fakeMediaTools(t)
	directory := resolvedTemp(t)
	track := filepath.Join(directory, "track-0.partial")
	if err := os.WriteFile(track, []byte("track-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	options, _ := MediaOptions(map[string]any{})
	mux := &NativeMediaMux{Resources: resources, Platform: "darwin"}
	base := MuxInput{
		Directory: directory,
		Tracks:    []LocalTrack{{Path: track, Kind: "video"}},
		Options:   options, ExpectedDuration: 10, Title: "标题", Ctx: context.Background(),
	}
	cases := []struct {
		name    string
		mutate  func(*MuxInput)
		message string
	}{
		{"no tracks", func(input *MuxInput) { input.Tracks = nil }, "音视频合并输入无效"},
		{"subtitle only", func(input *MuxInput) { input.Options.SubtitleOnly = true }, "音视频合并输入无效"},
		{"outside track", func(input *MuxInput) { input.Tracks[0].Path = outsideTrack(t) }, "媒体输入不属于该任务"},
		{"bad output name", func(input *MuxInput) { input.OutputName = "x.avi" }, "媒体输出名称无效"},
		{"bad subtitle language", func(input *MuxInput) {
			input.Subtitles = []MuxSubtitle{{Path: track, Language: "中文!"}}
		}, "字幕文件不属于该任务"},
		{"bad chapter", func(input *MuxInput) {
			input.Chapters = []MuxChapter{{Start: 5, End: 4, Title: "x"}}
		}, "章节时间无效"},
	}
	for _, testCase := range cases {
		input := base
		input.Tracks = append([]LocalTrack{}, base.Tracks...)
		input.Subtitles = nil
		input.Chapters = nil
		input.OutputName = ""
		input.Options = base.Options
		testCase.mutate(&input)
		if _, err := mux.Mux(input); err == nil || !strings.Contains(err.Error(), testCase.message) {
			t.Fatalf("%s: expected %q, got %v", testCase.name, testCase.message, err)
		}
	}

	audio, _ := MediaOptions(map[string]any{"audio_only": true})
	if _, err := mux.Mux(MuxInput{
		Directory: directory, Tracks: []LocalTrack{{Path: track, Kind: "video"}},
		Options: audio, ExpectedDuration: 10, Title: "标题", Ctx: context.Background(),
	}); err == nil || !strings.Contains(err.Error(), "没有可用音轨或画面") {
		t.Fatalf("audio only mismatch: %v", err)
	}
}

func TestPrepareCover(t *testing.T) {
	resources := resolvedTemp(t)
	directory := resolvedTemp(t)
	fakeTool(t, resources, "ffprobe", `#!/bin/sh
cat <<'EOF'
{"streams":[{"width":800,"height":600}]}
EOF
`)
	fakeTool(t, resources, "ffmpeg", "#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\nprintf 'cover-bytes' > \"$out\"\n")
	mux := &NativeMediaMux{Resources: resources, Platform: "darwin"}
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	cover, err := mux.PrepareCover(directory, png, false, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cover["relative"] != "cover.jpg" {
		t.Fatalf("cover result mismatch: %+v", cover)
	}
	if _, err := os.Stat(filepath.Join(directory, "cover.jpg")); err != nil {
		t.Fatalf("cover output missing: %v", err)
	}
	if _, err := mux.PrepareCover(directory, []byte("not-an-image"), false, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "封面不是可识别的图片") {
		t.Fatalf("invalid cover mismatch: %v", err)
	}
}

func TestMuxChapterAndSubtitleArgs(t *testing.T) {
	resources := fakeMediaTools(t)
	directory := resolvedTemp(t)
	track := filepath.Join(directory, "track-0.partial")
	if err := os.WriteFile(track, []byte("track-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	subtitle := filepath.Join(directory, "sub.srt")
	if err := os.WriteFile(subtitle, []byte("1\n00:00:01,000 --> 00:00:02,000\n字幕\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	options, _ := MediaOptions(map[string]any{})
	fakeTool(t, resources, "ffprobe", `#!/bin/sh
cat <<'EOF'
{"format":{"duration":"10.0"},"streams":[{"codec_type":"video"},{"codec_type":"audio"},{"codec_type":"subtitle"}],"chapters":[{}]}
EOF
`)
	mux := &NativeMediaMux{Resources: resources, Platform: "darwin"}
	result, err := mux.Mux(MuxInput{
		Directory: directory, Tracks: []LocalTrack{{Path: track, Kind: "video"}},
		Options: options, ExpectedDuration: 10, Title: "标题", Ctx: context.Background(),
		Subtitles: []MuxSubtitle{{Path: subtitle, Language: "zh-CN"}},
		Chapters:  []MuxChapter{{Start: 0, End: 5, Title: "第一章"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "chapters.ffmeta")); err != nil {
		t.Fatalf("chapters file missing: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(directory, "chapters.ffmeta"))
	if !strings.Contains(string(raw), "title=第一章") {
		t.Fatalf("chapters content mismatch: %s", string(raw))
	}
	encoded, _ := json.Marshal(result.Probe)
	if !strings.Contains(string(encoded), "video") {
		t.Fatalf("probe json mismatch: %s", string(encoded))
	}
}
