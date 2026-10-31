package videoppt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestModelCheckpointUnknownNeverReplays(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	calls := 0
	callback := func(context.Context, string, string, []Image) (string, error) {
		calls++
		paths, _ := filepath.Glob(filepath.Join(task.Dir, "model-source-*.json"))
		if len(paths) != 1 {
			t.Fatal("missing dispatch record")
		}
		body, err := os.ReadFile(paths[0])
		if err != nil || len(body) == 0 {
			t.Fatal("not persisted before dispatch", err)
		}
		return "", errors.New("response lost")
	}
	if _, err := CheckpointChat(task, "source", "connection", callback)(context.Background(), "system", "text", nil); err == nil {
		t.Fatal("unknown accepted")
	}
	for _, text := range []string{"text", "changed"} {
		if _, err := CheckpointChat(task, "source", "connection", callback)(context.Background(), "system", text, nil); err == nil {
			t.Fatal("unknown replay accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
}

func TestModelCheckpointPreparedBindsRequestAndConnection(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callback := func(context.Context, string, string, []Image) (string, error) {
		calls++
		cancel()
		return "known response", nil
	}
	if _, err := CheckpointChat(task, "extract", "connection", callback)(ctx, "system", "text", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	fresh := CheckpointChat(task, "extract", "connection", callback)
	response, err := fresh(context.Background(), "system", "text", nil)
	if err != nil || response != "known response" {
		t.Fatal("prepared response lost", response, err)
	}

	if calls != 1 {
		t.Fatalf("replayed prepared result: %d", calls)
	}
}

func TestModelCheckpointCorruptionFailsClosed(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	path := filepath.Join(task.Dir, "model-source-corrupt.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := CheckpointChat(task, "source", "connection", func(context.Context, string, string, []Image) (string, error) {
		t.Fatal("corrupt record dispatched")
		return "", nil
	})(context.Background(), "", "", nil)
	if err == nil {
		t.Fatal("corrupt accepted")
	}
}

func TestModelCheckpointExplicitRevisionsReuseTheirOwnResponses(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	calls := 0
	callback := func(_ context.Context, _ string, text string, _ []Image) (string, error) { calls++; return text, nil }
	for _, row := range []struct{ identity, text string }{{"connection", "crop-A"}, {"connection", "crop-B"}, {"changed-model", "crop-A"}, {"connection", "crop-A"}} {
		response, err := CheckpointChat(task, "extract", row.identity, callback)(context.Background(), "system", row.text, nil)
		if err != nil || response != row.text {
			t.Fatal(response, err)
		}
	}
	if calls != 3 {
		t.Fatalf("revision or reuse mismatch: %d", calls)
	}
}
