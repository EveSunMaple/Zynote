package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const stationFixtureSRT = "1\n00:00:00,000 --> 00:00:01,000\nknown station subtitle\n"

func TestStationSubtitleRecoversPublishedSRTWithoutVideoOrFetching(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	task, _, err := inv.createQueuedVideoTask(state, "", "https://www.bilibili.com/video/BV1234567890/?p=2", "subtitle-station", "", "", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	service := videoPptService(state)
	if err := service.SaveStationSubtitle(task, videoppt.StationSubtitle{State: "prepared", Title: "second", Source: "中文", SRT: stationFixtureSRT, Count: 1}); err != nil {
		t.Fatal(err)
	}
	meta, _, err := service.SubtitleTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := library.CourseDirectory(root, "Transcripts", "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := videoppt.PublishSubtitle(context.Background(), task, directory, meta.OutputName, []byte(stationFixtureSRT))
	if err != nil {
		t.Fatal(err)
	}
	// Crash boundary: published output, no Saved field or completion receipt yet.
	inv = NewInvoker()
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	result, err := inv.handleVideoResumeSubtitle(nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["savedPath"] != path {
		t.Fatal(result)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("duplicate output", entries, err)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "video-ready.json")); !os.IsNotExist(err) {
		t.Fatal("station subtitle invented video proof", err)
	}
	_, _, completedErr := inv.createQueuedVideoTask(state, "", "https://www.bilibili.com/video/BV1234567890/?p=2", "subtitle-station", "", "", task.ID)
	reused, err := inv.runBoundSubtitle(nil, state, task, completedErr)
	if err != nil || reused.(map[string]any)["savedPath"] != path {
		t.Fatal(reused, err)
	}
}

func TestSubtitleBatchRestoresParentAndPreparedChildrenWithoutNetwork(t *testing.T) {
	inv := NewInvoker()
	state, pptBatch := batchFixture(t, inv)
	input := pptBatch.Input
	input.Kind = "subtitle"
	input.Prompt = "station-first-then-asr"
	batch, err := videoPptService(state).BindBatch("cccccccc-cccc-4ccc-8ccc-cccccccccccc", input)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = videoPptService(state).SetBatchEpisodes(batch.ID, pptBatch.Episodes)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ChildID(1) == videoppt.BatchChildID(batch.ID, 1) {
		t.Fatal("child namespaces collide")
	}
	for _, episode := range batch.Episodes {
		task, _, err := inv.createQueuedVideoTask(state, "", episode.URL, "subtitle-station", "", batch.Input.CourseName, batch.ChildID(episode.Page))
		if err != nil {
			t.Fatal(err)
		}
		if err := videoPptService(state).SaveStationSubtitle(task, videoppt.StationSubtitle{State: "prepared", Title: episode.Title, Source: "中文", SRT: stationFixtureSRT, Count: 1}); err != nil {
			t.Fatal(err)
		}
	}
	inv = NewInvoker()
	restoreParams, _ := json.Marshal(map[string]string{"id": batch.ID})
	restored, err := inv.handleVideoPptRestore(state, restoreParams)
	if err != nil || restored.(map[string]any)["resume"].(map[string]any)["action"] != "video-ppt:batch-subtitles" {
		t.Fatal(restored, err)
	}
	params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
	if _, err := inv.handleVideoBatch(nil, state, params); err == nil {
		t.Fatal("same parent accepted across batch kind")
	}
	result, err := inv.handleVideoBatchSubtitles(nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.([]map[string]any) {
		if item["status"] != "已完成" || item["path"] == "" {
			t.Fatal(item)
		}
	}
	result, err = NewInvoker().handleVideoBatchSubtitles(nil, state, params)
	if err != nil {
		t.Fatal("completed child replay", result, err)
	}
}

func TestSubtitleBatchCancellationStopsUnstartedNextChild(t *testing.T) {
	inv := NewInvoker()
	state, pptBatch := batchFixture(t, inv)
	input := pptBatch.Input
	input.Kind = "subtitle"
	input.Prompt = "station-first-then-asr"
	batch, err := videoPptService(state).BindBatch("cccccccc-cccc-4ccc-8ccc-cccccccccccc", input)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = videoPptService(state).SetBatchEpisodes(batch.ID, pptBatch.Episodes)
	if err != nil {
		t.Fatal(err)
	}
	episode := batch.Episodes[0]
	task, _, err := inv.createQueuedVideoTask(state, "", episode.URL, "subtitle-station", "", "", batch.ChildID(episode.Page))
	if err != nil {
		t.Fatal(err)
	}
	if err := videoPptService(state).SaveStationSubtitle(task, videoppt.StationSubtitle{State: "prepared", SRT: stationFixtureSRT, Count: 1}); err != nil {
		t.Fatal(err)
	}
	inv.notifier = batchNotifier(func(value any) {
		event := value.(map[string]any)
		if event["kind"] == "video-batch" && event["items"].([]map[string]any)[0]["status"] == "已完成" {
			inv.cancelVideoBatches()
		}
	})
	params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
	if _, err := inv.handleVideoBatchSubtitles(nil, state, params); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := os.Stat(filepath.Join(videoPptService(state).Root, batch.ChildID(2))); !os.IsNotExist(err) {
		t.Fatal("unstarted subtitle child was created", err)
	}
}

func TestSubtitleBatchAuthOrRateFailureDoesNotCreateLaterChildren(t *testing.T) {
	for _, code := range []string{contract.CodeAuthExpired, contract.CodeRateLimited} {
		t.Run(code, func(t *testing.T) {
			inv := NewInvoker()
			state, pptBatch := batchFixture(t, inv)
			input := pptBatch.Input
			input.Kind = "subtitle"
			input.Prompt = "station-first-then-asr"
			batch, err := videoPptService(state).BindBatch("cccccccc-cccc-4ccc-8ccc-cccccccccccc", input)
			if err != nil {
				t.Fatal(err)
			}
			batch, err = videoPptService(state).SetBatchEpisodes(batch.ID, pptBatch.Episodes)
			if err != nil {
				t.Fatal(err)
			}
			inv.biliRoot = filepath.Join(state.LibraryRoot, "MediaDownloads")
			calls := 0
			inv.bili = bilibili.NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) {
				calls++
				status := http.StatusOK
				body := `{"code":-101}`
				if code == contract.CodeRateLimited {
					status = http.StatusTooManyRequests
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
			_, err = inv.handleVideoBatchSubtitles(&subtitleStore{state: state}, state, params)
			if application, ok := err.(*contract.AppError); !ok || application.Code != code {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("later page requested", calls)
			}
			if _, err := os.Stat(filepath.Join(videoPptService(state).Root, batch.ChildID(2))); !os.IsNotExist(err) {
				t.Fatal("later page created", err)
			}
		})
	}
}
