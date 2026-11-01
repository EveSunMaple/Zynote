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
		ctx       context.Context
		profile   Profile
		responses bool
		want      bool
	}{
		{"千问快速模式", quick, qwen, false, true},
		{"千问默认", context.Background(), qwen, false, false},
		{"其他服务商不加", quick, other, false, false},
		{"responses 接口不加", quick, qwen, true, false},
	}
	for _, item := range cases {
		body := map[string]any{}
		applyThinking(item.ctx, item.profile, body, item.responses)
		_, has := body["enable_thinking"]
		if has != item.want || (has && body["enable_thinking"] != false) {
			t.Fatalf("%s: got %v", item.name, body)
		}
	}
}
