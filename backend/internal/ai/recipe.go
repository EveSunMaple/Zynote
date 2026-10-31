package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

const jsonMaxAttempts = 2
const jsonRepairFeedback = "\n上次输出未通过格式校验。请严格返回指定结构的合法 JSON，不加说明。"

func recipeDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func JSONRecipePolicy() contract.JSONGenerationPolicy {
	return contract.JSONGenerationPolicy{Version: 1, Encoding: "json-no-html-escape", MaxAttempts: jsonMaxAttempts, RepairFeedbackSHA256: recipeDigest(jsonRepairFeedback)}
}

// TextRequestConfiguration shares model, endpoint and parameter construction
// with Chat. Only the non-secret effective model parameters are retained.
// Non-streaming and Responses store=false are fixed transport policies.
func (client *Client) TextRequestConfiguration() (contract.RequestedGenerationConfig, error) {
	profile := client.Profile
	model, err := chatModel(profile, false)
	if err != nil {
		return contract.RequestedGenerationConfig{}, err
	}
	responses := profile.APIFormat == "responses"
	endpoint := chatEndpoint(profile, responses)
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || (parsed.RawQuery != "" || parsed.ForceQuery) || parsed.Fragment != "" {
		return contract.RequestedGenerationConfig{}, fmt.Errorf("摘要模型端点无效，未建立生成配方")
	}
	body := chatBody(profile, model, map[string]any{}, responses, false)
	delete(body, "model")
	delete(body, "stream")
	format := "chat"
	if responses {
		format = "responses"
	}
	return contract.RequestedGenerationConfig{Version: 1, APIFormat: format, EndpointSHA256: recipeDigest(endpoint), RequestedModel: model, EffectiveParameters: body, ServerModelState: "unknown", AccountScopeState: "unknown"}, nil
}
