package research

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type WorkflowStep struct {
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	Answer       string       `json:"answer"`
	AnswerSHA256 *string      `json:"answer_sha256"`
	TaskID       *string      `json:"task_id"`
	CreateOp     string       `json:"create_op"`
	Quotes       []QuoteCheck `json:"quotes,omitempty"`
}

type Workflow struct {
	WorkflowID     string         `json:"workflow_id"`
	Question       string         `json:"question"`
	Status         string         `json:"status"`
	Revision       int            `json:"revision"`
	Fence          int            `json:"fence"`
	OutcomeUnknown bool           `json:"outcome_unknown"`
	Bindings       []Binding      `json:"bindings"`
	Steps          []WorkflowStep `json:"steps"`
	ErrorCode      string         `json:"error_code"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	// Quick 为真时每一步都关闭模型深度思考（快速模式）。
	Quick bool `json:"quick,omitempty"`
	// Progress 只在读取时由内存中的运行状态填入，不写入数据库。
	Progress *TaskProgress `json:"progress,omitempty"`
}

func (engine *Engine) readWorkflow(id string) (Workflow, error) {
	if !opPattern.MatchString(id) {
		return Workflow{}, fmt.Errorf("工作流标识无效")
	}
	row, err := engine.Store.Read("workflow", id)
	if err != nil {
		return Workflow{}, err
	}
	encoded, _ := marshalJSON(row)
	var flow Workflow
	if unmarshalJSON(encoded, &flow) != nil || flow.WorkflowID != id {
		return Workflow{}, fmt.Errorf("工作流数据无效")
	}
	if flow.Status == "running" {
		flow.Progress = activeRun(id).progress()
	}
	return flow, nil
}

func (engine *Engine) putWorkflow(flow *Workflow) error {
	return activeRun(flow.WorkflowID).guard(flow.Status == "stopped", func() error { return engine.writeWorkflow(flow) })
}

func (engine *Engine) writeWorkflow(flow *Workflow) error {
	flow.Progress = nil
	flow.Revision++
	flow.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, _ := marshalJSON(flow)
	var row map[string]any
	if unmarshalJSON(encoded, &row) != nil {
		return fmt.Errorf("工作流数据无效")
	}
	return engine.Store.Put("workflow", flow.WorkflowID, flow.Revision, row)
}

func (engine *Engine) ListWorkflows() []any {
	rows, err := engine.Store.List("workflow")
	if err != nil {
		return []any{}
	}
	sort.SliceStable(rows, func(left, right int) bool {
		return text(rows[left]["created_at"]) > text(rows[right]["created_at"])
	})
	result := []any{}
	for _, row := range rows {
		if text(row["status"]) == "running" {
			if progress := activeRun(text(row["workflow_id"])).progress(); progress != nil {
				row["progress"] = progress
			}
		}
		result = append(result, workflowListRow(row))
	}
	return result
}

func workflowListRow(row map[string]any) map[string]any {
	slim := map[string]any{}
	for key, value := range row {
		if key != "steps" {
			slim[key] = value
			continue
		}
		items, _ := value.([]any)
		steps := make([]any, 0, len(items))
		for _, item := range items {
			step, _ := item.(map[string]any)
			answer, _ := step["answer"].(string)
			trimmed := trimText(answer, 2000)
			entry := map[string]any{"name": step["name"], "status": step["status"], "quotes": step["quotes"], "task_id": step["task_id"]}
			entry["answer"] = trimmed
			if len([]rune(trimmed)) < len([]rune(answer)) {
				entry["answer_truncated"] = true
			}
			steps = append(steps, entry)
		}
		slim[key] = steps
	}
	return slim
}

func (engine *Engine) CreateWorkflow(ctx context.Context, input map[string]any) (any, error) {
	op := text(input["op"])
	question := trimText(text(input["question"]), 800)
	if !opPattern.MatchString(op) || question == "" {
		return nil, fmt.Errorf("工作流参数无效")
	}
	bindings := parseBindings(input["materials"])
	if len(bindings) == 0 || len(bindings) > 6 {
		return nil, fmt.Errorf("工作流参数无效")
	}
	result, err := engine.Store.Operation(op, input, func() (any, error) {
		materials := []Source{}
		for _, binding := range bindings {
			source, err := engine.Sources.Read(binding)
			if err != nil {
				return nil, err
			}
			materials = append(materials, source)
		}
		if err := engine.Sources.Verify(materials); err != nil {
			return nil, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		flow := Workflow{
			WorkflowID: randomID(), Question: question, Status: "paused", OutcomeUnknown: false,
			Bindings: bindings, ErrorCode: "", CreatedAt: now, UpdatedAt: now, Quick: input["quick"] == true,
		}
		for _, name := range []string{"材料整理", "综合回答"} {
			flow.Steps = append(flow.Steps, WorkflowStep{Name: name, Status: "pending", CreateOp: randomID()})
		}
		if err := engine.putWorkflow(&flow); err != nil {
			return nil, err
		}
		return map[string]any{"id": flow.WorkflowID}, nil
	})
	if err != nil {
		return nil, err
	}
	// 创建后立即开始运行，不必再点一次“继续”。
	flow, err := engine.readWorkflow(text(result.(map[string]any)["id"]))
	if err != nil || flow.Status != "paused" || flow.Revision != 1 {
		return flow, err
	}
	return engine.startWorkflow(ctx, &flow)
}

func (engine *Engine) stopWorkflow(input map[string]any, id, op string) (any, error) {
	result, err := engine.Store.Operation(op, input, func() (any, error) {
		flow, err := engine.readWorkflow(id)
		if err != nil {
			return nil, err
		}
		if flow.Revision != int(number(input["revision"])) || flow.Fence != int(number(input["fence"])) {
			return nil, fmt.Errorf("工作流版本已改变，请刷新")
		}
		if flow.Status != "running" {
			return nil, fmt.Errorf("工作流未运行")
		}
		activeRun(id).stop()
		flow.Status = "stopped"
		flow.Fence++
		flow.ErrorCode = "用户停止工作流"
		if err := engine.putWorkflow(&flow); err != nil {
			return nil, err
		}
		return map[string]any{"id": flow.WorkflowID}, nil
	})
	if err != nil {
		return nil, err
	}
	return engine.readWorkflow(text(result.(map[string]any)["id"]))
}

func (engine *Engine) resumeWorkflow(ctx context.Context, input map[string]any, id string) (any, error) {
	flow, err := engine.readWorkflow(id)
	if err != nil {
		return nil, err
	}
	if flow.Revision != int(number(input["revision"])) || flow.Fence != int(number(input["fence"])) {
		return nil, fmt.Errorf("工作流版本已改变，请刷新")
	}
	if flow.Status == "completed" || flow.Status == "running" {
		return nil, fmt.Errorf("当前工作流不能继续")
	}
	if flow.OutcomeUnknown && input["retry_unknown"] != true {
		return nil, fmt.Errorf("存在未确认模型请求，需要明确确认后再继续")
	}
	for _, binding := range flow.Bindings {
		if _, err := engine.Sources.Read(binding); err != nil {
			return nil, err
		}
	}
	return engine.startWorkflow(ctx, &flow)
}

// startWorkflow 把工作流标为运行中并在后台逐步执行；没有后台执行器时就地同步运行。
func (engine *Engine) startWorkflow(ctx context.Context, flow *Workflow) (any, error) {
	flow.Status = "running"
	flow.Fence++
	flow.OutcomeUnknown = false
	flow.ErrorCode = ""
	// 未完成的步骤换新的操作编号：沿用旧编号会直接取回上次失败或中断的研究任务。
	for index := range flow.Steps {
		if flow.Steps[index].Status != "completed" {
			flow.Steps[index].CreateOp = randomID()
		}
	}
	if err := engine.putWorkflow(flow); err != nil {
		return nil, err
	}
	id := flow.WorkflowID
	run := beginRun(id, nil, "")
	err := engine.launch(ctx, id, run, true, func(runContext context.Context, worker *Engine) error {
		current := *flow
		if err := worker.runWorkflow(runContext, run, &current); err != nil {
			current.Status = "failed"
			current.ErrorCode = err.Error()
			_ = worker.putWorkflow(&current)
			return err
		}
		current.Status = "completed"
		return worker.putWorkflow(&current)
	})
	if err != nil {
		return nil, err
	}
	return engine.readWorkflow(id)
}

func (engine *Engine) ActionWorkflow(ctx context.Context, input map[string]any) (any, error) {
	id := text(input["id"])
	op := text(input["op"])
	kind := text(input["kind"])
	if !opPattern.MatchString(id) || !opPattern.MatchString(op) || (kind != "stop" && kind != "resume") {
		return nil, fmt.Errorf("工作流参数无效")
	}
	if kind == "stop" {
		return engine.stopWorkflow(input, id, op)
	}
	return engine.resumeWorkflow(ctx, input, id)
}

func (engine *Engine) runWorkflow(ctx context.Context, run *runState, flow *Workflow) error {
	for index := range flow.Steps {
		step := &flow.Steps[index]
		if step.Status == "completed" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errResearchStopped
		}
		input := map[string]any{
			"op": step.CreateOp, "channels": []any{"library"}, "materials": bindingValues(flow.Bindings),
			"question": "请为以下问题整理原文证据，区分资料中的共识和缺失：" + flow.Question,
			"quick":    flow.Quick,
		}
		if index > 0 {
			// 前一步草稿完整交给第二步（不再截成 1500 字），但只作整理线索，不当来源。
			input["question"] = "请依据所选原文回答：" + flow.Question
			input["workflow_draft"] = flow.Steps[index-1].Answer
		}
		prefix := fmt.Sprintf("第 %d/%d 步 %s：", index+1, len(flow.Steps), step.Name)
		created, err := engine.create(ctx, input, false, run, prefix)
		if err != nil {
			return err
		}
		task := created.(Task)
		if task.Status != "completed" {
			return fmt.Errorf("工作流步骤未完成")
		}
		hash := hashText(task.Note)
		taskID := task.TaskID
		step.Status = "completed"
		step.Answer = task.Note
		step.Quotes = task.Quotes
		step.AnswerSHA256 = &hash
		step.TaskID = &taskID
		if err := engine.putWorkflow(flow); err != nil {
			return err
		}
	}
	return nil
}

func bindingValues(bindings []Binding) []any {
	result := []any{}
	for _, binding := range bindings {
		encoded, _ := marshalJSON(binding)
		var value map[string]any
		_ = unmarshalJSON(encoded, &value)
		result = append(result, value)
	}
	return result
}

func (engine *Engine) VerifyWorkflow(id string) (Workflow, error) {
	flow, err := engine.readWorkflow(id)
	if err != nil {
		return Workflow{}, err
	}
	if flow.Status != "completed" {
		return Workflow{}, fmt.Errorf("工作流尚未完整核验")
	}
	for _, step := range flow.Steps {
		if step.Status != "completed" || step.AnswerSHA256 == nil || *step.AnswerSHA256 != hashText(step.Answer) ||
			step.TaskID == nil {
			return Workflow{}, fmt.Errorf("工作流尚未完整核验")
		}
		if _, err := engine.VerifyResult(*step.TaskID); err != nil {
			return Workflow{}, err
		}
	}
	return flow, nil
}

func (engine *Engine) ExportWorkflow(id string) (any, error) {
	flow, err := engine.VerifyWorkflow(id)
	if err != nil {
		return nil, err
	}
	text := "# " + flow.Question + "\n\n"
	for _, step := range flow.Steps {
		text += "## " + step.Name + "\n\n" + step.Answer + "\n\n" + quoteMarkdown(step.Quotes)
	}
	encoded, _ := marshalJSON(flow.Bindings)
	text += "## 来源版本\n\n```json\n" + string(encoded) + "\n```\n"
	directory := filepath.Join(engine.Store.root, exportDirectory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("研究导出目录无效")
	}
	path := filepath.Join(directory, flow.WorkflowID+"-工作流.md")
	if existing, err := os.ReadFile(path); err == nil {
		if hashText(string(existing)) != hashText(text) {
			return nil, fmt.Errorf("已有研究成果被修改，保留当前文件")
		}
		return path, nil
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, fmt.Errorf("研究导出失败")
	}
	return path, nil
}

func trimText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func marshalJSON(value any) ([]byte, error)        { return jsonMarshal(value) }
func unmarshalJSON(value []byte, target any) error { return jsonUnmarshal(value, target) }
func jsonMarshal(value any) ([]byte, error)        { return json.Marshal(value) }
func jsonUnmarshal(value []byte, target any) error { return json.Unmarshal(value, target) }

// DeleteTask 删除一条研究记录。运行中的要先停止；属于工作流某一步的只能随工作流一起删除，
// 否则工作流会因缺少步骤记录而无法再核验导出。已保存的笔记是独立副本，不受影响。
func (engine *Engine) DeleteTask(id string) (any, error) {
	task, err := engine.readTask(id)
	if err != nil {
		return nil, err
	}
	if task.Status == "running" {
		return nil, fmt.Errorf("研究仍在运行，请先停止")
	}
	for _, row := range engine.ListWorkflows() {
		encoded, _ := marshalJSON(row)
		var flow Workflow
		if unmarshalJSON(encoded, &flow) != nil {
			continue
		}
		for _, step := range flow.Steps {
			if step.TaskID != nil && *step.TaskID == id {
				return nil, fmt.Errorf("这条研究是工作流的一步，请删除整个工作流")
			}
		}
	}
	if err := engine.Store.Delete("task", id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}

// DeleteWorkflow 删除工作流及其各步研究记录；运行中的要先停止。
func (engine *Engine) DeleteWorkflow(id string) (any, error) {
	flow, err := engine.readWorkflow(id)
	if err != nil {
		return nil, err
	}
	if flow.Status == "running" {
		return nil, fmt.Errorf("工作流仍在运行，请先停止")
	}
	for _, step := range flow.Steps {
		if step.TaskID != nil {
			if err := engine.Store.Delete("task", *step.TaskID); err != nil {
				return nil, err
			}
		}
	}
	if err := engine.Store.Delete("workflow", id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}
