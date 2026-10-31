package asr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/helper"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

// Uses an explicitly supplied licensed recording, the real native helper and
// local model. No network, credentials, or generated speech are used here.
func TestRecordedSpeechPipeline(t *testing.T) {
	if os.Getenv("ZYNOTE_ASR_TEST") != "1" {
		t.Skip("set ZYNOTE_ASR_TEST=1 and ZYNOTE_TEST_VIDEO/MODEL/NATIVE")
	}
	video, model, native := os.Getenv("ZYNOTE_TEST_VIDEO"), os.Getenv("ZYNOTE_TEST_MODEL"), os.Getenv("ZYNOTE_TEST_NATIVE")
	for _, path := range []string{video, model, native} {
		if !filepath.IsAbs(path) {
			t.Fatal("explicit absolute fixture paths required")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	resources, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(resources, "asr", "onnx-small")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(model, "acquisition.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "acquisition.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resources, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	target, err := media.MediaExecutablePath(resources, "ffmpeg", media.CurrentMediaPlatform())
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes, 0o755); err != nil {
		t.Fatal(err)
	}
	worker := helper.NewNative("asr")
	worker.Configure(native, "", "", "", model)
	defer worker.Dispose()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := (Pipeline{Resources: resources, DataRoot: t.TempDir(), Call: worker.CallContext}).Transcribe(ctx, video, "en")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count < 2 {
		t.Fatalf("expected multiple timestamped speech cues, got %d", result.Count)
	}
	if expected := os.Getenv("ZYNOTE_TEST_EXPECT"); expected != "" && !strings.Contains(strings.ToLower(result.SRT), strings.ToLower(expected)) {
		t.Fatalf("recording's expected phrase %q absent: %s", expected, result.SRT)
	}
	if output := os.Getenv("ZYNOTE_TEST_SRT_OUTPUT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("SRT evidence path must be absolute")
		}
		if err := os.WriteFile(output, []byte(result.SRT), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("real native pipeline produced %d cues", result.Count)
}
