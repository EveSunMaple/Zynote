package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestVideoPptStagesShareQueueIdentity(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
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
	value, err := inv.handleVideoPptPending(state)
	if err != nil {
		t.Fatal(err)
	}
	rows := value.([]videoppt.Pending)
	if len(rows) != 1 || rows[0].QueueState != "review" {
		t.Fatalf("stage completion mislabeled workflow done: %+v", rows)
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
			holdQueueWorker(t, inv)
			done := make(chan error, 1)
			params := json.RawMessage(`{"url":"https://www.bilibili.com/video/BV1xx411c7mD/","prompt":"fixture","operationId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`)
			go func() {
				var err error
				if stage == "source" {
					_, err = inv.handleVideoPptSource(nil, state, params)
				} else {
					_, err = inv.handleVideoPptAuto(nil, state, params)
				}
				done <- err
			}()
			id := waitVideoQueued(t, inv, state, done).ID
			cancelQueuedCall(t, inv, done)
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
			restored, err := inv.handleVideoPptRestore(state, json.RawMessage(`{"id":"`+id+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			plan := restored.(map[string]any)["resume"].(map[string]any)
			if plan["action"] != "video-ppt:"+stage || plan["operationId"] != id {
				t.Fatalf("bad resume plan: %+v", plan)
			}
			resume := json.RawMessage(`{"operationId":"` + id + `"}`)
			go func() {
				var err error
				if stage == "source" {
					_, err = inv.handleVideoPptSource(nil, state, resume)
				} else {
					_, err = inv.handleVideoPptAuto(nil, state, resume)
				}
				done <- err
			}()
			if row := waitVideoQueued(t, inv, state, done); row.ID != id {
				t.Fatal("resume allocated another task")
			}
			cancelQueuedCall(t, inv, done)
		})
	}
}
