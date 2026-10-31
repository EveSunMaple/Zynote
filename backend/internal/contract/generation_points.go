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
	AcceptedFormatRef GenerationEventRef `json:"acceptedFormatRef"`
	PointsSHA256      string             `json:"pointsSHA256"`
	PointCount        int                `json:"pointCount"`
}
type ValidatedSummaryPoint struct {
	Cue       int    `json:"cue"`
	Quote     string `json:"quote"`
	Point     string `json:"point"`
	Topic     string `json:"topic"`
	Timestamp string `json:"timestamp"`
}
type SummaryEvidence struct {
	Version           int                `json:"version"`
	SourceSHA256      string             `json:"sourceSHA256"`
	ItemRecipeHash    string             `json:"itemRecipeHash"`
	FinalPointsSHA256 string             `json:"finalPointsSHA256"`
	PointCount        int                `json:"pointCount"`
	ValidatedHead     GenerationEventRef `json:"validatedHead"`
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
	Ref   GenerationEventRef
	Event GenerationAttemptEvidence
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
	event.Observation.CueIDs = append([]int(nil), event.Observation.CueIDs...)
	if event.FormatValid != nil {
		value := *event.FormatValid
		event.FormatValid = &value
	}
	if event.ValidatedPoints != nil {
		value := *event.ValidatedPoints
		event.ValidatedPoints = &value
	}
	return event
}
