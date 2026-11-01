package research

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// MCP 结果只是辅助线索：模型填不出参数时跳过该工具并记录，不让整次研究失败，也不调用远端工具。
func TestMcpPlanUnavailableSkipsTool(t *testing.T) {
	for name, plan := range map[string]string{"unavailable": `{"unavailable":true}`, "invalid": "不是 JSON"} {
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			engine := &Engine{Store: store, Model: func(context.Context, string, string) (string, error) { return plan, nil }}
			task := &Task{TaskID: "0123456789abcdef0123456789abcdef", Status: "running", Limitations: "基础说明。"}
			snapshot := &McpSnapshot{Tools: []selectedMcpTool{{McpTool: McpTool{Name: "docs_search"}, ServerID: "s1"}}}
			if err := engine.runMcpTools(context.Background(), task, "泰勒公式", snapshot); err != nil {
				t.Fatalf("skip must not fail research: %v", err)
			}
			if task.Status != "running" || len(task.ToolResults) != 0 {
				t.Fatalf("tool must be skipped without results: %+v", task)
			}
			if !strings.Contains(task.Limitations, "docs_search 未调用") {
				t.Fatalf("limitations must record the skipped tool: %q", task.Limitations)
			}
			last := task.Events[len(task.Events)-1]
			if last["kind"] != "mcp-skipped" {
				t.Fatalf("skip event missing: %+v", task.Events)
			}
		})
	}
}
