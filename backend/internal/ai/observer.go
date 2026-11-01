package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func JSONInputSHA256(input any) (string, error) {
	body, err := marshalNoEscape(input)
	if err != nil {
		return "", err
	}
	return recipeDigest(string(body)), nil
}
func observationInput(ctx context.Context, encoded []byte) error {
	metadata, observer := contract.GenerationObservationFrom(ctx)
	if observer == nil {
		return nil
	}
	var input struct {
		Cues []struct {
			Cue int `json:"cue"`
		} `json:"cues"`
	}
	if json.Unmarshal(encoded, &input) != nil || metadata.Purpose != "subtitle-summary" || metadata.InputSHA256 != recipeDigest(string(encoded)) || len(input.Cues) == 0 {
		return fmt.Errorf("摘要输入观察绑定无效，未调用模型")
	}
	ids := make([]int, len(input.Cues))
	for i, cue := range input.Cues {
		ids[i] = cue.Cue
	}
	if !reflect.DeepEqual(ids, metadata.CueIDs) {
		return fmt.Errorf("摘要输入字幕编号不匹配，未调用模型")
	}
	return nil
}
func observedJSONAttempt(ctx context.Context, attempt int, feedback string) context.Context {
	metadata, observer := contract.GenerationObservationFrom(ctx)
	if observer == nil {
		return ctx
	}
	metadata.AttemptIndex = attempt
	metadata.FeedbackSHA256 = recipeDigest(feedback)
	return contract.WithGenerationObservation(ctx, metadata, observer)
}
func observationForRequest(ctx context.Context, profile Profile, endpoint string, body map[string]any, encoded []byte, messages []Message, onText func(string)) (contract.GenerationAttemptEvidence, contract.GenerationObserver, error) {
	metadata, observer := contract.GenerationObservationFrom(ctx)
	if observer == nil {
		return contract.GenerationAttemptEvidence{}, nil, nil
	}
	if metadata.Purpose != "subtitle-summary" || onText != nil || len(messages) != 2 || messages[0].Role != "system" || messages[1].Role != "user" {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要请求观察范围无效")
	}
	prompt, ok := messages[0].Content.(string)
	if !ok {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要请求必须是文本")
	}
	user, ok := messages[1].Content.(string)
	if !ok {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要请求必须是文本")
	}
	base := user
	switch metadata.AttemptIndex {
	case 0:
		if metadata.FeedbackSHA256 != recipeDigest("") {
			return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要反馈绑定无效")
		}
	case 1:
		if metadata.FeedbackSHA256 != recipeDigest(jsonRepairFeedback) || !strings.HasSuffix(user, jsonRepairFeedback) {
			return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要反馈绑定无效")
		}
		base = strings.TrimSuffix(user, jsonRepairFeedback)
	default:
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要尝试编号无效")
	}
	if err := observationInput(ctx, []byte(base)); err != nil {
		return contract.GenerationAttemptEvidence{}, nil, err
	}
	configuration, err := textRequestConfiguration(profile)
	if err != nil {
		return contract.GenerationAttemptEvidence{}, nil, err
	}
	if body["stream"] != false {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要请求传输策略无效")
	}
	wireInput, expectedInput := body["messages"], any(messages)
	if configuration.APIFormat == "responses" {
		if body["store"] != false {
			return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要请求存储策略无效")
		}
		wireInput, expectedInput = body["input"], responsesInput(messages)
	}
	actualWire, _ := marshalNoEscape(wireInput)
	expectedWire, _ := marshalNoEscape(expectedInput)
	if string(actualWire) != string(expectedWire) {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要实际输入与请求绑定不匹配")
	}
	// Compare the actual captured body to the shared request configuration.
	actual := map[string]any{}
	for key, value := range body {
		switch key {
		case "model", "stream", "messages", "input", "store":
		default:
			actual[key] = value
		}
	}
	left, _ := json.Marshal(actual)
	right, _ := json.Marshal(configuration.EffectiveParameters)
	if string(left) != string(right) || body["model"] != configuration.RequestedModel || recipeDigest(endpoint) != configuration.EndpointSHA256 {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要实际配置不匹配")
	}
	configBody, _ := json.Marshal(configuration)
	version := 1
	if metadata.ProtocolVersion == 2 {
		version = 2
	} else if metadata.ProtocolVersion != 0 {
		return contract.GenerationAttemptEvidence{}, nil, fmt.Errorf("摘要观察协议不受支持")
	}
	event := contract.GenerationAttemptEvidence{Version: version, Observation: metadata, State: "dispatch-intent", ConfigurationSHA256: recipeDigest(string(configBody)), EndpointSHA256: recipeDigest(endpoint), SystemPromptSHA256: recipeDigest(prompt), RequestBodySHA256: recipeDigest(string(encoded))}
	return event, observer, nil
}
func observedHTTPClient(client *http.Client, observer contract.GenerationObserver) *http.Client {
	if observer == nil {
		return client
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clientCopy
}
func emitFormatResult(ctx context.Context, valid bool) error {
	metadata, observer := contract.GenerationObservationFrom(ctx)
	if observer == nil {
		return nil
	}
	// Request facts are attached by Chat after the durable response-decoded event.
	facts, _ := ctx.Value(generationRequestFactsKey{}).(*contract.GenerationAttemptEvidence)
	if facts == nil || facts.State != "response-decoded" {
		return fmt.Errorf("摘要响应观察记录缺失")
	}
	event := *facts
	event.Observation = metadata
	event.State = "format-result"
	event.FormatValid = &valid
	return observer(ctx, event)
}

type generationRequestFactsKey struct{}
