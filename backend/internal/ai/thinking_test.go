package ai

import (
	"context"
	"testing"
)

func TestApplyThinkingOnlyForDashScopeWhenRequested(t *testing.T) {
	qwen := Profile{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"}
	other := Profile{BaseURL: "https://api.openai.com/v1"}
	quick := WithoutThinking(context.Background())
	cases := []struct {
		name      string
		quick     bool
		profile   Profile
		responses bool
		want      bool
	}{
		{"千问快速模式", true, qwen, false, true},
		{"千问默认", false, qwen, false, false},
		{"其他服务商不加", true, other, false, false},
		{"responses 接口不加", true, qwen, true, false},
	}
	for _, item := range cases {
		ctx := context.Background()
		if item.quick {
			ctx = quick
		}
		body := map[string]any{}
		applyThinking(ctx, item.profile, body, item.responses)
		_, has := body["enable_thinking"]
		if has != item.want || (has && body["enable_thinking"] != false) {
			t.Fatalf("%s: got %v", item.name, body)
		}
	}
}
