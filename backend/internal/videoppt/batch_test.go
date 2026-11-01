package videoppt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBatchBindingIsImmutableConcurrentAndValidatedOnRead(t *testing.T) {
	service := New(t.TempDir())
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	input := BatchInput{URL: "https://www.bilibili.com/video/BV1234567890/?p=1", Pages: []int{1, 2}, Prompt: "fixture"}
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := service.BindBatch(id, input); failures <- err }()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	changed := input
	changed.Prompt = "changed"
	if _, err := service.BindBatch(id, changed); err == nil {
		t.Fatal("input replaced")
	}
	episodes := []BatchEpisode{{Page: 1, Title: "first", URL: input.URL}, {Page: 2, Title: "second", URL: "https://www.bilibili.com/video/BV1234567890/?p=2"}}
	if _, err := service.SetBatchEpisodes(id, episodes); err != nil {
		t.Fatal(err)
	}
	if BatchChildID(id, 1) != "a50c0683-99b5-5148-b279-bd871a13d5bd" || BatchChildID(id, 1) == BatchChildID(id, 2) || BatchChildID(id, 1) == BatchChildID("cccccccc-cccc-4ccc-8ccc-cccccccccccc", 1) {
		t.Fatal("unstable child identities")
	}
	batch, err := service.Batch(id)
	if err != nil {
		t.Fatal(err)
	}
	batch.Episodes[1].URL = "https://www.bilibili.com/video/BV0987654321/?p=2"
	path, _ := service.batchPath(id)
	if err := saveTaskJSON(path, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Batch(id); err == nil {
		t.Fatal("changed episode source trusted")
	}
}

func TestStableBatchChildRetainsUnknownModelGuard(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	id := BatchChildID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", 2)
	operation := VideoOperation{Version: 1, Stage: "auto", URL: "https://www.bilibili.com/video/BV1234567890/?p=2", Prompt: "fixture"}
	task, err := service.CreateOperationTask(id, "second", operation)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := func(context.Context, string, string, []Image) (string, error) {
		calls++
		return "", errors.New("controlled lost response")
	}
	if _, err := CheckpointChat(task, "source", "profile", provider)(context.Background(), "system", "input", nil); err == nil {
		t.Fatal("expected dispatch failure")
	}
	task, err = service.CreateOperationTask(id, "second", operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckpointChat(task, "source", "profile", provider)(context.Background(), "system", "input", nil); err == nil || calls != 1 {
		t.Fatal("unknown replay", err, calls)
	}
	if _, err := os.Stat(task.Dir); err != nil {
		t.Fatal("recovery task removed", err)
	}
}
