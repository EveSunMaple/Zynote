package learning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func compactCorrectionFailure(detail string) error {
	return fmt.Errorf("紧凑校订结果缺少完整处理确认或有效改动，已拒绝保存（%s）", detail)
}

// ValidateCompactCorrection requires an explicit ordered processing receipt for
// every source row. An empty patches list never implies unlisted rows processed.
func ValidateCompactCorrection(value []byte, batch CorrectionBatch) ([]CorrectedRow, error) {
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
	if err := decoder.Decode(&output); err != nil {
		return nil, compactCorrectionFailure("JSON 结构无效：" + err.Error())
	}
	if output.Processing != "complete" {
		return nil, compactCorrectionFailure("processing 不是 complete")
	}
	if output.Patches == nil {
		return nil, compactCorrectionFailure("缺少 patches 数组")
	}
	if len(output.Processed) != len(batch.Rows) {
		return nil, compactCorrectionFailure(fmt.Sprintf(
			"processed 数量 %d，应为 %d", len(output.Processed), len(batch.Rows),
		))
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, compactCorrectionFailure("JSON 后存在多余内容")
	}
	result := make([]CorrectedRow, len(batch.Rows))
	for index, source := range batch.Rows {
		if output.Processed[index] == nil {
			return nil, compactCorrectionFailure(fmt.Sprintf("processed[%d] 为空", index))
		}
		if *output.Processed[index] != batch.Start+index {
			return nil, compactCorrectionFailure(fmt.Sprintf(
				"processed[%d]=%d，应为 %d", index, *output.Processed[index], batch.Start+index,
			))
		}
		result[index] = CorrectedRow{Text: source.Text}
	}
	seen := map[int]bool{}
	for _, patch := range *output.Patches {
		switch {
		case patch.ID == nil:
			return nil, compactCorrectionFailure("patch 缺少 id")
		case patch.Text == nil:
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 缺少 text", *patch.ID))
		case patch.Uncertain == nil:
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 缺少 uncertain", *patch.ID))
		case patch.Reason == nil:
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 缺少 reason", *patch.ID))
		case strings.TrimSpace(*patch.Text) == "":
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 的 text 为空", *patch.ID))
		case strings.TrimSpace(*patch.Reason) == "" || len([]rune(*patch.Reason)) > 240:
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 的 reason 为空或过长", *patch.ID))
		}
		index := *patch.ID - batch.Start
		if index < 0 || index >= len(batch.Rows) {
			return nil, compactCorrectionFailure(fmt.Sprintf(
				"patch id=%d 超出本批范围 %d–%d", *patch.ID, batch.Start, batch.Start+len(batch.Rows)-1,
			))
		}
		if seen[index] {
			return nil, compactCorrectionFailure(fmt.Sprintf("patch id=%d 重复", *patch.ID))
		}
		seen[index] = true
		if !*patch.Uncertain && *patch.Text == batch.Rows[index].Text {
			return nil, compactCorrectionFailure(fmt.Sprintf(
				"patch id=%d 标记为已修改但文本未变", *patch.ID,
			))
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
