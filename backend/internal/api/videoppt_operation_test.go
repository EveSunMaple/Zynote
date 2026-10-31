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

func TestLocalVideoOperationResumesVerifiedCopyWithoutRecopyingChangedSource(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	id := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	task, _, err := inv.createQueuedVideoTask(state, path, "", "source", "", "", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.ensureQueuedVideo(context.Background(), nil, state, task); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inv.createQueuedVideoTask(state, path, "", "source", "", "", id); err == nil {
		t.Fatal("changed content reused operation")
	}
	resumed, _, err := inv.createQueuedVideoTask(state, "", "", "source", "", "", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.ensureQueuedVideo(context.Background(), nil, state, resumed); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(task.Path)
	if string(body) != "original" {
		t.Fatal("recovery copy changed")
	}
	_, err = inv.jobs.Run("video-ppt:"+id, "", func(context.Context) (any, error) {
		inv.projectVideoSource(state, id)
		current := inv.jobs.Current().(map[string]any)
		source, ok := current["source"].(contract.MediaSource)
		if !ok || source.Status != "resolved" || source.Path != path || source.SHA256 == "" {
			t.Fatal("current source projection missing", current)
		}
		pending, pendingErr := inv.handleVideoPptPending(state)
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		tasks := pending.([]videoppt.Pending)
		if len(tasks) != 1 || tasks[0].Source == nil || tasks[0].Source.SHA256 != source.SHA256 {
			t.Fatal("pending source projection missing", tasks)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutoCompletedOperationReturnsVerifiedResultWithoutModel(t *testing.T) {
	inv := NewInvoker()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := &contract.State{LibraryRoot: root}
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	url := "https://www.bilibili.com/video/BV1xx411c7mD/"
	task, _, err := inv.createQueuedVideoTask(state, "", url, "auto", "fixture", "", id)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "lecture.pdf")
	if err := os.WriteFile(output, []byte("verified fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := finishVideoPptExport(state, task, videoppt.ExportResult{Path: output, Kept: 1}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"operationId": id, "url": url, "prompt": "fixture"})
	result, err := inv.handleVideoPptAuto(nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["path"] != output || result.(map[string]any)["cleaned"] != true {
		t.Fatalf("completed result lost: %+v", result)
	}
	if _, err := os.Stat(task.Dir); !os.IsNotExist(err) {
		t.Fatal("completed task recreated", err)
	}
}
