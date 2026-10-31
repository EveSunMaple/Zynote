package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCancelled is returned when the user cancels the active job.
var ErrCancelled = errors.New("任务已取消")
var ErrBusy = errors.New("当前已有任务运行，请等待完成或取消")

type Registry struct {
	mu      sync.Mutex
	active  map[string]context.CancelFunc
	current *Snapshot
}

type Snapshot struct {
	Key       string
	LectureID string
	Started   time.Time
	Progress  *Progress
}

type Progress struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Key     string `json:"key"`
}

// Report records the latest progress without retaining caller-owned mutable data.
func (registry *Registry) Report(message string) Progress {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	progress := Progress{Stage: "视频学习", Message: message}
	if registry.current == nil {
		return progress
	}
	progress.Key = registry.current.Key
	switch progress.Key {
	case "video-summary", "批量课程笔记":
		progress.Stage = "视频摘要"
	case "video-subtitle", "video-subtitles":
		progress.Stage = "字幕转写"
	case "video-directory":
		progress.Stage = "视频来源"
	case "video-ppt", "video-batch":
		progress.Stage = "视频课件"
	case "ppt-extract":
		progress.Stage = "PPT 截取"
	case "提取画面", "继续提取画面", "识别画面", "恢复画面成果":
		progress.Stage = "画面识别"
	default:
		progress.Stage = progress.Key
	}
	registry.current.Progress = &progress
	return progress
}

func NewRegistry() *Registry {
	return &Registry{active: map[string]context.CancelFunc{}}
}

func (registry *Registry) Run(key, lectureID string, run func(context.Context) (any, error)) (any, error) {
	registry.mu.Lock()
	if len(registry.active) > 0 {
		registry.mu.Unlock()
		return nil, ErrBusy
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
	result := map[string]any{
		"key": registry.current.Key, "lectureId": registry.current.LectureID,
	}
	if registry.current.Progress != nil {
		result["progress"] = *registry.current.Progress
	}
	return result
}
