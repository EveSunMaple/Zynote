package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
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
	holdQueueWorker(t, inv)
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	done := make(chan error, 1)
	go func() { _, err := inv.handleVideoResumeSubtitle(nil, state, params); done <- err }()
	row := waitVideoQueued(t, inv, state, done)
	if row.ID != task.ID {
		t.Fatal("wrong queued task")
	}
	cancelQueuedCall(t, inv, done)
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
		if _, err := inv.handleVideoResumeSubtitle(nil, &contract.State{LibraryRoot: t.TempDir()}, params); err == nil {
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
	holdQueueWorker(t, inv)
	done := make(chan error, 1)
	// A nil store must never be reached while this request waits in the queue.
	go func() {
		_, err := inv.handleVideoSubtitleTranscribe(nil, state, json.RawMessage(`{"url":"https://www.bilibili.com/video/BV1xx411c7mD/?p=2"}`))
		done <- err
	}()
	id := waitVideoQueued(t, inv, state, done).ID
	cancelQueuedCall(t, inv, done)
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
