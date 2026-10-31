package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

// Explicit opt-in only: this test incurs provider usage. Secrets are read from
// process environment and are never included in saved task or test evidence.
func TestRealModelRecordedCourseNotes(t *testing.T) {
	if os.Getenv("ZYNOTE_MODEL_TEST") != "1" {
		t.Skip("explicit ZYNOTE_MODEL_TEST=1 and provider environment required")
	}
	key, model, base := os.Getenv("ZYNOTE_TEST_API_KEY"), os.Getenv("ZYNOTE_TEST_MODEL_ID"), os.Getenv("ZYNOTE_TEST_BASE_URL")
	if key == "" || model == "" || base == "" {
		t.Fatal("missing explicit provider configuration")
	}
	body, err := os.ReadFile(os.Getenv("ZYNOTE_TEST_SRT"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	library := filepath.Join(root, "Library")
	if err := os.MkdirAll(library, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(library, "course.srt")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	service := subtitlederived.New(filepath.Join(library, "NoteGeneration"), library)
	task, err := service.Create([]subtitlederived.Source{{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "Licensed course acceptance fixture", Path: source, Hash: hex.EncodeToString(hash[:]), Generation: "acceptance-v1"}}, []string{"brief", "detailed", "keypoints", "mindmap"}, "Acceptance")
	if err != nil {
		t.Fatal(err)
	}
	// The locator above is a valid opaque domain fixture, not a claim that this
	// MIT recording came from Bilibili. No media download uses that locator.
	client := ai.New(ai.Profile{BaseURL: base, Model: model, Key: key, APIFormat: "openai"})
	calls := 0
	deps := subtitlederived.Dependencies{Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
		calls++
		result, err := summarizeVideoText(ctx, client, srt, mode, nil)
		if err != nil {
			return "", nil, err
		}
		points := make([]subtitlederived.Point, 0, len(result.Points))
		for _, point := range result.Points {
			points = append(points, subtitlederived.Point{Cue: float64(point.Cue), Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp})
		}
		t.Logf("mode=%s verifiedPoints=%d", mode, len(points))
		return result.Markdown, points, nil
	}, RenderSVG: func(title string, points []subtitlederived.Point) string {
		converted := make([]notePoint, 0, len(points))
		for _, point := range points {
			converted = append(converted, notePoint{Cue: point.Cue, Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp})
		}
		return notesSVG(title, converted)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	value, err := jobs.NewRegistry().RunQueued(ctx, filepath.Join(library, "TaskQueue"), task.ID, "course-notes", func(ctx context.Context) (any, error) { return service.Run(ctx, task.ID, deps) })
	if err != nil {
		t.Fatal(err)
	}
	result := value.(subtitlederived.Task)
	for _, item := range result.Items {
		if item.Status != "已完成" {
			t.Fatalf("mode=%s failed: %s", item.Mode, item.Error)
		}
		if len(item.Outputs) < 2 {
			t.Fatalf("mode=%s missing outputs", item.Mode)
		}
	}
	deps.Summarize = func(context.Context, string, string) (string, []subtitlederived.Point, error) {
		t.Error("completed model call replayed")
		return "", nil, fmt.Errorf("replay forbidden")
	}
	if _, err := subtitlederived.New(service.Root, library).Run(ctx, task.ID, deps); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("unexpected provider call count: %d", calls)
	}
	if directory := os.Getenv("ZYNOTE_TEST_NOTES_OUTPUT"); directory != "" {
		if !filepath.IsAbs(directory) {
			t.Fatal("absolute evidence directory required")
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			for _, output := range item.Outputs {
				data, err := os.ReadFile(output.Path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, item.Mode+filepath.Ext(output.Path)), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		report, _ := json.MarshalIndent(map[string]any{"model": model, "modes": 4, "pipelineCalls": calls, "replayCalls": 0, "sourceSHA256": hex.EncodeToString(hash[:]), "source": "MIT OCW 6.0001 Fall 2016 Lecture 1, 60-100 seconds; CC BY-NC-SA 4.0", "locatorIsFixture": true}, "", "  ")
		if err := os.WriteFile(filepath.Join(directory, "report.json"), report, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
