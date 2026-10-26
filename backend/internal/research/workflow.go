package research

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type WorkflowStep struct {
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	Answer       string  `json:"answer"`
	AnswerSHA256 *string `json:"answer_sha256"`
	TaskID       *string `json:"task_id"`
	CreateOp     string  `json:"create_op"`
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
	return flow, nil
}

func (engine *Engine) putWorkflow(flow *Workflow) error {
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
	result := []any{}
	for _, row := range rows {
		result = append(result, row)
	}
	return result
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
			Bindings: bindings, ErrorCode: "", CreatedAt: now, UpdatedAt: now,
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
	return engine.readWorkflow(text(result.(map[string]any)["id"]))
}

func (engine *Engine) ActionWorkflow(ctx context.Context, input map[string]any) (any, error) {
	id := text(input["id"])
	op := text(input["op"])
	kind := text(input["kind"])
	if !opPattern.MatchString(id) || !opPattern.MatchString(op) || (kind != "stop" && kind != "resume") {
		return nil, fmt.Errorf("工作流参数无效")
	}
	if kind == "stop" {
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
	flow.Status = "running"
	flow.Fence++
	flow.OutcomeUnknown = false
	flow.ErrorCode = ""
	if err := engine.runWorkflow(ctx, &flow); err != nil {
		flow.Status = "failed"
		flow.ErrorCode = err.Error()
		_ = engine.putWorkflow(&flow)
		return nil, err
	}
	flow.Status = "completed"
	if err := engine.putWorkflow(&flow); err != nil {
		return nil, err
	}
	return engine.readWorkflow(flow.WorkflowID)
}

func (engine *Engine) runWorkflow(ctx context.Context, flow *Workflow) error {
	for index := range flow.Steps {
		step := &flow.Steps[index]
		if step.Status == "completed" {
			continue
		}
		question := ""
		if index == 0 {
			question = trimText("请为以下问题整理原文证据，区分资料中的共识和缺失："+flow.Question, 1500)
		} else {
			question = trimText("请依据所选原文回答："+flow.Question+"\n前一步草稿仅为整理线索，不是额外来源，不得替代原文核对：\n"+trimText(flow.Steps[0].Answer, 10000), 1500)
		}
		created, err := engine.Create(ctx, map[string]any{
			"op": step.CreateOp, "question": question, "channels": []any{"library"}, "materials": bindingValues(flow.Bindings),
		})
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
		text += "## " + step.Name + "\n\n" + step.Answer + "\n\n"
	}
	encoded, _ := marshalJSON(flow.Bindings)
	text += "## 来源版本\n\n```json\n" + string(encoded) + "\n```\n"
	directory := filepath.Join(engine.Store.root, "研究成果")
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
