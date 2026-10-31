package videoppt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

// Opt-in executable integration test; it generates a controlled clip, not a
// real lecture, and exercises the production ffmpeg decode/scan path.
func TestMediaStableSlideScan(t *testing.T) {
	if os.Getenv("ZYNOTE_MEDIA_TEST") != "1" {
		t.Skip("set ZYNOTE_MEDIA_TEST=1 with ffmpeg/ffprobe installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "slides.mp4")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=white:s=320x180:d=3", "-f", "lavfi", "-i", "color=c=black:s=320x180:d=3", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-c:v", "mpeg4", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("encode: %v %s", err, output)
	}
	resources := t.TempDir()
	resources, err := filepath.EvalSymlinks(resources)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(resources, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		executable, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		target, err := media.MediaExecutablePath(resources, tool, media.CurrentMediaPlatform())
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	processor := Processor{Resources: resources}
	if _, err := processor.command(ctx, "ffprobe", []string{"-version"}, nil); err != nil {
		t.Fatalf("component check: %v", err)
	}
	duration, w, h, err := probeVideo(ctx, processor, path)
	if err != nil || duration < 5.9 || w != 320 || h != 180 {
		t.Fatalf("probe: %v %v %dx%d", err, duration, w, h)
	}
	detector, err := scanStableSlides(ctx, processor, CreatedTask{Path: path}, Source{Duration: duration}, 0, 0, w, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(detector.Times) != 2 || detector.Times[0] != 0 || detector.Times[1] != 3 {
		t.Fatalf("slide timeline: %v", detector.Times)
	}
}
