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

func TestVideoPptStagesShareQueueIdentity(t *testing.T) {
	inv := NewInvoker()
	state := &contract.State{LibraryRoot: t.TempDir()}
	task, err := videoPptService(state).CreateBlankTask("fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, stage := range []string{"source", "extract", "export"} {
		_, err := inv.runVideoPptQueued(state, task, stage, map[string]string{"stage": stage}, func(context.Context) (any, error) { calls++; return stage, nil })
		if err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
	}
	if calls != 3 {
		t.Fatalf("stages lost: %d", calls)
	}
}

func TestVideoPptQueuedCancellationPreservesIntent(t *testing.T) {
	for _, stage := range []string{"source", "auto"} {
		t.Run(stage, func(t *testing.T) {
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
			params := json.RawMessage(`{"url":"https://www.bilibili.com/video/BV1xx411c7mD/","prompt":"fixture"}`)
			go func() {
				var err error
				if stage == "source" {
					_, err = inv.handleVideoPptSource(nil, state, params)
				} else {
					_, err = inv.handleVideoPptAuto(nil, state, params)
				}
				done <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			var id string
			for {
				select {
				case err := <-done:
					t.Fatalf("rejected before queue: %v", err)
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
					t.Fatal("not queued")
				}
				time.Sleep(time.Millisecond)
			}
			inv.jobs.CancelAll()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancel accepted")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancel stuck")
			}
			directory := filepath.Join(root, "VideoTasks", id)
			paths, err := filepath.Glob(filepath.Join(directory, "input-"+stage+"-*.json"))
			if err != nil || len(paths) != 1 {
				t.Fatal("intent lost", paths, err)
			}
			body, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if json.Unmarshal(body, &input) != nil || input["url"] != "https://www.bilibili.com/video/BV1xx411c7mD/" {
				t.Fatalf("source missing: %s", body)
			}
			if _, err := os.Stat(filepath.Join(directory, "video.mp4")); !os.IsNotExist(err) {
				t.Fatal("queued download ran", err)
			}
		})
	}
}
