package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

type subtitlePicker struct {
	path  *string
	calls int
}

func (p *subtitlePicker) Call(method string, _ any, _ int) (json.RawMessage, error) {
	p.calls++
	return json.Marshal(map[string]any{"path": p.path})
}

type subtitleStore struct {
	state  *contract.State
	picker *subtitlePicker
}

func (s *subtitleStore) State() *contract.State          { return s.state }
func (s *subtitleStore) SetState(state *contract.State)  { s.state = state }
func (s *subtitleStore) Reverse() contract.ReverseCaller { return s.picker }

func submitSubtitle(inv *Invoker, store contract.StateStore, action string, params json.RawMessage) <-chan error {
	done := make(chan error, 1)
	go func() { _, err := inv.Invoke(store, action, params); done <- err }()
	return done
}

func TestLocalSubtitleRetryDoesNotReopenPicker(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lecture.mp4")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &subtitleStore{state: &contract.State{LibraryRoot: root}, picker: &subtitlePicker{path: &path}}
	inv := NewInvoker()
	holdQueueWorker(t, inv)
	params := json.RawMessage(`{"operationId":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}`)
	for attempt := 0; attempt < 2; attempt++ {
		done := submitSubtitle(inv, store, "video-ppt:subtitle-transcribe-local", params)
		row := waitVideoQueued(t, inv, store.state, done)
		if row.ID != "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" {
			t.Fatal("identity changed")
		}
		cancelQueuedCall(t, inv, done)
	}
	if store.picker.calls != 1 {
		t.Fatalf("retry reopened picker: %d", store.picker.calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("source lost", err)
	}
}

func TestLocalPickerCancellationDoesNotBindOperation(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	picker := &subtitlePicker{}
	store := &subtitleStore{state: state, picker: picker}
	inv := NewInvoker()
	result, err := inv.Invoke(store, "video-ppt:subtitle-transcribe-local", json.RawMessage(`{"operationId":"ffffffff-ffff-4fff-8fff-ffffffffffff"}`))
	if err != nil || result != nil {
		t.Fatal("picker cancellation failed", result, err)
	}
	if _, err := os.Stat(filepath.Join(state.LibraryRoot, "VideoTasks", "ffffffff-ffff-4fff-8fff-ffffffffffff")); !os.IsNotExist(err) {
		t.Fatal("cancelled picker bound task", err)
	}
}

func TestPreparedSubtitlePublicationAndCompletionNeverRerunASR(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	inv := NewInvoker()
	id := "99999999-9999-4999-8999-999999999999"
	url := "https://www.bilibili.com/video/BV1xx411c7mD/"
	task, _, err := inv.createQueuedVideoTask(state, "", url, "subtitle", "", "", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.Path, []byte("video fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := videoppt.MarkVideoReady(task); err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:00:00,000 --> 00:00:02,000\nSaved words\n"
	if err := videoppt.SavePreparedSubtitle(task, srt, 1); err != nil {
		t.Fatal(err)
	}
	meta, _, err := videoPptService(state).SubtitleTask(id)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := library.CourseDirectory(root, "Transcripts", "")
	if err != nil {
		t.Fatal(err)
	}
	published, err := videoppt.PublishSubtitle(context.Background(), task, directory, meta.OutputName, []byte(srt))
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct after the published-before-saved-task checkpoint window.
	fresh := NewInvoker()
	result, err := fresh.transcribeVideoTask(context.Background(), state, task)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["savedPath"] != published {
		t.Fatal("duplicate subtitle path")
	}
	params, _ := json.Marshal(map[string]string{"operationId": id, "url": url})
	result, err = NewInvoker().Invoke(&subtitleStore{state: state}, "video-ppt:subtitle-transcribe", params)
	if err != nil || result.(map[string]any)["srt"] != srt {
		t.Fatal("completion not reused", result, err)
	}
	files, _ := filepath.Glob(filepath.Join(directory, "*.srt"))
	if len(files) != 1 {
		t.Fatal("duplicate subtitle export", files)
	}
}
