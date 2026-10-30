package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestZhiyunTaskStorePersistsAndInterruptsStaleTasks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zhiyun-tasks")
	store := newZhiyunTaskStore(root)
	task, err := store.Create(zhiyunExportInput{
		Kind: "ppt", CourseID: "123", SubID: "456", CourseName: "微积分", Title: "第一讲",
	}, "第一讲")
	if err != nil {
		t.Fatal(err)
	}
	if task.State != zhiyunTaskQueued || task.Title != "第一讲" {
		t.Fatalf("created task mismatch: %+v", task)
	}
	if _, err := store.Update(task.ID, func(value *zhiyunTask) {
		value.State = zhiyunTaskRunning
		value.Stage = "正在下载"
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := newZhiyunTaskStore(root)
	rows, err := reloaded.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != zhiyunTaskInterrupted {
		t.Fatalf("stale task should be interrupted: %+v", rows)
	}

	if _, err := reloaded.Update(task.ID, func(value *zhiyunTask) {
		value.State = zhiyunTaskCompleted
		value.OutputPath = "C:/PPT/第一讲.pptx"
		value.LectureID = "00000000-0000-4000-8000-000000000001"
	}); err != nil {
		t.Fatal(err)
	}
	persisted := newZhiyunTaskStore(root)
	loaded, err := persisted.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != zhiyunTaskCompleted || loaded.OutputPath == "" || loaded.LectureID == "" {
		t.Fatalf("completed task mismatch: %+v", loaded)
	}
	if _, err := os.Stat(filepath.Join(root, task.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestCancelMarksQueuedZhiyunTasksCancelled(t *testing.T) {
	invoker := NewInvoker()
	state := &contract.State{DataRoot: t.TempDir()}
	task, err := invoker.zhiyunTaskStore(state).Create(zhiyunExportInput{
		Kind: "ppt", CourseID: "123", SubID: "456", CourseName: "课程",
	}, "第一讲")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoker.handleCancel(nil, state, nil); err != nil {
		t.Fatal(err)
	}
	current, err := invoker.zhiyunTaskStore(state).Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != zhiyunTaskCancelled {
		t.Fatalf("queued task should be cancelled: %+v", current)
	}
}

func TestZhiyunTaskStoreRejectsUnknownTask(t *testing.T) {
	store := newZhiyunTaskStore(filepath.Join(t.TempDir(), "zhiyun-tasks"))
	if _, err := store.Get("00000000-0000-4000-8000-000000000009"); err == nil {
		t.Fatal("unknown task must fail")
	}
	if validZhiyunTaskID("../escape") {
		t.Fatal("invalid task id must fail")
	}
}
