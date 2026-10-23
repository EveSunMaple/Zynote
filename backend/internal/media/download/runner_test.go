package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type stubCatalog struct {
	source bilibili.Source
	err    error
}

func (stub stubCatalog) Read(string, bool, any) (any, error) { return stub.source, stub.err }

func resolvedTemp(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func fakeTool(t *testing.T, resources, name, script string) {
	t.Helper()
	directory := filepath.Join(resources, "video")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func fakeMediaTools(t *testing.T) string {
	t.Helper()
	resources := resolvedTemp(t)
	fakeTool(t, resources, "ffmpeg", "#!/bin/sh\nout=\"\"\nfor a in \"$@\"; do out=\"$a\"; done\nprintf 'fake-media-bytes' > \"$out\"\n")
	fakeTool(t, resources, "ffprobe", `#!/bin/sh
cat <<'EOF'
{"format":{"duration":"10.0"},"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"chapters":[]}
EOF
`)
	return resources
}

func runnerFixture(t *testing.T, payload string) (*Runner, *media.DownloadJobs, string) {
	t.Helper()
	resources := fakeMediaTools(t)
	root := resolvedTemp(t)
	jobs := media.NewDownloadJobs(root, nil)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"media-v1"`)
		writer.Header().Set("Accept-Ranges", "bytes")
		_, _ = writer.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	duration := 10.0
	source := bilibili.Source{
		Identity: "BV1xx411c7mD:111:1",
		Catalog:  bilibili.Catalog{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "标题", Duration: &duration},
		Streams: []bilibili.MediaStream{{
			ID: "c80", Kind: "combined", Extension: "flv",
			URLs: []string{server.URL + "/media.flv"},
		}},
		SourceID: "BV1xx411c7mD-P1",
		Platform: "bilibili",
	}
	runner := &Runner{
		Jobs:    jobs,
		Mux:     &media.NativeMediaMux{Resources: resources, Platform: "darwin"},
		Catalog: stubCatalog{source: source},
	}
	return runner, jobs, source.Identity
}

func createJob(t *testing.T, jobs *media.DownloadJobs, identity string, recipe map[string]any) *media.NativeDownloadJob {
	t.Helper()
	job, err := jobs.Create(identity, "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", "标题", recipe, nil)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestRunnerPublishesVerifiedOutput(t *testing.T) {
	runner, jobs, identity := runnerFixture(t, "media-bytes")
	job := createJob(t, jobs, identity, map[string]any{"retries": 0})
	result, err := runner.Run(job.ID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row, err := jobs.Read(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "completed" || len(row.Outputs) != 1 || row.Outputs[0].Relative != "result.mkv" {
		t.Fatalf("completed job mismatch: %+v", row)
	}
	directory, err := jobs.TaskDirectory(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(filepath.Join(directory, row.Outputs[0].Relative))
	if err != nil || string(bytes) != "fake-media-bytes" {
		t.Fatalf("output mismatch: %q %v", string(bytes), err)
	}
	if pending, _ := result["cleanupPending"].([]string); len(pending) != 0 {
		t.Fatalf("cleanup pending mismatch: %+v", pending)
	}
	if _, err := os.Stat(filepath.Join(directory, "track-0.partial")); !os.IsNotExist(err) {
		t.Fatalf("intermediate not cleaned: %v", err)
	}
	output, ok := result["output"].(map[string]any)
	if !ok || output["duration"] != 10.0 || output["video"] != 1 || output["audio"] != 1 {
		t.Fatalf("output projection mismatch: %+v", result["output"])
	}
}

func TestRunnerRejectsUnsupportedOptions(t *testing.T) {
	runner, jobs, identity := runnerFixture(t, "media-bytes")
	job := createJob(t, jobs, identity, map[string]any{"subtitles": true})
	if _, err := runner.Run(job.ID, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "字幕") {
		t.Fatalf("subtitle rejection mismatch: %v", err)
	}
	row, _ := jobs.Read(job.ID)
	if row.State != "error" || !strings.Contains(row.ErrorMessage, "字幕") {
		t.Fatalf("error state mismatch: %+v", row)
	}
}

func TestRunnerRejectsSplitWithoutChapters(t *testing.T) {
	runner, jobs, identity := runnerFixture(t, "media-bytes")
	job := createJob(t, jobs, identity, map[string]any{"split_chapters": true})
	if _, err := runner.Run(job.ID, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "站点章节") {
		t.Fatalf("chapter rejection mismatch: %v", err)
	}
}

func TestRunnerRejectsChangedIdentity(t *testing.T) {
	runner, jobs, identity := runnerFixture(t, "media-bytes")
	job := createJob(t, jobs, identity, map[string]any{})
	runner.Catalog = stubCatalog{source: bilibili.Source{
		Identity: "BV1xx411c7mD:222:2",
		Catalog:  bilibili.Catalog{URL: job.SourceURL},
	}}
	if _, err := runner.Run(job.ID, context.Background()); err == nil ||
		!strings.Contains(err.Error(), "分集身份已改变") {
		t.Fatalf("identity mismatch: %v", err)
	}
	row, _ := jobs.Read(job.ID)
	if row.State != "error" {
		t.Fatalf("error state mismatch: %+v", row)
	}
}

func TestRunnerSubtitleOnlyPublishesSubtitle(t *testing.T) {
	runner, jobs, identity := runnerFixture(t, "media-bytes")
	subtitleServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"body":[{"from":0.5,"to":1.25,"content":"你好"}]}`))
	}))
	defer subtitleServer.Close()
	duration := 10.0
	runner.Catalog = stubCatalog{source: bilibili.Source{
		Identity: identity,
		Catalog:  bilibili.Catalog{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "标题", Duration: &duration},
		SubtitleTracks: []bilibili.SubtitleTrack{{
			Language: "zh-Hans", URL: subtitleServer.URL + "/subtitle.json",
		}},
		Platform: "bilibili",
	}}
	job := createJob(t, jobs, identity, map[string]any{"subtitle_only": true, "retries": 0})
	result, err := runner.Run(job.ID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row, err := jobs.Read(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "completed" || len(row.Outputs) != 1 || row.Outputs[0].Relative != "subtitle-zh-Hans.srt" {
		t.Fatalf("subtitle job mismatch: %+v", row)
	}
	if output, ok := result["output"].(map[string]any); !ok || output["subtitles"] != 1 || output["video"] != 0 {
		t.Fatalf("subtitle output mismatch: %+v", result["output"])
	}
	directory, _ := jobs.TaskDirectory(job.ID)
	body, err := os.ReadFile(filepath.Join(directory, "subtitle-zh-Hans.srt"))
	if err != nil || !strings.Contains(string(body), "00:00:00,500 --> 00:00:01,250") {
		t.Fatalf("subtitle content mismatch: %q %v", string(body), err)
	}
}
