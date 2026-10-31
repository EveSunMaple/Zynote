package jobs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func operation(index int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", index) }

func waitQueue(t *testing.T, registry *Registry, root, id, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if record, err := registry.QueueRecord(root, id); err == nil && record.State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue %s did not reach %s", id, state)
}

func TestQueueFIFOAndRestartProjection(t *testing.T) {
	registry := NewRegistry()
	root := filepath.Join(t.TempDir(), "queue")
	block := make(chan struct{})
	entered := make(chan struct{})
	activeDone := make(chan struct{})
	go func() {
		defer close(activeDone)
		_, _ = registry.Run("existing", "", func(context.Context) (any, error) { close(entered); <-block; return nil, nil })
	}()
	<-entered
	order := make(chan int, 2)
	results := make(chan error, 2)
	for i := 1; i <= 2; i++ {
		go func(index int) {
			_, err := registry.RunQueued(context.Background(), root, operation(index), "notes", func(context.Context) (any, error) { order <- index; return nil, nil })
			results <- err
		}(i)
		waitQueue(t, registry, root, operation(i), "queued")
	}
	// A reconstructed registry cannot claim ownership of the old process's work.
	other := NewRegistry()
	if record, err := other.QueueRecord(root, operation(1)); err != nil || record.State != "interrupted" {
		t.Fatalf("restart projection: %+v %v", record, err)
	}
	close(block)
	<-activeDone
	for i := 1; i <= 2; i++ {
		select {
		case got := <-order:
			if got != i {
				t.Fatalf("FIFO: got %d want %d", got, i)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queue stuck")
		}
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	waitQueue(t, registry, root, operation(2), "completed")
}

func TestQueueDuplicateCancelAndCapacity(t *testing.T) {
	registry := NewRegistry()
	root := filepath.Join(t.TempDir(), "queue")
	block := make(chan struct{})
	entered := make(chan struct{})
	activeDone := make(chan struct{})
	go func() {
		defer close(activeDone)
		_, _ = registry.Run("existing", "", func(context.Context) (any, error) { close(entered); <-block; return nil, nil })
	}()
	<-entered
	defer func() { close(block); <-activeDone }()
	results := make(chan error, QueueLimit)
	for i := 1; i <= QueueLimit; i++ {
		go func(index int) {
			_, err := registry.RunQueued(context.Background(), root, operation(index), "notes", func(context.Context) (any, error) { t.Error("cancelled waiter executed"); return nil, nil })
			results <- err
		}(i)
		waitQueue(t, registry, root, operation(i), "queued")
	}
	for _, id := range []string{operation(1), operation(QueueLimit + 1)} {
		if _, err := registry.RunQueued(context.Background(), root, id, "notes", nil); !errors.Is(err, ErrBusy) {
			t.Fatalf("duplicate/full: %v", err)
		}
	}
	registry.CancelAll()
	for i := 0; i < QueueLimit; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrCancelled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancel stuck")
		}
	}
	waitQueue(t, registry, root, operation(1), "cancelled")
}

func TestQueueIdentityAndDomainBusyNotReplayed(t *testing.T) {
	registry := NewRegistry()
	root := filepath.Join(t.TempDir(), "queue")
	calls := 0
	_, err := registry.RunQueued(context.Background(), root, operation(1), "notes", func(context.Context) (any, error) { calls++; return nil, ErrBusy })
	if !errors.Is(err, ErrBusy) || calls != 1 {
		t.Fatalf("domain retried: %d %v", calls, err)
	}
	waitQueue(t, registry, root, operation(1), "failed")
	if _, err := registry.RunQueued(context.Background(), root, operation(1), "different", nil); err == nil {
		t.Fatal("operation rebound")
	}
}
