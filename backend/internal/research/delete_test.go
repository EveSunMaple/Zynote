package research

import (
	"context"
	"strings"
	"testing"
)

func TestDeleteResearchAndWorkflow(t *testing.T) {
	engine, bindings := citationEngine(t, []string{sampleSRT("今天讲泰勒公式", "余项有两种形式")})
	engine.Model = func(context.Context, string, string) (string, error) {
		return "老师说“余项有两种形式”[S1@00:00:10]。", nil
	}
	created, err := engine.Create(context.Background(), map[string]any{
		"op": strings.Repeat("1", 32), "question": "余项", "channels": []any{"library"}, "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	single := createdTask(t, engine, created).TaskID
	flowValue, err := engine.CreateWorkflow(context.Background(), map[string]any{
		"op": strings.Repeat("2", 32), "question": "余项", "materials": bindings,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := flowValue.(Workflow)
	if flow.Status != "completed" || flow.Steps[0].TaskID == nil {
		t.Fatalf("workflow should complete: %+v", flow)
	}
	if _, err := engine.DeleteTask(*flow.Steps[0].TaskID); err == nil {
		t.Fatal("a workflow step must not be deleted on its own")
	}
	if _, err := engine.DeleteTask(single); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.DeleteWorkflow(flow.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if len(engine.List()) != 0 || len(engine.ListWorkflows()) != 0 {
		t.Fatalf("records should be gone: %d tasks, %d workflows", len(engine.List()), len(engine.ListWorkflows()))
	}
}
