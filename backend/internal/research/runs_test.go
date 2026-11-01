package research

import (
	"context"
	"strings"
	"testing"
	"time"
)

// backgroundEngine 返回一个在后台运行研究的引擎：模型以流式写出 first，然后等 release 才写完。
func backgroundEngine(t *testing.T, first string, release chan struct{}) (*Engine, []any) {
	t.Helper()
	engine, bindings := citationEngine(t, []string{sampleSRT("今天讲泰勒公式", "余项有两种形式", "先看佩亚诺余项")})
	engine.Spawn = func(run func(*Engine)) { go run(engine) }
	engine.Stream = func(ctx context.Context, _, _ string, onText, onThinking func(string)) (string, error) {
		onThinking("先读资料")
		onText(first)
		select {
		case <-release:
			return first + "[S1@00:00:10]。", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return engine, bindings
}

func waitTask(t *testing.T, engine *Engine, id string, done func(Task) bool) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := engine.Read(id)
		if err != nil {
			t.Fatal(err)
		}
		if task := value.(Task); done(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("研究任务没有在预期时间内到达所需状态")
	return Task{}
}

func TestResearchRunsInBackgroundWithProgress(t *testing.T) {
	release := make(chan struct{})
	engine, bindings := backgroundEngine(t, "老师说“余项有两种形式”", release)
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("a", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := createdTask(t, engine, created)
	if task.Status != "running" {
		t.Fatalf("create must return at once with a running task, got %s", task.Status)
	}
	writing := waitTask(t, engine, task.TaskID, func(task Task) bool {
		return task.Progress != nil && task.Progress.Stage == "writing"
	})
	if writing.Progress.Chars == 0 || !strings.Contains(writing.Progress.Preview, "余项有两种形式") {
		t.Fatalf("progress must show streamed text: %+v", writing.Progress)
	}
	listed := engine.List()[0].(map[string]any)
	if listed["progress"] == nil {
		t.Fatal("task list must carry progress of running tasks")
	}
	close(release)
	done := waitTask(t, engine, task.TaskID, func(task Task) bool { return task.Status != "running" })
	if done.Status != "completed" || done.Progress != nil || len(done.Quotes) != 1 || done.Quotes[0].Status != "exact" {
		t.Fatalf("background research should complete with checked quotes: %+v", done)
	}
}

func TestStopCancelsBackgroundResearch(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	engine, bindings := backgroundEngine(t, "老师说", release)
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("b", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := createdTask(t, engine, created).TaskID
	waitTask(t, engine, id, func(task Task) bool { return task.Progress != nil && task.Progress.Stage == "writing" })
	if _, err := engine.Stop(id); err != nil {
		t.Fatal(err)
	}
	// 模型请求被取消后，后台的失败保存不得把“已停止”改掉。
	deadline := time.Now().Add(2 * time.Second)
	for activeRun(id) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if activeRun(id) != nil {
		t.Fatal("cancelled research must leave the run registry")
	}
	value, _ := engine.Read(id)
	if task := value.(Task); task.Status != "stopped" {
		t.Fatalf("stopped research must stay stopped, got %s (%s)", task.Status, task.ErrorCode)
	}
}

// createdTask 读回 Create 登记的完整任务（Create 只回不含原文的精简行）。
func createdTask(t *testing.T, engine *Engine, created any) Task {
	t.Helper()
	row, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("create must return a slim row, got %T", created)
	}
	if _, has := row["materials"]; has {
		t.Fatal("create must not return source text")
	}
	task, err := engine.readTask(text(row["task_id"]))
	if err != nil {
		t.Fatal(err)
	}
	if active := activeRun(task.TaskID); active != nil && task.Status == "running" {
		task.Progress = active.progress()
	}
	return task
}
