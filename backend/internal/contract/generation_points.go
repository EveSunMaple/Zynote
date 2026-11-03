package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type GenerationEventRef struct {
	Version  int    `json:"version"`
	Sequence int    `json:"sequence"`
	SHA256   string `json:"sha256"`
}
type ValidatedPointEvidence struct {
	Processing        *ChunkProcessingReceipt `json:"processing,omitempty"`
	AcceptedFormatRef GenerationEventRef      `json:"acceptedFormatRef"`
	PointsSHA256      string                  `json:"pointsSHA256"`
	PointCount        int                     `json:"pointCount"`
}
type ChunkProcessingReceipt struct {
	State          string `json:"state"`
	CoreSpanSHA256 string `json:"coreSpanSHA256"`
}

func CoreSpansDigest(spans []CoreSpan) string {
	body, _ := json.Marshal(spans)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type ValidatedSummaryPoint struct {
	Cue       int    `json:"cue"`
	Quote     string `json:"quote"`
	Point     string `json:"point"`
	Topic     string `json:"topic"`
	Timestamp string `json:"timestamp"`
}
type SummaryEvidence struct {
	GlobalRootSHA256      string             `json:"globalRootSHA256,omitempty"`
	GlobalState           string             `json:"globalState,omitempty"`
	GlobalStages          int                `json:"globalStages,omitempty"`
	GlobalUnreviewed      int                `json:"globalUnreviewed,omitempty"`
	GlobalUpperUnreviewed int                `json:"globalUpperUnreviewed,omitempty"`
	TreeRootSHA256        string             `json:"treeRootSHA256,omitempty"`
	TreeLeafSetSHA256     string             `json:"treeLeafSetSHA256,omitempty"`
	TreeDispositionSHA256 string             `json:"treeDispositionSHA256,omitempty"`
	TreeGeneration        int64              `json:"treeGeneration,omitempty"`
	TreeEpoch             int64              `json:"treeEpoch,omitempty"`
	TreePages             int                `json:"treePages,omitempty"`
	TreeNodes             int                `json:"treeNodes,omitempty"`
	Partial               bool               `json:"partial,omitempty"`
	Version               int                `json:"version"`
	SourceSHA256          string             `json:"sourceSHA256"`
	ItemRecipeHash        string             `json:"itemRecipeHash"`
	FinalPointsSHA256     string             `json:"finalPointsSHA256"`
	PointCount            int                `json:"pointCount"`
	ValidatedHead         GenerationEventRef `json:"validatedHead"`
}

func SummaryPointsDigest(points []ValidatedSummaryPoint) (string, error) {
	if points == nil {
		points = []ValidatedSummaryPoint{}
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(points); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(body.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

type GenerationReceipt struct {
	PreviousRef GenerationEventRef
	Ref         GenerationEventRef
	Event       GenerationAttemptEvidence
}
type GenerationReceiptReader func(context.Context) (GenerationReceipt, error)
type generationReceiptReaderKey struct{}

func WithGenerationReceiptReader(ctx context.Context, reader GenerationReceiptReader) context.Context {
	return context.WithValue(ctx, generationReceiptReaderKey{}, reader)
}
func ReadGenerationReceipt(ctx context.Context) (GenerationReceipt, error) {
	if err := ctx.Err(); err != nil {
		return GenerationReceipt{}, ObservationFailure(err)
	}
	reader, _ := ctx.Value(generationReceiptReaderKey{}).(GenerationReceiptReader)
	if reader == nil {
		return GenerationReceipt{}, ObservationFailure(fmt.Errorf("generation receipt unavailable"))
	}
	return reader(ctx)
}
func CloneGenerationEvent(event GenerationAttemptEvidence) GenerationAttemptEvidence {
	event.Observation.CoreSpans = append([]CoreSpan(nil), event.Observation.CoreSpans...)
	event.Observation.CueIDs = append([]int(nil), event.Observation.CueIDs...)
	if event.FormatValid != nil {
		value := *event.FormatValid
		event.FormatValid = &value
	}
	if event.ValidatedPoints != nil {
		value := *event.ValidatedPoints
		if value.Processing != nil {
			processing := *value.Processing
			value.Processing = &processing
		}
		event.ValidatedPoints = &value
	}
	return event
}
