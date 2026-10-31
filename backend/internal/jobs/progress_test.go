package jobs

import (
	"context"
	"errors"
	"testing"
)

func TestProgressSnapshotAndBusyIsolation(t *testing.T) {
	r := NewRegistry()
	_, err := r.Run("video-summary", "", func(context.Context) (any, error) {
		p := r.Report("正在生成第2段")
		if p.Stage != "视频摘要" || p.Key != "video-summary" {
			t.Fatalf("wrong progress: %+v", p)
		}
		snapshot := r.Current().(map[string]any)
		if snapshot["progress"].(Progress).Message != p.Message {
			t.Fatal("progress missing on reconnect")
		}
		snapshot["progress"] = Progress{Message: "changed"}
		if r.Current().(map[string]any)["progress"].(Progress).Message != p.Message {
			t.Fatal("snapshot aliases registry")
		}
		if _, busy := r.Run("video-subtitle", "", func(context.Context) (any, error) { t.Fatal("busy callback ran"); return nil, nil }); !errors.Is(busy, ErrBusy) {
			t.Fatalf("busy: %v", busy)
		}
		if r.Current().(map[string]any)["key"] != "video-summary" {
			t.Fatal("active task replaced")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Current() != nil {
		t.Fatal("finished progress retained")
	}
	_, _ = r.Run("video-subtitle", "", func(context.Context) (any, error) {
		if _, ok := r.Current().(map[string]any)["progress"]; ok {
			t.Fatal("previous progress leaked")
		}
		if r.Report("正在转写").Stage != "字幕转写" {
			t.Fatal("incorrect subtitle stage")
		}
		return nil, nil
	})
}
