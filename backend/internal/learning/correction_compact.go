package learning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ValidateCompactCorrection requires an explicit ordered processing receipt for
// every source row. An empty patches list never implies unlisted rows processed.
func ValidateCompactCorrection(value []byte, batch CorrectionBatch) ([]CorrectedRow, error) {
	failure := fmt.Errorf("紧凑校订结果缺少完整处理确认或有效改动，已拒绝保存")
	var output struct {
		Processing string `json:"processing"`
		Processed  []*int `json:"processed"`
		Patches    *[]struct {
			ID        *int    `json:"id"`
			Text      *string `json:"text"`
			Uncertain *bool   `json:"uncertain"`
			Reason    *string `json:"reason"`
		} `json:"patches"`
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&output) != nil || output.Processing != "complete" || len(output.Processed) != len(batch.Rows) || output.Patches == nil {
		return nil, failure
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, failure
	}
	result := make([]CorrectedRow, len(batch.Rows))
	for index, source := range batch.Rows {
		if output.Processed[index] == nil || *output.Processed[index] != batch.Start+index {
			return nil, failure
		}
		result[index] = CorrectedRow{Text: source.Text}
	}
	seen := map[int]bool{}
	for _, patch := range *output.Patches {
		if patch.ID == nil || patch.Text == nil || patch.Uncertain == nil || patch.Reason == nil || strings.TrimSpace(*patch.Text) == "" || strings.TrimSpace(*patch.Reason) == "" || len([]rune(*patch.Reason)) > 240 {
			return nil, failure
		}
		index := *patch.ID - batch.Start
		if index < 0 || index >= len(batch.Rows) || seen[index] {
			return nil, failure
		}
		seen[index] = true
		if !*patch.Uncertain && *patch.Text == batch.Rows[index].Text {
			return nil, failure
		}
		text := *patch.Text
		// Speculative alternatives are not silently promoted to corrected text.
		if *patch.Uncertain {
			text = batch.Rows[index].Text
		}
		result[index] = CorrectedRow{Text: text, Uncertain: *patch.Uncertain, Reason: *patch.Reason}
	}
	return result, nil
}
