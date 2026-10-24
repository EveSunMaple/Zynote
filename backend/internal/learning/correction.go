package learning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

type CorrectionMode string

const (
	CorrectionQuality  CorrectionMode = "quality"
	CorrectionBalanced CorrectionMode = "balanced"
	CorrectionText     CorrectionMode = "text"
)

func ParseCorrectionMode(value string) (CorrectionMode, bool) {
	switch CorrectionMode(value) {
	case CorrectionQuality, CorrectionBalanced, CorrectionText:
		return CorrectionMode(value), true
	}
	return CorrectionQuality, false
}

func (mode CorrectionMode) Label() string {
	switch mode {
	case CorrectionBalanced:
		return "均衡"
	case CorrectionText:
		return "快速文字"
	}
	return "视觉优先"
}

type CorrectionBatch struct {
	Start int
	Rows  []documents.Segment
}

type AdjacentRow struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type CoursewareImage struct {
	Label string
	URL   string
}

type CorrectionContext struct {
	Courseware     string
	LectureContext []AdjacentRow
	Images         []CoursewareImage
	Omissions      []string
}

type CorrectedRow struct {
	Text      string
	Uncertain bool
	Reason    string
}

type CorrectedSegment struct {
	Start        int64  `json:"start"`
	End          int64  `json:"end"`
	Text         string `json:"text"`
	OriginalText string `json:"originalText"`
	Uncertain    bool   `json:"uncertain"`
	Reason       string `json:"reason"`
}

type CorrectionCheckpoint struct {
	Fingerprint string
	Total       int
	Segments    []CorrectedSegment
}

func encodeJSONValue(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte("null")
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
}

func jsonQuoted(value string) string {
	return string(encodeJSONValue(value))
}

func CorrectionFingerprint(
	connection string,
	segments []documents.Segment,
	prompt string,
	pptData []byte,
	selectedText string,
	limitations []string,
	mode CorrectionMode,
) string {
	hash := sha256.New()
	hash.Write(encodeJSONValue(struct {
		Engine     string              `json:"engine"`
		Connection string              `json:"connection"`
		Segments   []documents.Segment `json:"segments"`
		Prompt     string              `json:"prompt"`
	}{Engine: "qwen", Connection: connection, Segments: segments, Prompt: prompt}))
	hash.Write(pptData)
	hash.Write(encodeJSONValue(struct {
		CoursewareText string   `json:"coursewareText"`
		Limitations    []string `json:"limitations"`
	}{CoursewareText: selectedText, Limitations: limitations}))
	if mode != CorrectionQuality {
		hash.Write(encodeJSONValue(struct {
			CorrectionMode CorrectionMode `json:"correctionMode"`
		}{CorrectionMode: mode}))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func utf16Len(value string) int {
	length := 0
	for _, char := range value {
		if char > 0xFFFF {
			length += 2
		} else {
			length++
		}
	}
	return length
}

func sliceUTF16(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	length := 0
	for index, char := range value {
		width := 1
		if char > 0xFFFF {
			width = 2
		}
		if length+width > limit {
			return value[:index]
		}
		length += width
	}
	return value
}

func PlanCorrection(segments []documents.Segment, start, outputTokens int) ([]CorrectionBatch, error) {
	if outputTokens <= 0 {
		outputTokens = 8192
	}
	budget := int(math.Floor(float64(outputTokens) * 0.8))
	batches := []CorrectionBatch{}
	for index := start; index < len(segments); {
		batch := CorrectionBatch{Start: index}
		output := 2
		input := 0
		for index < len(segments) && len(batch.Rows) < 96 {
			quoted := jsonQuoted(segments[index].Text)
			chars := utf16Len(quoted)
			if chars > 6000 {
				return nil, fmt.Errorf(
					"第 %d 条原文超过 6000 字符单批输入预算，请拆分过长原文后重试；原文未截断", index+1,
				)
			}
			estimate := int(math.Ceil(float64(len(quoted))/2)) + 96
			if len(batch.Rows) > 0 && (output+estimate > budget || input+chars > 6000) {
				break
			}
			batch.Rows = append(batch.Rows, segments[index])
			input += chars
			output += estimate
			index++
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

func ValidateCorrectionRows(value []byte, batch CorrectionBatch) ([]CorrectedRow, error) {
	failure := fmt.Errorf("校订结果缺少原文或次序变化，已拒绝保存")
	var rows []struct {
		ID        *int    `json:"id"`
		Text      *string `json:"text"`
		Uncertain *bool   `json:"uncertain"`
		Reason    *string `json:"reason"`
	}
	if err := json.Unmarshal(value, &rows); err != nil {
		return nil, failure
	}
	if len(rows) != len(batch.Rows) {
		return nil, failure
	}
	result := make([]CorrectedRow, 0, len(rows))
	for index, row := range rows {
		if row.ID == nil || *row.ID != batch.Start+index || row.Text == nil ||
			strings.TrimSpace(*row.Text) == "" || row.Uncertain == nil || row.Reason == nil {
			return nil, failure
		}
		result = append(result, CorrectedRow{Text: *row.Text, Uncertain: *row.Uncertain, Reason: *row.Reason})
	}
	return result, nil
}

func ParseCorrectedSegments(raw string) ([]CorrectedSegment, error) {
	var items []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	result := make([]CorrectedSegment, 0, len(items))
	for _, item := range items {
		var segment CorrectedSegment
		for key, target := range map[string]any{
			"start":        &segment.Start,
			"end":          &segment.End,
			"text":         &segment.Text,
			"originalText": &segment.OriginalText,
			"uncertain":    &segment.Uncertain,
			"reason":       &segment.Reason,
		} {
			value, ok := item[key]
			if !ok || json.Unmarshal(value, target) != nil {
				return nil, fmt.Errorf("校订检查点数据无效")
			}
		}
		result = append(result, segment)
	}
	return result, nil
}

func ValidCorrectionPrefix(saved []CorrectedSegment, source []documents.Segment) bool {
	if len(saved) > len(source) {
		return false
	}
	for index, row := range saved {
		if strings.TrimSpace(row.Text) == "" ||
			row.OriginalText != source[index].Text ||
			row.Start != source[index].Start ||
			row.End != source[index].End {
			return false
		}
	}
	return true
}

func ResumableCorrection(checkpoint *CorrectionCheckpoint, fingerprint string, source []documents.Segment) []CorrectedSegment {
	if checkpoint == nil || checkpoint.Fingerprint != fingerprint ||
		checkpoint.Total != len(source) || !ValidCorrectionPrefix(checkpoint.Segments, source) {
		return nil
	}
	if checkpoint.Segments == nil {
		return []CorrectedSegment{}
	}
	return checkpoint.Segments
}

func CorrectionNeedsVision(contexts []CorrectionContext, visionModel string) bool {
	if strings.TrimSpace(visionModel) != "" {
		return false
	}
	for _, context := range contexts {
		if len(context.Images) > 0 {
			return true
		}
	}
	return false
}

type page struct {
	Number int
	Text   string
	Index  int
	Score  int
	Tokens map[string]bool
}

var pageHeaderPattern = regexp.MustCompile(`\[第\s*(\d+)\s*页\]\n`)
var imagePagePattern = regexp.MustCompile(`第\s*(\d+)\s*页`)

func whitespaceByte(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

func slicePages(text string) []string {
	matches := pageHeaderPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		pages := []string{}
		for offset := 0; offset < len(text); {
			chunk := sliceUTF16(text[offset:], 2000)
			if chunk == "" {
				break
			}
			pages = append(pages, chunk)
			offset += len(chunk)
		}
		return pages
	}
	pages := make([]string, 0, len(matches))
	for index, match := range matches {
		start := match[0]
		end := len(text)
		if index+1 < len(matches) {
			next := matches[index+1][0]
			end = next
			position := next
			for position > start && whitespaceByte(text[position-1]) {
				position--
			}
			for position < next && whitespaceByte(text[position]) {
				if text[position] == '\n' {
					end = position
					break
				}
				position++
			}
		}
		pages = append(pages, text[start:end])
	}
	return pages
}

func grams(value string) map[string]bool {
	tokens := map[string]bool{}
	var words []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, char := range strings.ToLower(value) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || isHan(char) {
			current.WriteRune(char)
		} else {
			flush()
		}
	}
	flush()
	for _, word := range words {
		runes := []rune(word)
		han := false
		for _, char := range runes {
			if isHan(char) {
				han = true
				break
			}
		}
		if han {
			for _, size := range []int{2, 3} {
				for index := 0; index+size <= len(runes); index++ {
					tokens[string(runes[index:index+size])] = true
				}
			}
		} else if len(runes) > 1 {
			tokens[word] = true
		}
	}
	return tokens
}

func isHan(char rune) bool {
	return (char >= 0x4E00 && char <= 0x9FFF) || (char >= 0x3400 && char <= 0x4DBF) ||
		(char >= 0xF900 && char <= 0xFAFF) || (char >= 0x20000 && char <= 0x2FA1F)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func CorrectionContextFor(ppt string, images []CoursewareImage) func(CorrectionBatch, []documents.Segment, CorrectionMode) CorrectionContext {
	texts := slicePages(ppt)
	pages := make([]page, 0, len(texts))
	for index, text := range texts {
		number := index + 1
		if match := pageHeaderPattern.FindStringSubmatch(text); match != nil {
			fmt.Sscanf(match[1], "%d", &number)
		}
		pages = append(pages, page{Number: number, Text: text, Index: index, Tokens: grams(text)})
	}
	return func(batch CorrectionBatch, source []documents.Segment, mode CorrectionMode) CorrectionContext {
		quality := mode == CorrectionQuality
		limit := 8000
		remaining := 1200
		distanceLimit := 6
		if !quality {
			limit = 4000
			remaining = 800
			distanceLimit = 4
		}
		adjacent := []AdjacentRow{}
		for distance := 1; distance <= distanceLimit; distance++ {
			for _, id := range []int{batch.Start - distance, batch.Start + len(batch.Rows) - 1 + distance} {
				if id < 0 || id >= len(source) || remaining <= 0 {
					continue
				}
				text := sliceUTF16(source[id].Text, remaining)
				adjacent = append(adjacent, AdjacentRow{ID: id, Text: text})
				remaining -= utf16Len(text)
			}
		}
		sort.Slice(adjacent, func(left, right int) bool { return adjacent[left].ID < adjacent[right].ID })
		queryText := []string{}
		for _, row := range batch.Rows {
			queryText = append(queryText, row.Text)
		}
		for _, row := range adjacent {
			queryText = append(queryText, row.Text)
		}
		query := grams(strings.Join(queryText, "\n"))
		ranked := append([]page(nil), pages...)
		for index := range ranked {
			score := 0
			for token := range ranked[index].Tokens {
				if query[token] {
					score++
				}
			}
			ranked[index].Score = score
		}
		sort.SliceStable(ranked, func(left, right int) bool {
			if ranked[left].Score != ranked[right].Score {
				return ranked[left].Score > ranked[right].Score
			}
			return ranked[left].Index < ranked[right].Index
		})
		selected := []page{}
		matched := false
		for _, item := range ranked {
			if item.Score > 0 {
				matched = true
				selected = append(selected, item)
			}
		}
		center := 0
		if len(pages) > 0 {
			center = int(math.Floor(float64(batch.Start) / float64(maxInt(1, len(source))) * float64(len(pages))))
			if center >= len(pages) {
				center = len(pages) - 1
			}
		}
		if !matched && len(pages) > 0 {
			from := maxInt(0, center-1)
			to := minInt(len(pages), center+2)
			selected = append(selected, pages[from:to]...)
		}
		parts := make([]string, 0, len(selected))
		for _, item := range selected {
			parts = append(parts, item.Text)
		}
		text := sliceUTF16(strings.Join(parts, "\n"), limit)
		omissions := []string{}
		if len(text) < len(ppt) {
			omissions = append(omissions, fmt.Sprintf("课件文字按本批相关性择取，最多 %d 字；未发送整份课件。", limit))
		}
		if !matched && len(pages) > 0 {
			omissions = append(omissions, "未找到课件文字匹配，按字幕进度选取候选页面，不能视为可靠对齐。")
		}
		type scoredImage struct {
			image CoursewareImage
			index int
			score int
		}
		scored := make([]scoredImage, 0, len(images))
		for index, image := range images {
			score := 0
			if match := imagePagePattern.FindStringSubmatch(image.Label); match != nil {
				number := 0
				fmt.Sscanf(match[1], "%d", &number)
				for _, item := range ranked {
					if item.Number == number {
						score = item.Score
						break
					}
				}
			}
			scored = append(scored, scoredImage{image: image, index: index, score: score})
		}
		sort.SliceStable(scored, func(left, right int) bool {
			if scored[left].score != scored[right].score {
				return scored[left].score > scored[right].score
			}
			return scored[left].index < scored[right].index
		})
		candidates := []scoredImage{}
		for _, item := range scored {
			if item.score > 0 {
				candidates = append(candidates, item)
			}
			if len(candidates) >= 3 && quality {
				break
			}
			if len(candidates) >= 1 && !quality {
				break
			}
		}
		if quality && len(candidates) == 0 && len(images) > 0 {
			imageCenter := int(math.Floor(float64(batch.Start) / float64(maxInt(1, len(source))) * float64(len(images))))
			if imageCenter >= len(images) {
				imageCenter = len(images) - 1
			}
			from := maxInt(0, imageCenter-1)
			to := minInt(len(scored), imageCenter+2)
			candidates = append(candidates, scored[from:to]...)
			if len(candidates) > 0 {
				omissions = append(omissions, "附图按进度猜选，未经可靠页面对齐。")
			}
		}
		if mode == CorrectionText {
			candidates = nil
		}
		if mode != CorrectionQuality {
			if mode == CorrectionText {
				omissions = append(omissions, "快速文字模式未附图片，图表与扫描页不可猜测。")
			} else {
				omissions = append(omissions, "均衡模式只附文字匹配图片，未匹配图表不可猜测。")
			}
		}
		sort.Slice(candidates, func(left, right int) bool { return candidates[left].index < candidates[right].index })
		chosen := []CoursewareImage{}
		bytesUsed := 0
		for _, item := range candidates {
			size := len([]byte(item.image.URL))
			if size > 900000 || size+bytesUsed > 1800000 {
				omissions = append(omissions, item.image.Label+" 图片超出请求字节预算，未附加。")
				continue
			}
			bytesUsed += size
			chosen = append(chosen, item.image)
		}
		return CorrectionContext{
			Courseware: text, LectureContext: adjacent, Images: chosen,
			Omissions: uniqueStrings(omissions),
		}
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func RunCorrectionBatches[T any](
	ctx context.Context,
	batches []CorrectionBatch,
	run func(context.Context, CorrectionBatch, int) (T, error),
	commit func(T, CorrectionBatch) error,
) error {
	for start := 0; start < len(batches); start += 2 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunkContext, cancel := context.WithCancel(ctx)
		var mutex sync.Mutex
		pending := map[int]T{}
		next := start
		var firstErr error
		var once sync.Once
		var wait sync.WaitGroup
		for offset := 0; offset < 2 && start+offset < len(batches); offset++ {
			index := start + offset
			batch := batches[index]
			wait.Add(1)
			go func() {
				defer wait.Done()
				result, err := run(chunkContext, batch, index)
				if err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				mutex.Lock()
				defer mutex.Unlock()
				pending[index] = result
				for {
					ready, ok := pending[next]
					if !ok {
						break
					}
					if err := commit(ready, batches[next]); err != nil {
						once.Do(func() {
							firstErr = err
							cancel()
						})
						return
					}
					delete(pending, next)
					next++
				}
			}()
		}
		wait.Wait()
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		if firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func CorrectedSegmentsToDocuments(segments []CorrectedSegment) []documents.Segment {
	result := make([]documents.Segment, 0, len(segments))
	for _, segment := range segments {
		result = append(result, documents.Segment{Start: segment.Start, End: segment.End, Text: segment.Text})
	}
	return result
}
