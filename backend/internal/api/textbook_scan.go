package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/textbook"
)

type textbookScanWarning struct {
	Page            int  `json:"page"`
	Evidence        int  `json:"evidence"`
	Coordinates     int  `json:"coordinates"`
	Invalid         int  `json:"invalid"`
	SkippedContents bool `json:"-"`
}

func (w textbookScanWarning) count() int { return w.Evidence + w.Coordinates + w.Invalid }

func (w textbookScanWarning) describe() string {
	var reasons []string
	if w.Evidence > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 条找不到对应的课堂原文依据", w.Evidence))
	}
	if w.Coordinates > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 条选区坐标无效", w.Coordinates))
	}
	if w.Invalid > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 条建议格式无效", w.Invalid))
	}
	return fmt.Sprintf("第 %d 页：%s", w.Page, strings.Join(reasons, "，"))
}

// Current vision models use a 0–1000 integer grid. Older responses can use 0–1 ratios.
func parseTextbookRect(raw json.RawMessage) ([4]float64, bool, bool) {
	var rect [4]float64
	var coords []*float64
	if json.Unmarshal(raw, &coords) != nil || len(coords) != len(rect) {
		return rect, false, false
	}
	grid := false
	for i, value := range coords {
		if value == nil {
			return rect, false, false
		}
		n := *value
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1000 {
			return rect, false, false
		}
		grid = grid || n > 1
		rect[i] = n
	}
	return rect, grid, true
}

func scaleTextbookRect(rect [4]float64, grid bool) ([4]float64, bool) {
	for i, n := range rect {
		if grid {
			if n != math.Trunc(n) {
				return rect, false
			}
			n /= 1000
		}
		rect[i] = n
	}
	return rect, rect[2] > rect[0] && rect[3] > rect[1]
}

func normalizedTextbookRect(raw json.RawMessage) ([4]float64, bool) {
	rect, grid, ok := parseTextbookRect(raw)
	if !ok {
		return rect, false
	}
	return scaleTextbookRect(rect, grid)
}

type scanDecision struct {
	Rect json.RawMessage `json:"rect"`
	textbookSuggestion
}

func reviewScannedProposal(
	proposal json.RawMessage,
	page int,
	placement scanPlacement,
	sources textbookSources,
	catalog []textbookEvidence,
	warning *textbookScanWarning,
) (textbook.Highlight, bool) {
	var decision scanDecision
	if err := json.Unmarshal(proposal, &decision); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && (typeErr.Field == "evidenceId" || typeErr.Field == "source" || typeErr.Field == "evidence") {
			warning.Evidence++
		} else {
			warning.Invalid++
		}
		return textbook.Highlight{}, false
	}
	quote := strings.TrimSpace(decision.Quote)
	if quote == "" || len([]rune(quote)) > 3000 {
		warning.Invalid++
		return textbook.Highlight{}, false
	}
	if textbookContentsHeading(quote) || textbookContentsEntry(quote) || textbookBareTitle(quote) {
		return textbook.Highlight{}, false
	}
	rect, ok := normalizedTextbookRect(decision.Rect)
	if !ok {
		warning.Coordinates++
		return textbook.Highlight{}, false
	}
	row, ok := decision.annotation(sources, catalog)
	if !ok {
		warning.Evidence++
		return textbook.Highlight{}, false
	}
	position, aligned := placement.locate(rect)
	if !aligned {
		warning.Coordinates++
		return textbook.Highlight{}, false
	}
	row.ID, row.Page, row.Rect = ids.New(), page, position
	return row, true
}

func reviewScannedSuggestions(raw []byte, page int, placement scanPlacement, sources textbookSources, catalog []textbookEvidence) ([]textbook.Highlight, textbookScanWarning, error) {
	warning := textbookScanWarning{Page: page}
	if textbookContentsReply(raw) {
		warning.SkippedContents = true
		return nil, warning, nil
	}
	var proposals []json.RawMessage
	if err := json.Unmarshal(raw, &proposals); err != nil || proposals == nil || len(proposals) > 20 {
		return nil, warning, fmt.Errorf("第 %d 页的识别结果不是有效的建议列表", page)
	}
	var rows []textbook.Highlight
	for _, proposal := range proposals {
		if row, ok := reviewScannedProposal(proposal, page, placement, sources, catalog, &warning); ok {
			rows = append(rows, row)
		}
	}
	return rows, warning, nil
}

type scannedTextbookPage struct {
	client  *ai.Client
	chapter string
	sources textbookSources
	catalog []textbookEvidence
	image   string
	page    int
	affine  [6]float64
}

func analyzeScannedTextbookPage(ctx context.Context, input scannedTextbookPage) ([]textbook.Highlight, textbookScanWarning, error) {
	var warning textbookScanWarning
	placement, ok := newScanPlacement(input.image, input.affine)
	if !ok {
		return nil, warning, contract.InputInvalid(fmt.Sprintf("第 %d 页的图像或页面坐标无法读取", input.page))
	}
	options := textbookModelOptions(input.client, input.client.Profile.VisionModel)
	options.Images = []ai.Image{{Label: "教材原页", URL: input.image}}
	var rows []textbook.Highlight
	rejected := errors.New("本页建议未通过核验")
	_, err := input.client.JSONWithOptions(ctx,
		textbookBodyPrompt+textbookNotePrompt+"正文只输出 JSON 数组，每条含 rect、quote、explanation、extension，以及 evidenceId 或 source=ai。quote 必须逐字摘自框内的一行教材。rect 为图片左上原点的四个 0–1000 整数，依次为左、上、右、下，紧贴这一行。每页最多 15 条。",
		struct {
			Chapter  string             `json:"chapter"`
			Evidence []textbookEvidence `json:"evidence"`
		}{input.chapter, input.catalog},
		func(raw []byte) error {
			current, issues, parseErr := reviewScannedSuggestions(raw, input.page, placement, input.sources, input.catalog)
			if parseErr != nil {
				return parseErr
			}
			if len(current) == 0 && issues.count() > 0 {
				warning = issues
				return rejected // Allow one correction before reporting this page's rejected suggestions.
			}
			if len(current) > 0 || issues.SkippedContents || warning.count() == 0 {
				warning = issues
			}
			rows = current
			return nil
		}, options)
	if err != nil && !errors.Is(err, rejected) {
		return nil, warning, err
	}
	return rows, warning, nil
}

func (inv *Invoker) finishTextbookDraft(bookID, hash string, rows []textbook.Highlight, pages []textbookScanWarning) (any, error) {
	var warnings []textbookScanWarning
	var skipped []int
	for _, page := range pages {
		if page.SkippedContents {
			skipped = append(skipped, page.Page)
		}
		if page.count() > 0 {
			warnings = append(warnings, page)
		}
	}
	if len(rows) == 0 && len(warnings) != 0 {
		var details []string
		for _, w := range warnings[:min(3, len(warnings))] {
			details = append(details, w.describe())
		}
		if len(warnings) > len(details) {
			details = append(details, fmt.Sprintf("另有 %d 页未通过核验", len(warnings)-len(details)))
		}
		return nil, contract.InputInvalid(strings.Join(details, "；") + "。本次没有可保存的建议。")
	}
	id, displayed := inv.textbook.AddDraft(bookID, hash, rows)
	result := map[string]any{"id": id, "rows": displayed}
	if len(skipped) > 0 {
		result["skippedContentsPages"] = skipped
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}
