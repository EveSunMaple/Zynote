package bilibili

import (
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func selectSource() Source {
	return Source{Streams: []MediaStream{
		{ID: "v1080-7", Kind: "video", Height: 1080, Codec: "avc1.640028", Bandwidth: 4000000, Extension: "mp4"},
		{ID: "v720-7", Kind: "video", Height: 720, Codec: "avc1.64001f", Bandwidth: 2000000, Extension: "mp4"},
		{ID: "v720-12", Kind: "video", Height: 720, Codec: "hev1.1.6.L120", Bandwidth: 1500000, Extension: "mp4"},
		{ID: "a320-0", Kind: "audio", Codec: "mp4a.40.2", Bandwidth: 320000, Extension: "m4a"},
		{ID: "a128-0", Kind: "audio", Codec: "mp4a.40.2", Bandwidth: 128000, Extension: "m4a"},
		{ID: "c80", Kind: "combined", Height: 0, Codec: "", Bandwidth: 0, Extension: "flv"},
	}}
}

func TestSelectStreamsAuto(t *testing.T) {
	options, _ := media.MediaOptions(map[string]any{"height": 720})
	picked, err := SelectStreams(selectSource(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 2 || picked[0].ID != "v720-7" || picked[1].ID != "a320-0" {
		t.Fatalf("auto selection mismatch: %+v", picked)
	}
}

func TestSelectStreamsCodecAndAudio(t *testing.T) {
	options, _ := media.MediaOptions(map[string]any{"height": 720, "video_codec": "h265", "audio_quality": "128"})
	picked, err := SelectStreams(selectSource(), options)
	if err != nil {
		t.Fatal(err)
	}
	if picked[0].ID != "v720-12" || picked[1].ID != "a128-0" {
		t.Fatalf("codec/audio selection mismatch: %+v", picked)
	}
}

func TestSelectStreamsAudioOnly(t *testing.T) {
	options, _ := media.MediaOptions(map[string]any{"audio_only": true})
	picked, err := SelectStreams(selectSource(), options)
	if err != nil || len(picked) != 1 || picked[0].ID != "a320-0" {
		t.Fatalf("audio only mismatch: %+v %v", picked, err)
	}
	noAudio := Source{Streams: []MediaStream{{ID: "c1", Kind: "combined", Extension: "flv"}}}
	picked, err = SelectStreams(noAudio, options)
	if err != nil || picked[0].ID != "c1" {
		t.Fatalf("combined fallback mismatch: %+v %v", picked, err)
	}
	if _, err := SelectStreams(Source{Streams: []MediaStream{{ID: "v1", Kind: "video", Height: 720}}}, options); err == nil ||
		!strings.Contains(err.Error(), "此视频没有可用音轨") {
		t.Fatalf("missing audio error mismatch: %v", err)
	}
}

func TestSelectStreamsExact(t *testing.T) {
	options, _ := media.MediaOptions(map[string]any{"format_ids": "v720-7+a320-0"})
	picked, err := SelectStreams(selectSource(), options)
	if err != nil || len(picked) != 2 {
		t.Fatalf("exact selection mismatch: %+v %v", picked, err)
	}
	missing, _ := media.MediaOptions(map[string]any{"format_ids": "v999-7"})
	if _, err := SelectStreams(selectSource(), missing); err == nil || !strings.Contains(err.Error(), "所选格式已失效") {
		t.Fatalf("missing format error mismatch: %v", err)
	}
	duplicate, _ := media.MediaOptions(map[string]any{"format_ids": "v720-7+v720-7"})
	if _, err := SelectStreams(selectSource(), duplicate); err == nil {
		t.Fatalf("duplicate format should fail")
	}
	audioConflict, _ := media.MediaOptions(map[string]any{"format_ids": "v720-7", "audio_only": true})
	if _, err := SelectStreams(selectSource(), audioConflict); err == nil {
		t.Fatalf("audio conflict should fail")
	}
}

func TestSelectStreamsCombinedFallback(t *testing.T) {
	options, _ := media.MediaOptions(map[string]any{})
	source := Source{Streams: []MediaStream{{ID: "c80", Kind: "combined", Extension: "flv"}}}
	picked, err := SelectStreams(source, options)
	if err != nil || len(picked) != 1 || picked[0].ID != "c80" {
		t.Fatalf("combined fallback mismatch: %+v %v", picked, err)
	}
	capped, _ := media.MediaOptions(map[string]any{"height": 480})
	none := Source{Streams: []MediaStream{{ID: "v1", Kind: "video", Height: 1080}}}
	if _, err := SelectStreams(none, capped); err == nil || !strings.Contains(err.Error(), "没有符合画质和编码要求的格式") {
		t.Fatalf("no match error mismatch: %v", err)
	}
}
