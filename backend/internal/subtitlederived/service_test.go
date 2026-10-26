package subtitlederived

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndRun(t *testing.T) {
	library := t.TempDir()
	service := New(t.TempDir(), library)
	srt := "1\n00:00:01,000 --> 00:00:02,000\n第一句\n"
	path := filepath.Join(library, "Transcripts", "课程", "P1.srt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(srt))
	task, err := service.Create([]Source{{
		URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: "第一讲",
		Path: path, Hash: hex.EncodeToString(sum[:]), Generation: "gen_1",
	}}, []string{"brief", "mindmap"}, "课程")
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Run(context.Background(), task.ID, Dependencies{
		Generation: func(context.Context, string) (string, error) { return "gen_1", nil },
		Summarize: func(context.Context, string, string) (string, []Point, error) {
			return "# 摘要", []Point{{Cue: 0, Quote: "第一句", Point: "要点", Topic: "主题", Timestamp: "00:00:01"}}, nil
		},
		RenderSVG: func(string, []Point) string { return "<svg/>" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Items) != 2 || run.Interrupted || run.Items[0].Status != "已完成" || run.Items[1].Status != "已完成" {
		t.Fatalf("run mismatch: %+v", run)
	}
	for _, item := range run.Items {
		if len(item.Outputs) < 2 {
			t.Fatalf("outputs missing: %+v", item)
		}
	}
}
