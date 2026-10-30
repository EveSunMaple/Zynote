package jobs

import (
	"context"
	"testing"
)

func TestRunIsExclusiveAndCancellable(t *testing.T) {
	registry := NewRegistry()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := registry.Run("chat:1", "", func(ctx context.Context) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	<-started
	if _, err := registry.Run("chat:2", "", func(context.Context) (any, error) { return nil, nil }); err == nil {
		t.Fatal("second job should be rejected")
	}
	if registry.Current() == nil {
		t.Fatal("current job missing")
	}
	if !registry.CancelAll() {
		t.Fatal("cancel should report an active job")
	}
	if err := <-done; err == nil || err.Error() != "任务已取消" {
		t.Fatalf("cancel mismatch: %v", err)
	}
	if registry.Cancel("chat:1") {
		t.Fatal("cancelling a finished job should report false")
	}
	if registry.Current() != nil {
		t.Fatal("current job should be cleared")
	}
}
