package subtitlederived

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func recoveryTask(t *testing.T) (*Service, Task) {
	t.Helper()
	root := t.TempDir()
	library := t.TempDir()
	service := New(root, library)
	text := "1\n00:00:01,000 --> 00:00:02,000\n第一句\n"
	path := filepath.Join(library, "source.srt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := service.Create([]Source{{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "恢复测试", Path: path, Hash: digest(text), Generation: "g1"}}, []string{"brief"}, "课程")
	if err != nil {
		t.Fatal(err)
	}
	return service, task
}

func TestUnknownGenerationIsNotReplayedAfterRestart(t *testing.T) {
	service, task := recoveryTask(t)
	calls := 0
	deps := Dependencies{Summarize: func(context.Context, string, string) (string, []Point, error) {
		calls++
		disk, err := service.Read(task.ID)
		if err != nil || disk.Items[0].GenerationState != "dispatched" {
			t.Fatal("dispatch not persisted before model call")
		}
		return "", nil, fmt.Errorf("response lost")
	}}
	if _, err := service.Run(context.Background(), task.ID, deps); err != nil {
		t.Fatal(err)
	}
	restarted := New(service.Root, service.Library)
	restored, err := restarted.Run(context.Background(), task.ID, deps)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || restored.Items[0].GenerationState != "dispatched" || restored.Items[0].Status != "失败" {
		t.Fatalf("unsafe replay: calls=%d task=%+v", calls, restored)
	}
}

func TestPreparedGenerationResumesWithoutModel(t *testing.T) {
	service, task := recoveryTask(t)
	task.Items[0].Prepared = &Prepared{Markdown: "# 已生成", Points: []Point{{Quote: "第一句", Point: "结论"}}}
	task.Items[0].GenerationState = "prepared"
	if err := service.save(task); err != nil {
		t.Fatal(err)
	}
	restarted := New(service.Root, service.Library)
	deps := Dependencies{Summarize: func(context.Context, string, string) (string, []Point, error) {
		t.Fatal("prepared result regenerated")
		return "", nil, nil
	}}
	for i := 0; i < 2; i++ {
		result, err := restarted.Run(context.Background(), task.ID, deps)
		if err != nil || result.Items[0].Status != "已完成" {
			t.Fatalf("restore failed: %+v %v", result, err)
		}
	}
}
