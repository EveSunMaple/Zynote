package api

import (
	"context"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func holdQueueWorker(t *testing.T, inv *Invoker) {
	t.Helper()
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = inv.jobs.Run("existing", "", func(context.Context) (any, error) { close(entered); <-release; return nil, nil })
	}()
	<-entered
	t.Cleanup(func() { close(release); <-finished })
}

func waitVideoQueued(t *testing.T, inv *Invoker, state *contract.State, done <-chan error) videoppt.Pending {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("request finished before queue admission: %v", err)
		default:
		}
		value, err := inv.handleVideoPptPending(state)
		if err != nil {
			t.Fatal(err)
		}
		rows := value.([]videoppt.Pending)
		if len(rows) == 1 && rows[0].QueueState == "queued" {
			return rows[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("task never queued")
		}
		time.Sleep(time.Millisecond)
	}
}

func cancelQueuedCall(t *testing.T, inv *Invoker, done <-chan error) {
	t.Helper()
	inv.jobs.CancelAll()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled queued request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queue cancellation stuck")
	}
}
