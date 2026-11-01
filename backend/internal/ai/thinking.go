package ai

import (
	"context"
	"strings"
)

type thinkingKey struct{}

// WithoutThinking 标记这次请求关闭模型的深度思考，用于快速模式和检索词改写这类不需要推理的小请求。
// 只对通义千问（DashScope）兼容接口生效；其他服务商不认识 enable_thinking，传了反而可能报错。
func WithoutThinking(ctx context.Context) context.Context {
	return context.WithValue(ctx, thinkingKey{}, true)
}

func thinkingDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(thinkingKey{}).(bool)
	return disabled
}

// applyThinking 在需要时往 chat/completions 请求体里加 enable_thinking=false。
func applyThinking(ctx context.Context, profile Profile, body map[string]any, responses bool) {
	if responses || !thinkingDisabled(ctx) || !strings.Contains(strings.ToLower(profile.BaseURL), "dashscope") {
		return
	}
	body["enable_thinking"] = false
}
