package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrCancelled is returned when the user cancels the active job.
var ErrCancelled = errors.New("任务已取消")

type Registry struct {
	mu      sync.Mutex
	active  map[string]context.CancelFunc
	current *Snapshot
}

type Snapshot struct {
	Key       string
	LectureID string
	Started   time.Time
}

func NewRegistry() *Registry {
	return &Registry{active: map[string]context.CancelFunc{}}
}

func (registry *Registry) Run(key, lectureID string, run func(context.Context) (any, error)) (any, error) {
	registry.mu.Lock()
	if len(registry.active) > 0 {
		registry.mu.Unlock()
		return nil, fmt.Errorf("当前已有任务运行，请等待完成或取消")
	}
	ctx, cancel := context.WithCancel(context.Background())
	registry.active[key] = cancel
	registry.current = &Snapshot{Key: key, LectureID: lectureID, Started: time.Now()}
	registry.mu.Unlock()
	defer func() {
		registry.mu.Lock()
		delete(registry.active, key)
		if registry.current != nil && registry.current.Key == key {
			registry.current = nil
		}
		registry.mu.Unlock()
		cancel()
	}()
	result, err := run(ctx)
	if ctx.Err() != nil {
		return nil, ErrCancelled
	}
	return result, err
}

func (registry *Registry) CancelAll() bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	cancelled := false
	for _, cancel := range registry.active {
		cancel()
		cancelled = true
	}
	return cancelled
}

// Cancel stops one active job by key.
func (registry *Registry) Cancel(key string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	cancel, ok := registry.active[key]
	if !ok {
		return false
	}
	cancel()
	return true
}

func (registry *Registry) Current() any {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.current == nil {
		return nil
	}
	return map[string]any{
		"key": registry.current.Key, "lectureId": registry.current.LectureID,
	}
}
