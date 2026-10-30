package asr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func fakePipeline(t *testing.T, call CallFunc) (Pipeline, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell script")
	}
	resources, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resources, "asr", "onnx-small"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "asr", "onnx-small", "acquisition.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resources, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\nprintf 'abcdefgh' > \"$out\"\n"
	path := filepath.Join(resources, "video", "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(t.TempDir(), "lecture.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Pipeline{
		Resources: resources, DataRoot: dataRoot,
		Platform: media.CurrentMediaPlatform(), AllowSystemFallback: false, Call: call,
	}, video
}

func TestTranscribePipeline(t *testing.T) {
	updates := []string{}
	pipeline, video := fakePipeline(t, func(ctx context.Context, method string, params any, timeoutMS int) (json.RawMessage, error) {
		if method != "asr.transcribe" {
			t.Fatalf("unexpected method %s", method)
		}
		values := params.(map[string]any)
		if values["language"] != "zh" {
			t.Fatalf("language mismatch: %v", values["language"])
		}
		if info, err := os.Stat(values["path"].(string)); err != nil || info.Size() != 8 {
			t.Fatalf("pcm missing: %v", err)
		}
		return json.RawMessage(`{"duration":2.5,"cues":[{"from":0.2,"to":1.4,"content":" 第一句 "},{"from":1.2,"to":2.4,"content":"第二句"}]}`), nil
	})
	pipeline.Progress = func(message string) { updates = append(updates, message) }
	result, err := pipeline.Transcribe(context.Background(), video, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || !strings.Contains(result.SRT, "第一句") || !strings.Contains(result.SRT, "第二句") {
		t.Fatalf("srt mismatch: %+v", result)
	}
	if !strings.Contains(result.SRT, "00:00:00,200 --> 00:00:01,200") {
		t.Fatalf("timestamp mismatch: %s", result.SRT)
	}
	if len(updates) != 2 {
		t.Fatalf("progress mismatch: %v", updates)
	}
	recovery, err := os.ReadFile(filepath.Join(result.Directory, "recovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(recovery, &state); err != nil {
		t.Fatal(err)
	}
	if state["state"] != "ready-to-save" || state["count"] != float64(2) {
		t.Fatalf("recovery mismatch: %s", recovery)
	}
	if _, err := os.Stat(filepath.Join(result.Directory, "result.srt")); err != nil {
		t.Fatal(err)
	}
	if err := result.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Directory); !os.IsNotExist(err) {
		t.Fatalf("cleanup failed: %v", err)
	}
}

func TestTranscribeCancelled(t *testing.T) {
	pipeline, video := fakePipeline(t, func(context.Context, string, any, int) (json.RawMessage, error) {
		t.Fatal("native call must not start after cancellation")
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pipeline.Transcribe(ctx, video, "zh"); err == nil || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("cancel mismatch: %v", err)
	}
}

func TestTranscribeRejectsInvalidCues(t *testing.T) {
	pipeline, video := fakePipeline(t, func(context.Context, string, any, int) (json.RawMessage, error) {
		return json.RawMessage(`{"duration":2,"cues":[{"from":1.5,"to":1.2,"content":"倒序"}]}`), nil
	})
	if _, err := pipeline.Transcribe(context.Background(), video, "zh"); err == nil || !strings.Contains(err.Error(), "无法读取") {
		t.Fatalf("invalid cue mismatch: %v", err)
	}
}

func TestTranscribeRequiresModel(t *testing.T) {
	pipeline, video := fakePipeline(t, func(context.Context, string, any, int) (json.RawMessage, error) {
		return nil, nil
	})
	if err := os.Remove(filepath.Join(pipeline.Resources, "asr", "onnx-small", "acquisition.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.Transcribe(context.Background(), video, "zh"); err == nil || !strings.Contains(err.Error(), "运行组件") {
		t.Fatalf("model guard mismatch: %v", err)
	}
}

func TestSegmentsClampOverlap(t *testing.T) {
	segments := Segments([]Cue{
		{From: 1.2, To: 2.4, Content: "第二句"},
		{From: 0.2, To: 1.4, Content: "第一句"},
		{From: 3, To: 3, Content: "空"},
	})
	if len(segments) != 2 {
		t.Fatalf("segments mismatch: %+v", segments)
	}
	if segments[0].Start != 200 || segments[0].End != 1200 || segments[1].End != 2400 {
		t.Fatalf("timing mismatch: %+v", segments)
	}
}
