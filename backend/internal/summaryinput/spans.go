package summaryinput

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

const SpanChunkPolicy = "subtitle-core-codepoints-v2"

type SpanCue struct {
	Cue       int    `json:"cue"`
	Time      string `json:"time"`
	Text      string `json:"text"`
	CoreStart int    `json:"coreStartCodepoint"`
	CoreEnd   int    `json:"coreEndCodepoint"`
	StartMs   int64  `json:"startMs"`
	EndMs     int64  `json:"endMs"`
}
type SpanInput struct {
	Mode string    `json:"mode"`
	Cues []SpanCue `json:"cues"`
}
type SpanChunk struct {
	Input     SpanInput
	CoreSpans []contract.CoreSpan
}

func SpanDigest(input SpanInput) (string, error) {
	body, err := encodeValue(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// PlanSpans is deterministic, Unicode-codepoint based, and keeps parent cue
// timing intact. It never invents subcue word times by character interpolation.
func PlanSpans(mode string, segments []documents.Segment, limit int) ([]SpanChunk, error) {
	if limit < 1 {
		return nil, fmt.Errorf("摘要分块限制无效")
	}
	chunks := []SpanChunk{}
	used := 0
	for cue, segment := range segments {
		text := []rune(segment.Text)
		if len(text) == 0 {
			return nil, fmt.Errorf("摘要字幕正文为空")
		}
		for start := 0; start < len(text); {
			if len(chunks) == 0 || used == limit {
				chunks = append(chunks, SpanChunk{Input: SpanInput{Mode: mode, Cues: []SpanCue{}}})
				used = 0
			}
			end := start + limit - used
			if end > len(text) {
				end = len(text)
			}
			chunk := &chunks[len(chunks)-1]
			time := Cues([]documents.Segment{segment}, cue)[0].Time
			chunk.Input.Cues = append(chunk.Input.Cues, SpanCue{Cue: cue, Time: time, Text: string(text[start:end]), CoreStart: start, CoreEnd: end, StartMs: segment.Start, EndMs: segment.End})
			chunk.CoreSpans = append(chunk.CoreSpans, contract.CoreSpan{ParentCueID: cue, StartCodepoint: start, EndCodepoint: end})
			used += end - start
			start = end
		}
	}
	if err := ValidateCorePartition(segments, chunks); err != nil {
		return nil, err
	}
	return chunks, nil
}

func ValidateCorePartition(segments []documents.Segment, chunks []SpanChunk) error {
	positions := make([]int, len(segments))
	lastCue := -1
	for _, chunk := range chunks {
		if len(chunk.Input.Cues) != len(chunk.CoreSpans) {
			return fmt.Errorf("摘要核心正文与区间数量不一致")
		}
		for index, span := range chunk.CoreSpans {
			if span.ParentCueID < 0 || span.ParentCueID >= len(segments) || span.ParentCueID < lastCue || span.StartCodepoint != positions[span.ParentCueID] || span.EndCodepoint <= span.StartCodepoint || span.EndCodepoint > len([]rune(segments[span.ParentCueID].Text)) {
				return fmt.Errorf("摘要核心区间有缺口或重叠")
			}
			cue := chunk.Input.Cues[index]
			parent := segments[span.ParentCueID]
			if cue.Cue != span.ParentCueID || cue.CoreStart != span.StartCodepoint || cue.CoreEnd != span.EndCodepoint || cue.Text != string([]rune(parent.Text)[span.StartCodepoint:span.EndCodepoint]) || cue.StartMs != parent.Start || cue.EndMs != parent.End || cue.Time != Cues([]documents.Segment{parent}, span.ParentCueID)[0].Time {
				return fmt.Errorf("摘要核心正文、父时间或编号不一致")
			}
			positions[span.ParentCueID] = span.EndCodepoint
			lastCue = span.ParentCueID
		}
	}
	for cue, segment := range segments {
		if positions[cue] != len([]rune(segment.Text)) {
			return fmt.Errorf("摘要核心区间未覆盖全部字幕")
		}
	}
	return nil
}
