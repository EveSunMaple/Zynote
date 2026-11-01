package ai

import (
	"strings"
	"testing"
)

// 思考过程只交给进度回调，不混进回答正文。
func TestReadSSESeparatesReasoning(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"先看资料"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"再组织"}}]}`,
		`data: {"choices":[{"delta":{"content":"余项有两种"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"
	thinking, text := "", ""
	answer, err := readSSEWithReasoning(strings.NewReader(stream), func(chunk string) { text += chunk }, func(chunk string) { thinking += chunk })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "余项有两种" || text != answer || thinking != "先看资料再组织" {
		t.Fatalf("answer=%q text=%q thinking=%q", answer, text, thinking)
	}
}
