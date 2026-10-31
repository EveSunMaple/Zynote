package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const QueueLimit = 16

var queueID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type QueueRecord struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
}

type queuedCall struct {
	id, root string
	cancel   context.CancelFunc
}

func saveQueue(root string, record QueueRecord) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".queue-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(root, record.ID+".json"))
}

// QueueRecord returns interrupted for a persisted queue entry with no owner in
// this process. Recovery is explicit: the domain supplies the same task ID and
// revalidates its checkpoints before resuming. Closures and credentials are never
// serialized, and unknown external calls are not replayed by this scheduler.
func (registry *Registry) QueueRecord(root, id string) (QueueRecord, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if !queueID.MatchString(id) {
		return QueueRecord{}, fmt.Errorf("invalid queue operation ID")
	}
	body, err := os.ReadFile(filepath.Join(root, id+".json"))
	if err != nil {
		return QueueRecord{}, err
	}
	var record QueueRecord
	if json.Unmarshal(body, &record) != nil || record.ID != id {
		return record, fmt.Errorf("invalid queue record")
	}
	owned := false
	for _, call := range registry.queue {
		if call.id == id && call.root == root {
			owned = true
		}
	}
	if !owned && (record.State == "queued" || record.State == "running") {
		record.State = "interrupted"
	}
	return record, nil
}

// RunQueued adds a bounded FIFO waiter while retaining the global single-worker
// invariant. The caller persists its domain inputs before invoking this method.
// After process loss the caller explicitly resumes that same domain task.
func (registry *Registry) RunQueued(ctx context.Context, root, id, key string, run func(context.Context) (any, error)) (any, error) {
	if !filepath.IsAbs(root) || !queueID.MatchString(id) {
		return nil, fmt.Errorf("invalid queue operation")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	call := &queuedCall{id: id, root: filepath.Clean(root), cancel: cancel}
	if err := registry.admitQueue(call, key); err != nil {
		return nil, err
	}
	defer registry.releaseQueue(call)
	return registry.waitQueue(ctx, call, key, run)
}

func verifyQueueIdentity(root, id, key string) error {
	body, err := os.ReadFile(filepath.Join(root, id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var previous QueueRecord
	if json.Unmarshal(body, &previous) != nil || previous.ID != id || previous.Key != key {
		return fmt.Errorf("queue operation identity mismatch")
	}
	return nil
}

func (registry *Registry) admitQueue(call *queuedCall, key string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.queue) >= QueueLimit {
		return ErrBusy
	}
	for _, existing := range registry.queue {
		if existing.id == call.id && existing.root == call.root {
			return ErrBusy
		}
	}
	if err := verifyQueueIdentity(call.root, call.id, key); err != nil {
		return err
	}
	if err := call.setState(key, "queued"); err != nil {
		return err
	}
	registry.queue = append(registry.queue, call)
	return nil
}

func (registry *Registry) releaseQueue(call *queuedCall) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for i, entry := range registry.queue {
		if entry == call {
			registry.queue = append(registry.queue[:i], registry.queue[i+1:]...)
			return
		}
	}
}

func (call *queuedCall) setState(key, state string) error {
	return saveQueue(call.root, QueueRecord{ID: call.id, Key: key, State: state, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

func (call *queuedCall) finish(ctx context.Context, key string, result any, err error) (any, error) {
	if ctx.Err() != nil {
		result = nil
		err = ErrCancelled
	}
	state := "completed"
	if err != nil {
		state = "failed"
	}
	if errors.Is(err, ErrCancelled) {
		state = "cancelled"
	}
	if saveErr := call.setState(key, state); saveErr != nil {
		return nil, saveErr
	}
	return result, err
}

func (registry *Registry) firstQueued(call *queuedCall) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return len(registry.queue) > 0 && registry.queue[0] == call
}

func runQueueCall(ctx, active context.Context, run func(context.Context) (any, error)) (any, error) {
	combined, stop := context.WithCancel(active)
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	if ctx.Err() != nil {
		return nil, ErrCancelled
	}
	return run(combined)
}

func (registry *Registry) waitQueue(ctx context.Context, call *queuedCall, key string, run func(context.Context) (any, error)) (any, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return call.finish(ctx, key, nil, ErrCancelled)
		}
		if registry.firstQueued(call) {
			started := false
			result, err := registry.Run(key, "", func(active context.Context) (any, error) {
				started = true
				if err := call.setState(key, "running"); err != nil {
					return nil, err
				}
				return runQueueCall(ctx, active, run)
			})
			// A BUSY error from inside domain work must not replay that work.
			if started || !errors.Is(err, ErrBusy) {
				return call.finish(ctx, key, result, err)
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
