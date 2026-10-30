package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeTool(t *testing.T, resources, name, script string) string {
	t.Helper()
	skipWithoutPosixShell(t)
	directory := filepath.Join(resources, "video")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func resolvedTemp(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMediaExecutableResolution(t *testing.T) {
	resources := resolvedTemp(t)
	script := "#!/bin/sh\nexit 0\n"
	path := fakeTool(t, resources, "ffprobe", script)
	resolved, err := CheckedMediaExecutable(resources, "ffprobe", "darwin", false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("resolved path mismatch: %s", resolved)
	}
	if path, err := MediaExecutablePath(resources, "ffmpeg", "win32"); err != nil || !strings.HasSuffix(path, "ffmpeg.exe") {
		t.Fatalf("win32 path mismatch: %v %v", path, err)
	}
	if _, err := CheckedMediaExecutable(resources, "ffmpeg", "darwin", false); err == nil || err.Error() != "未找到媒体处理组件" {
		t.Fatalf("missing tool error mismatch: %v", err)
	}
	if _, err := MediaExecutablePath("relative/path", "ffmpeg", "darwin"); err == nil {
		t.Fatal("relative resources should fail")
	}
	if _, err := MediaExecutablePath(resources, "bash", "darwin"); err == nil {
		t.Fatal("unknown tool should fail")
	}

	link := filepath.Join(resources, "video", "ffmpeg")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckedMediaExecutable(resources, "ffmpeg", "darwin", false); err == nil ||
		!strings.Contains(err.Error(), "必须是普通文件") {
		t.Fatalf("symlink tool should fail: %v", err)
	}
}

func TestMediaExecutableOutsideResources(t *testing.T) {
	skipWithoutPosixShell(t)
	root := resolvedTemp(t)
	resources := filepath.Join(root, "resources")
	if err := os.MkdirAll(filepath.Join(resources, "video"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(outside, "ffprobe")
	if err := os.WriteFile(external, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(resources, "video", "ffprobe")); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckedMediaExecutable(resources, "ffprobe", "darwin", false); err == nil {
		t.Fatal("outside symlink should be rejected")
	}
}

func TestMediaCommandOutput(t *testing.T) {
	resources := resolvedTemp(t)
	fakeTool(t, resources, "ffprobe", "#!/bin/sh\necho '{\"format\":{\"duration\":\"12.5\"}}'\n")
	output, err := MediaCommand(MediaCommandInput{
		Resources: resources,
		Tool:      "ffprobe",
		Args:      []string{"-v", "error"},
		Ctx:       context.Background(),
		Platform:  "darwin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"duration":"12.5"`) {
		t.Fatalf("output mismatch: %q", output)
	}
}

func TestMediaCommandFailureAndCancel(t *testing.T) {
	resources := resolvedTemp(t)
	fakeTool(t, resources, "ffprobe", "#!/bin/sh\nexit 3\n")
	if _, err := MediaCommand(MediaCommandInput{
		Resources: resources, Tool: "ffprobe", Ctx: context.Background(), Platform: "darwin",
	}); err == nil || err.Error() != "媒体处理失败，未发布成果" {
		t.Fatalf("exit failure mismatch: %v", err)
	}

	fakeTool(t, resources, "ffprobe", "#!/bin/sh\nsleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := MediaCommand(MediaCommandInput{
		Resources: resources, Tool: "ffprobe", Ctx: ctx, Platform: "darwin",
	})
	if err == nil || !strings.Contains(err.Error(), "媒体处理已取消") {
		t.Fatalf("cancel mismatch: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("cancel took too long: %v", time.Since(started))
	}
}

func TestProbeParsing(t *testing.T) {
	resources := resolvedTemp(t)
	payload := `{"format":{"duration":"12.5"},"streams":[{"codec_type":"video"},{"codec_type":"audio"},{"codec_type":"subtitle"},{"codec_type":"video","disposition":{"attached_pic":1}},{"codec_type":"attachment","tags":{"filename":"cover.jpg"}}],"chapters":[{},{}]}`
	fakeTool(t, resources, "ffprobe", "#!/bin/sh\ncat <<'EOF'\n"+payload+"\nEOF\n")
	probe, err := (&NativeMediaMux{Resources: resources, Platform: "darwin"}).Probe(context.Background(), fakeInputFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if probe.Duration != 12.5 || probe.Video != 1 || probe.Audio != 1 || probe.Subtitles != 1 ||
		probe.Covers != 2 || probe.Chapters != 2 {
		t.Fatalf("probe mismatch: %+v", probe)
	}
}

func fakeInputFile(t *testing.T) string {
	t.Helper()
	root := resolvedTemp(t)
	path := filepath.Join(root, "input.mp4")
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMediaServiceMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MediaDownloads")
	service := NewMediaService(root, nil)
	if _, err := service.List(); err != nil {
		t.Fatalf("list with missing root: %v", err)
	}
}

func TestMediaServiceActions(t *testing.T) {
	root := resolvedTemp(t)
	service := NewMediaService(root, nil)
	options, err := service.Options()
	if err != nil {
		t.Fatal(err)
	}
	if options["defaults"] == nil || options["heights"] == nil || options["enums"] == nil {
		t.Fatalf("options missing fields: %+v", options)
	}

	job, err := service.Create("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://example.com/v", "标题", map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := job["id"].(string)
	list, err := service.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list mismatch: %v %v", list, err)
	}
	if list[0]["output"] != "" || len(list[0]["files"].([]string)) != 0 {
		t.Fatalf("projection mismatch: %+v", list[0])
	}

	created, _ := service.Jobs.Read(id)
	if _, err := service.Jobs.Transition(id, created.Revision, "running", "下载中", "", "", nil); err != nil {
		t.Fatal(err)
	}
	running, _ := service.Jobs.Read(id)
	if _, err := service.Jobs.Transition(id, running.Revision, "error", "", "失败", "", nil); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Retry(id)
	if err != nil || retried["state"] != "queued" || retried["stage"] != "等待继续下载" {
		t.Fatalf("retry mismatch: %+v %v", retried, err)
	}
	cancelled, err := service.Cancel(id)
	if err != nil || cancelled["state"] != "cancelled" || cancelled["stage"] != "已取消" {
		t.Fatalf("cancel mismatch: %+v %v", cancelled, err)
	}

	if _, err := service.Retry("ffffffffffffffffffffffffffffffff"); err == nil || err.Error() != "下载任务不存在" {
		t.Fatalf("missing job retry mismatch: %v", err)
	}
	if _, err := service.Retry("not-a-job-id"); err == nil {
		t.Fatalf("invalid id should fail")
	}
}
