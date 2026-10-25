package subtitlesync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateRunAndResume(t *testing.T) {
	library := t.TempDir()
	service := New(t.TempDir(), library)
	task, err := service.Create([]CreateItem{{
		Page: 1, Title: "第一讲", URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1",
	}}, "课程", "课程名")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Items) != 1 || task.Items[0].URL != "https://www.bilibili.com/video/BV1xx411c7mD/?p=1" {
		t.Fatalf("create mismatch: %+v", task)
	}
	srt := "1\n00:00:01,000 --> 00:00:02,000\n第一句\n"
	path := filepath.Join(library, "Transcripts", "课程名", "P1.srt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(context.Background(), task.ID, Dependencies{
		Acquire:    func(context.Context, Item) (string, string, error) { return srt, path, nil },
		Publish:    func(context.Context, Item, string, string) (bool, string, error) { return false, "gen_1", nil },
		Generation: func(context.Context, string) (string, error) { return "", nil },
		Progress:   func(string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Items[0].Status != "已完成" || run.Items[0].Generation == nil || *run.Items[0].Generation != "gen_1" ||
		run.Interrupted {
		t.Fatalf("run mismatch: %+v", run)
	}
	list, err := service.List()
	if err != nil || len(list) != 1 || list[0].ID != task.ID {
		t.Fatalf("list mismatch: %+v %v", list, err)
	}
}
