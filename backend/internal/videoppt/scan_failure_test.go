package videoppt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	mode := os.Getenv("ZYNOTE_PPT_TEST_SCAN")
	if name == "ffmpeg" && mode != "" {
		if mode == "wait" {
			_ = os.WriteFile(os.Getenv("ZYNOTE_PPT_TEST_STARTED"), []byte("started"), 0600)
			for {
				time.Sleep(time.Second)
			}
		}
		frame := make([]byte, 14400)
		for index := range frame {
			frame[index] = 40
		}
		_, _ = os.Stdout.Write(frame)
		_, _ = os.Stdout.Write(frame) // A valid stable candidate already exists.
		switch mode {
		case "failed":
			os.Exit(7)
		case "partial":
			_, _ = os.Stdout.Write([]byte{1})
			os.Exit(0)
		case "success":
			os.Exit(0)
		}
		os.Exit(11)
	}
	os.Exit(m.Run())
}

func controlledScanProcessor(t *testing.T, mode string) (Processor, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "video"), 0755); err != nil {
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
	if err := os.WriteFile(filepath.Join(root, "video", "ffmpeg"+suffix), body, 0700); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(root, "started")
	t.Setenv("ZYNOTE_PPT_TEST_SCAN", mode)
	t.Setenv("ZYNOTE_PPT_TEST_STARTED", started)
	return Processor{Resources: root, Platform: platform}, started
}

func TestPPTScanDoesNotAcceptCandidateAfterFailedOrPartialStream(t *testing.T) {
	for _, mode := range []string{"failed", "partial", "success"} {
		t.Run(mode, func(t *testing.T) {
			processor, _ := controlledScanProcessor(t, mode)
			detector, err := scanStableSlides(context.Background(), processor, CreatedTask{Path: "controlled"}, Source{Duration: 2}, 0, 0, 160, 90, nil)
			if mode == "success" {
				if err != nil || len(detector.Times) != 1 || detector.Times[0] != 0 {
					t.Fatalf("valid scan changed %+v %v", detector, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "扫描") || detector != nil {
				t.Fatalf("failed stream accepted %+v %v", detector, err)
			}
		})
	}
}

func TestPPTScanPreservesCancellationAndDeadline(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[timeout], func(t *testing.T) {
			processor, started := controlledScanProcessor(t, "wait")
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
				_, err := scanStableSlides(ctx, processor, CreatedTask{Path: "controlled"}, Source{Duration: 2}, 0, 0, 160, 90, nil)
				result <- err
			}()
			if !timeout {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(started); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("scan process did not start")
					}
					time.Sleep(time.Millisecond)
				}
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, expected) {
					t.Fatalf("context outcome lost %v", err)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("scan cancellation stalled")
			}
		})
	}
}
