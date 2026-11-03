package contract

import (
	"context"
	"errors"
)

type GenerationObservation struct {
	CoreSpans        []CoreSpan `json:"coreSpans,omitempty"`
	ProtocolVersion  int        `json:"protocolVersion,omitempty"`
	Purpose          string     `json:"purpose"`
	TaskID           string     `json:"taskID"`
	ItemIndex        int        `json:"itemIndex"`
	ItemRecipeSHA256 string     `json:"itemRecipeSHA256"`
	SourceSHA256     string     `json:"sourceSHA256"`
	ChunkIndex       int        `json:"chunkIndex"`
	ChunkCount       int        `json:"chunkCount"`
	CueIDs           []int      `json:"cueIDs"`
	InputSHA256      string     `json:"inputSHA256"`
	AttemptIndex     int        `json:"attemptIndex"`
	FeedbackSHA256   string     `json:"feedbackSHA256"`
}
type CoreSpan struct {
	ParentCueID    int `json:"parentCueId"`
	StartCodepoint int `json:"startCodepoint"`
	EndCodepoint   int `json:"endCodepoint"`
}
type GenerationAttemptEvidence struct {
	Version             int                     `json:"version"`
	Observation         GenerationObservation   `json:"observation"`
	State               string                  `json:"state"`
	ConfigurationSHA256 string                  `json:"configurationSHA256"`
	EndpointSHA256      string                  `json:"endpointSHA256"`
	SystemPromptSHA256  string                  `json:"systemPromptSHA256"`
	RequestBodySHA256   string                  `json:"requestBodySHA256"`
	HTTPStatus          int                     `json:"httpStatus,omitempty"`
	DecodedTextSHA256   string                  `json:"decodedTextSHA256,omitempty"`
	FormatValid         *bool                   `json:"formatValid,omitempty"`
	ValidatedPoints     *ValidatedPointEvidence `json:"validatedPoints,omitempty"`
}
type GenerationObserver func(context.Context, GenerationAttemptEvidence) error

type generationObservationKey struct{}
type generationObservationContext struct {
	metadata GenerationObservation
	observer GenerationObserver
}

func WithGenerationObservation(ctx context.Context, metadata GenerationObservation, observer GenerationObserver) context.Context {
	metadata.CueIDs = append([]int(nil), metadata.CueIDs...)
	metadata.CoreSpans = append([]CoreSpan(nil), metadata.CoreSpans...)
	return context.WithValue(ctx, generationObservationKey{}, generationObservationContext{metadata, observer})
}
func GenerationObservationFrom(ctx context.Context) (GenerationObservation, GenerationObserver) {
	value, _ := ctx.Value(generationObservationKey{}).(generationObservationContext)
	value.metadata.CueIDs = append([]int(nil), value.metadata.CueIDs...)
	value.metadata.CoreSpans = append([]CoreSpan(nil), value.metadata.CoreSpans...)
	return value.metadata, value.observer
}

type GenerationObservationError struct{ Cause error }

func (value *GenerationObservationError) Error() string {
	return "摘要请求证据无效或保存失败，任务已停止，未继续调用模型"
}
func (value *GenerationObservationError) Unwrap() error { return value.Cause }
func ObservationFailure(err error) error {
	if err == nil {
		return nil
	}
	var existing *GenerationObservationError
	if errors.As(err, &existing) {
		return err
	}
	return &GenerationObservationError{Cause: err}
}
func IsObservationFailure(err error) bool {
	var target *GenerationObservationError
	return errors.As(err, &target)
}
