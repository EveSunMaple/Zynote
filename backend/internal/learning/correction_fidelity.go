package learning

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

var correctionNumbers = regexp.MustCompile(`[0-9０-９]+(?:[.．][0-9０-９]+)?|[零〇一二两三四五六七八九十百千万亿]+`)

// CorrectionFidelityReason is a conservative damage detector, not a semantic
// verifier. Rejected proposals retain source text and require human review.
func CorrectionFidelityReason(source, proposed string, neighbors []documents.Segment) string {
	if source == proposed {
		return ""
	}
	for _, neighbor := range neighbors {
		if neighbor.Text != source && strings.TrimSpace(neighbor.Text) == strings.TrimSpace(proposed) {
			return "校订疑似复制其他字幕，已保留原文，需人工核对"
		}
	}
	if strings.Join(correctionNumbers.FindAllString(source, -1), "|") != strings.Join(correctionNumbers.FindAllString(proposed, -1), "|") {
		return "校订改变了数值，已保留原文，需人工核对"
	}
	for _, token := range []string{"不", "没", "未", "无", "勿", "非", "禁止"} {
		if strings.Count(source, token) != strings.Count(proposed, token) {
			return "校订改变了否定表达，已保留原文，需人工核对"
		}
	}
	for _, token := range []string{"=", "+", "-", "−", "±", "×", "÷", "*", "/", "^", "<", ">", "≤", "≥", "²", "³"} {
		if strings.Count(source, token) != strings.Count(proposed, token) {
			return "校订改变了公式符号，已保留原文，需人工核对"
		}
	}
	a, b := []rune(source), []rune(proposed)
	if len(b) > maxInt(len(a)*2, len(a)+64) || len(b)*100 < len(a)*65 {
		return "校订文本长度变化过大，已保留原文，需人工核对"
	}
	// Trim equal edges before bounded dynamic programming. A huge malformed
	// response must not create unbounded CPU work in live or recovery paths.
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	left, right := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	if len(left) > 0 && len(right) > 2000000/len(left) {
		return "校订差分超出安全核对预算，已保留原文，需人工核对"
	}
	common := prefix + suffix + correctionLCS(left, right)
	if common*100 < len(a)*65 {
		return "校订丢失或替换过多原句内容，已保留原文，需人工核对"
	}
	if len(a) >= 12 && (len(a)+len(b)-2*common)*100 > maxInt(len(a), len(b))*45 {
		return "校订差分过大，已保留原文，需人工核对"
	}
	return ""
}

func correctionLCS(a, b []rune) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	row := make([]int, len(a)+1)
	for _, right := range b {
		diagonal := 0
		for i, left := range a {
			old := row[i+1]
			if left == right {
				row[i+1] = diagonal + 1
			} else if row[i] > row[i+1] {
				row[i+1] = row[i]
			}
			diagonal = old
		}
	}
	return row[len(a)]
}

func GuardCorrectionRows(rows []CorrectedRow, batch CorrectionBatch, neighbors []documents.Segment) []CorrectedRow {
	result := append([]CorrectedRow(nil), rows...)
	for i := range result {
		reason := CorrectionFidelityReason(batch.Rows[i].Text, result[i].Text, neighbors)
		if reason != "" {
			result[i].Text = batch.Rows[i].Text
			result[i].Uncertain = true
			result[i].Reason = reason
		} else if result[i].Uncertain {
			result[i].Text = batch.Rows[i].Text
		}
	}
	return result
}

// GuardCorrectedSegments rechecks historical cache/checkpoint payloads locally,
// without changing recipe identity or causing a provider request.
func GuardCorrectedSegments(rows []CorrectedSegment, source []documents.Segment) ([]CorrectedSegment, error) {
	if !ValidCorrectionPrefix(rows, source) {
		return nil, fmt.Errorf("校订结果与识别原文或时间不一致，未保存或重放")
	}
	result := append([]CorrectedSegment{}, rows...)
	for i := range result {
		reason := CorrectionFidelityReason(source[i].Text, result[i].Text, source)
		if reason != "" {
			result[i].Text = source[i].Text
			result[i].Uncertain = true
			result[i].Reason = reason
		} else if result[i].Uncertain {
			result[i].Text = source[i].Text
		}
	}
	return result, nil
}
