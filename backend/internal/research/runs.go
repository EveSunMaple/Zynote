package research

import (
	"context"
	"errors"
	"sync"
	"time"
)

// 研究和工作流在后台运行：创建请求只登记任务并立即返回，读资料、调用模型、核对原话
// 在后台 goroutine 里进行。runs 记录本进程内正在运行的任务，用于显示进度和真正取消。
// 进度只在内存里，不写库；进程重启后残留的 running 任务由 initialize 标为中断。

var errResearchStopped = errors.New("研究已被用户停止")

type TaskProgress struct {
	Stage   string `json:"stage"`
	Label   string `json:"label"`
	Chars   int    `json:"chars,omitempty"`
	Preview string `json:"preview,omitempty"`
	Elapsed int    `json:"elapsed_seconds"`
}

type runState struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped bool
	started time.Time
	stage   string
	label   string
	chars   int
	written []rune
	parent  *runState
	prefix  string
}

var runs sync.Map

const previewRunes = 600

func beginRun(id string, parent *runState, prefix string) *runState {
	run := &runState{started: time.Now(), stage: "queued", label: "排队中", parent: parent, prefix: prefix}
	runs.Store(id, run)
	return run
}

func activeRun(id string) *runState {
	value, ok := runs.Load(id)
	if !ok {
		return nil
	}
	return value.(*runState)
}

func endRun(id string, run *runState) {
	runs.CompareAndDelete(id, run)
}

// setStage 切换阶段；工作流步骤的进度同时带前缀反映到工作流上。
func (run *runState) setStage(stage, label string) {
	if run == nil {
		return
	}
	run.mu.Lock()
	run.stage, run.label = stage, label
	run.chars, run.written = 0, nil
	run.mu.Unlock()
	run.parent.setStage(stage, run.prefix+label)
}

// write 记录模型流式返回的回答文字：累计字数，保留末尾一段供界面预览。
func (run *runState) write(chunk string) {
	if run == nil || chunk == "" {
		return
	}
	run.record("writing", "模型正在写回答", chunk, true)
}

// think 记录模型的思考过程：只计字数显示进度，不展示内容。
func (run *runState) think(chunk string) {
	if run == nil || chunk == "" {
		return
	}
	run.record("thinking", "模型正在阅读资料、思考", chunk, false)
}

func (run *runState) record(stage, label, chunk string, keep bool) {
	run.mu.Lock()
	if run.stage != stage {
		run.chars, run.written = 0, nil
	}
	run.stage, run.label = stage, label
	run.chars += len([]rune(chunk))
	if keep {
		run.written = append(run.written, []rune(chunk)...)
		if len(run.written) > previewRunes {
			run.written = append([]rune(nil), run.written[len(run.written)-previewRunes:]...)
		}
	}
	run.mu.Unlock()
	if run.parent != nil {
		run.parent.record(stage, run.prefix+label, chunk, keep)
	}
}

func (run *runState) progress() *TaskProgress {
	if run == nil {
		return nil
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return &TaskProgress{
		Stage: run.stage, Label: run.label, Chars: run.chars, Preview: string(run.written),
		Elapsed: int(time.Since(run.started).Seconds()),
	}
}

// stop 标记停止并取消正在进行的模型请求；之后后台的任何保存都会被拒绝，
// 不会把“已停止”覆盖回“已完成”或“失败”。
func (run *runState) stop() {
	if run == nil {
		return
	}
	run.mu.Lock()
	run.stopped = true
	cancel := run.cancel
	run.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// guard 在保存前调用：已停止的任务只允许写入停止状态本身。
func (run *runState) guard(allowed bool, save func() error) error {
	if run == nil {
		return save()
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.stopped && !allowed {
		return errResearchStopped
	}
	return save()
}

// launch 运行一项研究或工作流。有后台执行器时在新 goroutine 里用独立的存储连接运行并立即返回；
// 没有（测试、工作流内部的步骤）时就地同步运行并返回结果。
func (engine *Engine) launch(ctx context.Context, id string, run *runState, async bool, work func(context.Context, *Engine) error) error {
	if !async || engine.Spawn == nil {
		runContext, cancel := context.WithCancel(ctx)
		run.mu.Lock()
		run.cancel = cancel
		run.mu.Unlock()
		defer func() {
			cancel()
			endRun(id, run)
		}()
		return work(runContext, engine)
	}
	runContext, cancel := context.WithCancel(context.Background())
	run.mu.Lock()
	run.cancel = cancel
	run.mu.Unlock()
	engine.Spawn(func(worker *Engine) {
		defer func() {
			cancel()
			endRun(id, run)
		}()
		_ = work(runContext, worker)
	})
	return nil
}

// 导出目录用英文名，避免部分同步盘和命令行工具处理中文路径出错。
const (
	exportDirectory     = "research-exports"
	noteExportDirectory = "note-exports"
)
