package documents

import (
	"fmt"
	"regexp"
	"strings"
)

type transcriptRow struct {
	Text  string
	Stamp *int64
}

var voiceHeadingPattern = regexp.MustCompile(`^\d+\.\s*语音识别$`)
var blockStampPattern = regexp.MustCompile(`^(?:\[|【)?(\d{1,2}:\d{2}(?::\d{2})?)(?:\]|】)?\s*[:：]?\s*(.*)$`)

type SplitResult struct {
	Text      string    `json:"text"`
	Segments  []Segment `json:"segments"`
	Estimated bool      `json:"estimated"`
}

func transcriptBlocks(text string) []string {
	blocks := []string{}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(text, -1) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			blocks = append(blocks, trimmed)
		}
	}
	for index, block := range blocks {
		if voiceHeadingPattern.MatchString(block) {
			return blocks[index+1:]
		}
	}
	return blocks
}

func transcriptStamp(raw string) (int64, error) {
	if strings.Count(raw, ":") == 1 {
		padded := raw
		if len(padded) < 5 {
			padded = strings.Repeat("0", 5-len(padded)) + padded
		}
		return milliseconds("00:" + padded)
	}
	padded := raw
	if len(padded) < 8 {
		padded = strings.Repeat("0", 8-len(padded)) + padded
	}
	return milliseconds(padded)
}

func parseTranscriptRow(block string) (transcriptRow, error) {
	value := block
	stamp := (*int64)(nil)
	if match := blockStampPattern.FindStringSubmatch(block); match != nil {
		parsed, err := transcriptStamp(match[1])
		if err != nil {
			return transcriptRow{}, err
		}
		stamp = &parsed
		value = match[2]
	}
	if value == "" && stamp == nil {
		return transcriptRow{}, nil
	}
	return transcriptRow{Text: value, Stamp: stamp}, nil
}

func transcriptSegments(rows []transcriptRow) ([]Segment, error) {
	segments := []Segment{}
	cursor := int64(0)
	pending := (*int64)(nil)
	for index, row := range rows {
		if row.Text == "" {
			pending = row.Stamp
			continue
		}
		start := cursor
		if row.Stamp != nil {
			start = *row.Stamp
		} else if pending != nil {
			start = *pending
		}
		pending = nil
		end := start + maxInt64(1000, int64(len([]rune(row.Text)))*250)
		for _, later := range rows[index+1:] {
			if later.Stamp != nil {
				if *later.Stamp > start {
					end = *later.Stamp
				}
				break
			}
		}
		if len(segments) > 0 && start < segments[len(segments)-1].End {
			segments[len(segments)-1].End = start
		}
		if len(segments) > 0 && start <= segments[len(segments)-1].Start {
			return nil, fmt.Errorf("原文时间戳没有递增，请先检查原文")
		}
		segments = append(segments, Segment{Start: start, End: end, Text: row.Text})
		cursor = end
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("语音识别原文为空")
	}
	return segments, nil
}

func SplitTranscript(text string) (SplitResult, error) {
	rows := []transcriptRow{}
	for _, block := range transcriptBlocks(text) {
		row, err := parseTranscriptRow(block)
		if err != nil {
			return SplitResult{}, err
		}
		if row.Text != "" || row.Stamp != nil {
			rows = append(rows, row)
		}
	}
	segments, err := transcriptSegments(rows)
	if err != nil {
		return SplitResult{}, err
	}
	return SplitResult{Text: text, Segments: segments, Estimated: true}, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
