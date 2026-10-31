package asr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestMain(m *testing.M) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "ffmpeg" && os.Getenv("ZYNOTE_ASR_TEST_DECODE") != "" {
		if os.Getenv("ZYNOTE_ASR_TEST_DECODE") == "wait" {
			_ = os.WriteFile(os.Getenv("ZYNOTE_ASR_TEST_STARTED"), []byte("started"), 0600)
			for {
				time.Sleep(time.Second)
			}
		}
		if err := os.WriteFile(os.Args[len(os.Args)-1], []byte("abcdefgh"), 0600); err != nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func contextPipeline(t *testing.T, mode string, call CallFunc) (Pipeline, string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resources := filepath.Join(root, "resources")
	for _, directory := range []string{filepath.Join(resources, "video"), filepath.Join(resources, "asr", "onnx-small")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(resources, "asr", "onnx-small", "acquisition.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	platform, suffix := runtime.GOOS, ""
	if platform == "windows" {
		platform = "win32"
		suffix = ".exe"
	}
	if err := os.WriteFile(filepath.Join(resources, "video", "ffmpeg"+suffix), body, 0700); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(root, "source.mp4")
	if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "started")
	t.Setenv("ZYNOTE_ASR_TEST_DECODE", mode)
	t.Setenv("ZYNOTE_ASR_TEST_STARTED", started)
	return Pipeline{Resources: resources, DataRoot: filepath.Join(root, "data"), Platform: platform, Call: call}, video, started
}

func TestDecodeCancellationKeepsTypedCause(t *testing.T) {
	pipeline, video, started := contextPipeline(t, "wait", func(context.Context, string, any, int) (json.RawMessage, error) {
		t.Fatal("cancelled decode dispatched ASR")
		return nil, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := pipeline.Transcribe(ctx, video, "en"); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("decode process did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel lost %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("decode cancel stalled")
	}
}

func TestDecodeExpiredContextKeepsDeadlineCause(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := decodeASRAudio(ctx, Pipeline{}, "unused", "unused"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("decode timeout flattened: %v", err)
	}
}

func TestDispatchContextOutcomesAndNoPostCancelResult(t *testing.T) {
	for _, mode := range []string{"before-dispatch", "cancel-after-success", "wrapped-cancel", "wrapped-deadline", "typed-timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			pipeline, video, _ := contextPipeline(t, "success", func(context.Context, string, any, int) (json.RawMessage, error) {
				calls++
				switch mode {
				case "cancel-after-success":
					cancel()
					return json.RawMessage(`{"duration":2,"cues":[{"from":0,"to":1,"content":"Hello"}]}`), nil
				case "wrapped-cancel":
					return nil, fmt.Errorf("native: %w", context.Canceled)
				case "wrapped-deadline":
					return nil, fmt.Errorf("native: %w", context.DeadlineExceeded)
				case "typed-timeout":
					return nil, contract.NewError(contract.CodeTimeout, "timeout")
				}
				t.Fatal("cancelled before dispatch called native")
				return nil, nil
			})
			if mode == "before-dispatch" {
				pipeline.Progress = func(message string) {
					if strings.Contains(message, "正在识别语音范围") {
						cancel()
					}
				}
			}
			result, err := pipeline.Transcribe(ctx, video, "en")
			if err == nil || result.SRT != "" {
				t.Fatalf("cancel/timeout published result %+v %v", result, err)
			}
			if mode == "typed-timeout" {
				var app *contract.AppError
				if !errors.As(err, &app) || app.Code != contract.CodeTimeout {
					t.Fatal(err)
				}
			} else {
				expected := error(context.Canceled)
				if mode == "wrapped-deadline" {
					expected = context.DeadlineExceeded
				}
				if !errors.Is(err, expected) {
					t.Fatalf("context cause lost %v", err)
				}
			}
			expectedCalls := 1
			if mode == "before-dispatch" {
				expectedCalls = 0
			}
			if calls != expectedCalls {
				t.Fatal("wrong dispatch count")
			}
			files, _ := filepath.Glob(filepath.Join(pipeline.DataRoot, "asr-tasks", "*", "result.srt"))
			if len(files) != 0 {
				t.Fatal("cancelled pipeline persisted result")
			}
		})
	}
}
