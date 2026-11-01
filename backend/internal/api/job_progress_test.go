package api

import (
	"context"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type progressRecorder struct{ events []any }

func (r *progressRecorder) Notify(_ string, value any) error {
	r.events = append(r.events, value)
	return nil
}

func TestRejectedJobDoesNotBroadcastCompletion(t *testing.T) {
	inv := NewInvoker()
	recorder := &progressRecorder{}
	inv.SetNotifier(recorder)
	_, err := inv.jobs.Run("video-summary", "", func(context.Context) (any, error) {
		_, busy := inv.runJob("video-summary", func(context.Context) (any, error) { t.Fatal("rejected callback ran"); return nil, nil })
		app, ok := busy.(*contract.AppError)
		if !ok || app.Code != contract.CodeBusy {
			t.Fatalf("expected BUSY, got %v", busy)
		}
		if len(recorder.events) != 0 {
			t.Fatal("rejected task broadcast progress/completion")
		}
		inv.videoProgress("正在生成摘要")
		if len(recorder.events) != 1 {
			t.Fatal("progress not emitted")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
