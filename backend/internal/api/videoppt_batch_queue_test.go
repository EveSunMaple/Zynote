package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

type batchNotifier func(any)

func (notify batchNotifier) Notify(_ string, value any) error { notify(value); return nil }

func batchFixture(t *testing.T, inv *Invoker) (*contract.State, videoppt.BatchOperation) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	batch, err := videoPptService(state).BindBatch("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", videoppt.BatchInput{URL: "https://www.bilibili.com/video/BV1234567890/?p=1", Pages: []int{1, 2}, Prompt: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err = videoPptService(state).SetBatchEpisodes(batch.ID, []videoppt.BatchEpisode{{Page: 1, Title: "first", URL: batch.Input.URL}, {Page: 2, Title: "second", URL: "https://www.bilibili.com/video/BV1234567890/?p=2"}})
	if err != nil {
		t.Fatal(err)
	}
	return state, batch
}

func completeBatchChild(t *testing.T, inv *Invoker, state *contract.State, batch videoppt.BatchOperation, index int) string {
	t.Helper()
	episode := batch.Episodes[index]
	task, _, err := inv.createQueuedVideoTask(state, "", episode.URL, "auto", batch.Input.Prompt, batch.Input.CourseName, videoppt.BatchChildID(batch.ID, episode.Page))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "output.pdf")
	if err := os.WriteFile(path, []byte("known verified fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := videoPptService(state).CompleteOperation(task, map[string]any{"path": path}, path); err != nil {
		t.Fatal(err)
	}
	if err := videoPptService(state).DiscardCompleted(task.ID); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBatchResumeReusesCompletedChildrenWithoutCatalogOrModel(t *testing.T) {
	inv := NewInvoker()
	state, batch := batchFixture(t, inv)
	first := completeBatchChild(t, inv, state, batch, 0)
	second := completeBatchChild(t, inv, state, batch, 1)
	// Simulate process loss before recording the parent completion.
	inv = NewInvoker()
	pending, err := inv.handleVideoPptPending(state)
	if err != nil {
		t.Fatal(err)
	}
	if rows := pending.([]videoppt.Pending); len(rows) != 1 || rows[0].Kind != "batch" || rows[0].QueueState != "interrupted" {
		t.Fatal(rows)
	}
	params, _ := json.Marshal(map[string]string{"id": batch.ID})
	restored, err := inv.handleVideoPptRestore(state, params)
	if err != nil {
		t.Fatal(err)
	}
	if restored.(map[string]any)["resume"].(map[string]any)["action"] != "video-ppt:batch" {
		t.Fatal(restored)
	}
	params, _ = json.Marshal(map[string]string{"operationId": batch.ID})
	result, err := inv.handleVideoBatch(nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]map[string]any)
	if len(items) != 2 || items[0]["path"] != first || items[1]["path"] != second {
		t.Fatal(items)
	}
	pending, err = inv.handleVideoPptPending(state)
	if err != nil || len(pending.([]videoppt.Pending)) != 0 {
		t.Fatal(pending, err)
	}
}

func TestBatchCancelBetweenChildrenStopsNextAndKeepsManifest(t *testing.T) {
	inv := NewInvoker()
	state, batch := batchFixture(t, inv)
	completeBatchChild(t, inv, state, batch, 0)
	inv.notifier = batchNotifier(func(value any) {
		event := value.(map[string]any)
		if event["kind"] != "video-batch" {
			return
		}
		if event["operationId"] != batch.ID {
			t.Error("missing parent event owner")
		}
		items := event["items"].([]map[string]any)
		if items[0]["status"] == "已完成" {
			_, _ = inv.handleCancel(nil, nil, nil)
		}
	})
	params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
	_, err := inv.handleVideoBatch(nil, state, params)
	if application, ok := err.(*contract.AppError); !ok || application.Code != contract.CodeCancelled {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(videoPptService(state).Root, videoppt.BatchChildID(batch.ID, 2))); !os.IsNotExist(err) {
		t.Fatal("next child created", err)
	}
	saved, err := videoPptService(state).Batch(batch.ID)
	if err != nil || saved.Completed {
		t.Fatal(saved, err)
	}
}

func TestBatchFreshProcessContinuesFrozenUnstartedChildWithoutDuplicatingFirst(t *testing.T) {
	inv := NewInvoker()
	state, batch := batchFixture(t, inv)
	first := completeBatchChild(t, inv, state, batch, 0)
	// The manifest was prepared and child one completed, then the process exited.
	for attempt := 0; attempt < 2; attempt++ {
		inv = NewInvoker()
		inv.biliRoot = filepath.Join(state.LibraryRoot, "MediaDownloads")
		inv.bili = bilibili.NewScopedResources(func(string, map[string]string, context.Context) (*http.Response, error) {
			return nil, errors.New("controlled stop before protected acquisition")
		})
		params, _ := json.Marshal(map[string]string{"operationId": batch.ID})
		value, err := inv.handleVideoBatch(&subtitleStore{state: state}, state, params)
		if err != nil {
			t.Fatal(err)
		}
		items := value.([]map[string]any)
		if items[0]["path"] != first || items[0]["status"] != "已完成" || items[1]["status"] != "失败" {
			t.Fatal(items)
		}
		operation, err := videoPptService(state).Operation(videoppt.BatchChildID(batch.ID, 2))
		if err != nil || operation.URL != batch.Episodes[1].URL {
			t.Fatal(operation, err)
		}
		persisted, err := videoPptService(state).Batch(batch.ID)
		if err != nil || persisted.Completed {
			t.Fatal(persisted, err)
		}
	}
	entries, err := os.ReadDir(videoPptService(state).Root)
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	for _, entry := range entries {
		if entry.IsDir() && entry.Name()[0] != '.' {
			children++
		}
	}
	if children != 1 {
		t.Fatal("duplicate recovery children", children)
	}
}

func TestBatchPrepareQueuesBehindExistingWorkerAndCancelsWithoutDeadlock(t *testing.T) {
	inv := NewInvoker()
	state, batch := batchFixture(t, inv)
	// Use a fresh bound manifest without episodes so catalog preparation queues.
	batch, err := videoPptService(state).BindBatch("cccccccc-cccc-4ccc-8ccc-cccccccccccc", batch.Input)
	if err != nil {
		t.Fatal(err)
	}
	holdQueueWorker(t, inv)
	ctx, release, err := inv.beginVideoBatch(state.LibraryRoot, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() { _, err := inv.prepareVideoBatch(ctx, nil, state, batch); done <- err }()
	deadline := time.After(5 * time.Second)
	for {
		record, err := inv.jobs.QueueRecord(filepath.Join(state.LibraryRoot, "TaskQueue"), batch.ID)
		if err == nil && record.State == "queued" {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("did not queue: %v", err)
		case <-deadline:
			t.Fatal("queue admission timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	inv.cancelVideoBatches()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested-worker deadlock")
	}
	if current := inv.jobs.Current().(map[string]any); current["key"] != "existing" {
		t.Fatal("batch replaced worker", current)
	}
}
