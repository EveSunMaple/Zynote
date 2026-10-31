package visual

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Execute the test binary as a deterministic media subprocess. This tests the
// real process/stream/error boundary, not FFmpeg decoding quality.
func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	mode := os.Getenv("ZYNOTE_VISUAL_TEST_PROCESS")
	if mode != "" && (name == "ffmpeg" || name == "ffprobe") {
		if name == "ffprobe" && mode != "probe-wait" {
			fmt.Print(`{"format":{"duration":"20"},"streams":[{"codec_type":"video"}]}`)
			os.Exit(0)
		}
		if strings.HasSuffix(mode, "wait") {
			_ = os.WriteFile(os.Getenv("ZYNOTE_VISUAL_TEST_STARTED"), []byte("started"), 0600)
			for {
				time.Sleep(time.Second)
			}
		}
		switch mode {
		case "producer":
			if strings.Contains(strings.Join(os.Args, " "), "rawvideo") {
				for _, value := range []byte{40, 40, 80, 80} {
					pixels := make([]byte, 576)
					for index := range pixels {
						pixels[index] = value
					}
					_, _ = os.Stdout.Write(pixels)
				}
			} else {
				_ = os.WriteFile(os.Args[len(os.Args)-1], []byte("controlled frame bytes"), 0600)
			}
			os.Exit(0)
		case "scan-fail":
			_, _ = os.Stdout.Write(make([]byte, 576))
			os.Exit(7)
		case "scan-partial":
			_, _ = os.Stdout.Write(make([]byte, 575))
			os.Exit(0)
		case "scan-empty":
			os.Exit(0)
		case "extract-fail":
			os.Exit(9)
		}
		os.Exit(11)
	}
	os.Exit(m.Run())
}

func controlledMediaService(t *testing.T, mode string) (*Service, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resources := filepath.Join(root, "resources")
	if err := os.MkdirAll(filepath.Join(resources, "video"), 0755); err != nil {
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
	suffix := ""
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
		suffix = ".exe"
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(resources, "video", tool+suffix), body, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ZYNOTE_VISUAL_TEST_PROCESS", mode)
	started := filepath.Join(root, "started")
	t.Setenv("ZYNOTE_VISUAL_TEST_STARTED", started)
	return New(filepath.Join(root, "outputs"), filepath.Join(root, "frames"), resources, platform, false), started
}

func TestCaptureRejectsFailedOrIncompleteBalancedScan(t *testing.T) {
	for _, mode := range []string{"scan-fail", "scan-partial", "scan-empty"} {
		t.Run(mode, func(t *testing.T) {
			service, _ := controlledMediaService(t, mode)
			source := filepath.Join(t.TempDir(), "source.mp4")
			if err := os.WriteFile(source, []byte("controlled source"), 0600); err != nil {
				t.Fatal(err)
			}
			hash, _ := fileDigest(source)
			id := strings.Repeat("a", 32)
			capture, err := service.Capture(context.Background(), CaptureRequest{ID: id, Path: source, ExpectedHash: hash, Cap: 4, Policy: "balanced"})
			if err == nil || !strings.Contains(err.Error(), "扫描") || len(capture.Frames) != 0 {
				t.Fatalf("scan failure returned success: %+v %v", capture, err)
			}
			if _, err := os.Stat(filepath.Join(service.FramesRoot, "native-visual", id, "source.json")); !os.IsNotExist(err) {
				t.Fatal("failed scan published source manifest")
			}
			if files, _ := filepath.Glob(filepath.Join(service.Root, ".candidates", "*.json")); len(files) != 0 {
				t.Fatal("failed scan published selection evidence")
			}
		})
	}
}

func TestMediaStagesPreserveCancellationAndDeadline(t *testing.T) {
	for _, stage := range []string{"probe", "scan", "image", "frame"} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-timeout-%t", stage, timeout), func(t *testing.T) {
				mode := "extract-wait"
				if stage == "probe" {
					mode = "probe-wait"
				}
				service, started := controlledMediaService(t, mode)
				var ctx context.Context
				var cancel context.CancelFunc
				expected := error(context.Canceled)
				if timeout {
					ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
					expected = context.DeadlineExceeded
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				result := make(chan error, 1)
				go func() {
					var err error
					switch stage {
					case "probe":
						_, err = service.probeVideoDuration(ctx, "source")
					case "scan":
						_, err = service.balancedTimes(ctx, "source", 20, []float64{5, 15})
					case "image":
						_, err = service.captureImageFrame(ctx, "source", t.TempDir())
					case "frame":
						_, err = service.captureVideoFrames(ctx, "source", t.TempDir(), []float64{5})
					}
					result <- err
				}()
				if !timeout {
					deadline := time.Now().Add(5 * time.Second)
					for {
						if _, err := os.Stat(started); err == nil {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("media process did not start")
						}
						time.Sleep(time.Millisecond)
					}
					cancel()
				}
				select {
				case err := <-result:
					if !errors.Is(err, expected) {
						t.Fatalf("lost context outcome: %v", err)
					}
				case <-time.After(8 * time.Second):
					t.Fatal("media cancellation stalled")
				}
			})
		}
	}
}
