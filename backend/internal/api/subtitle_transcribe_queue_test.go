package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestSubtitleResumePersistsQueueAndCancelsWithoutASR(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	source := filepath.Join(t.TempDir(), "recording.mp4")
	if err := os.WriteFile(source, []byte("opaque fixture; never decoded"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := videoPptService(state).CreateSubtitleTask(source, "recording.srt", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = inv.jobs.Run("existing", "", func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	}()
	<-entered
	defer func() { close(release); <-finished }()
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	done := make(chan error, 1)
	go func() { _, err := inv.handleVideoResumeSubtitle(state, params); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("resume rejected before queue: %v", err)
		default:
		}
		if record, err := inv.jobs.QueueRecord(filepath.Join(state.LibraryRoot, "TaskQueue"), task.ID); err == nil && record.State == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subtitle never queued")
		}
		time.Sleep(time.Millisecond)
	}
	value, err := inv.handleVideoPptPending(state)
	if err != nil {
		t.Fatal(err)
	}
	rows := value.([]videoppt.Pending)
	if len(rows) != 1 || rows[0].QueueState != "queued" {
		t.Fatalf("missing queue projection: %+v", rows)
	}
	inv.jobs.CancelAll()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled subtitle succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel stuck")
	}
	if _, _, err := videoPptService(state).SubtitleTask(task.ID); err != nil {
		t.Fatal("cancel lost recovery input", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source changed", err)
	}
}

func TestSubtitleResumeRejectsInvalidIdentityBeforeQueue(t *testing.T) {
	inv := NewInvoker()
	for _, id := range []string{"../outside", "", "not-a-uuid"} {
		params, _ := json.Marshal(map[string]string{"id": id})
		if _, err := inv.handleVideoResumeSubtitle(&contract.State{LibraryRoot: t.TempDir()}, params); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestRemoteSubtitleQueuedCancellationRetainsIntentWithoutDownload(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = inv.jobs.Run("existing", "", func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	}()
	<-entered
	defer func() { close(release); <-finished }()
	done := make(chan error, 1)
	// A nil store must never be reached while this request waits in the queue.
	go func() {
		_, err := inv.handleVideoSubtitleTranscribe(nil, state, json.RawMessage(`{"url":"https://www.bilibili.com/video/BV1xx411c7mD/?p=2"}`))
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	var id string
	for {
		select {
		case err := <-done:
			t.Fatalf("request rejected before queue: %v", err)
		default:
		}
		value, err := inv.handleVideoPptPending(state)
		if err != nil {
			t.Fatal(err)
		}
		rows := value.([]videoppt.Pending)
		if len(rows) == 1 && rows[0].QueueState == "queued" {
			id = rows[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote task never queued")
		}
		time.Sleep(time.Millisecond)
	}
	inv.jobs.CancelAll()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel stuck")
	}
	meta, directory, err := videoPptService(state).SubtitleTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=2" || meta.Page != 2 {
		t.Fatalf("lost source intent: %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(directory, "video.mp4")); !os.IsNotExist(err) {
		t.Fatalf("download ran: %v", err)
	}
}
