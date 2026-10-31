package videoppt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOperationBindingAndCompletedReceiptSurviveCleanup(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	operation := VideoOperation{Version: 1, Stage: "auto", URL: "https://fixture/video", Prompt: "crop", CourseName: "course"}
	task, err := service.CreateOperationTask(id, "lecture", operation)
	if err != nil {
		t.Fatal(err)
	}
	if same, err := service.CreateOperationTask(id, "lecture", operation); err != nil || same.ID != id {
		t.Fatal(same, err)
	}
	changed := operation
	changed.Prompt = "changed"
	if _, err := service.CreateOperationTask(id, "lecture", changed); err == nil {
		t.Fatal("changed request reused identity")
	}
	output := filepath.Join(t.TempDir(), "lecture.pdf")
	if err := os.WriteFile(output, []byte("verified fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteOperation(task, map[string]any{"path": output}, output); err != nil {
		t.Fatal(err)
	}
	if err := service.DiscardCompleted(id); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateOperationTask(id, "lecture", operation)
	var completed *CompletedOperationError
	if !errors.As(err, &completed) || completed.Path != output {
		t.Fatal("completion not retained", err)
	}
	if _, err := service.CreateOperationTask(id, "lecture", changed); err == nil {
		t.Fatal("completed binding changed")
	}
	if err := os.WriteFile(output, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateOperationTask(id, "lecture", operation); err == nil {
		t.Fatal("edited result accepted")
	}
}

func TestVideoReadyRejectsUnverifiedAndChangedCopy(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	task.Path = filepath.Join(task.Dir, "video.mp4")
	if err := os.WriteFile(task.Path, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyVideoReady(task); err == nil {
		t.Fatal("unverified copy accepted")
	}
	if err := MarkVideoReady(task); err != nil {
		t.Fatal(err)
	}
	if err := VerifyVideoReady(task); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.Path, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyVideoReady(task); err == nil {
		t.Fatal("changed copy accepted")
	}
}
