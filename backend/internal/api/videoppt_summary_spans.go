package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func spanSummaryPrompt(mode string, maxPoints int) string {
	prompt := strings.ReplaceAll(summaryPrompt(mode, maxPoints), "仅返回 JSON 数组，每项为", "cards 数组每项为")
	prompt = strings.ReplaceAll(prompt, "没有有用内容时返回 []。", "")
	return prompt + " 仅返回对象 {processing:\"complete\"或\"insufficient\",cards:[上述要点]}。每条text是coreStartCodepoint到coreEndCodepoint的核心正文，按Unicode码点计数；必须处理本批全部核心区间。无有用内容但已处理可返回complete和空cards；原文不足以判断时返回insufficient。引文必须是给定text中的连续原话，不得跨核心区间拼接。startMs/endMs是父字幕完整时段，不是子句精确对齐。"
}

func summarizeSpanVideoText(ctx context.Context, client *ai.Client, srt, mode string, progress func(string)) (summaryResult, error) {
	heading, ok := videoNoteTitles[mode]
	if !ok {
		return summaryResult{}, contract.InputInvalid(errSummaryModeInvalid)
	}
	segments, err := documents.ParseSrt(srt)
	if err != nil || len(segments) < 1 || len(segments) > 20000 {
		return summaryResult{}, contract.InputInvalid("字幕数量或格式无效")
	}
	chunks, err := summaryinput.PlanSpans(mode, segments, summaryChunkLimit)
	if err != nil {
		return summaryResult{}, err
	}
	lines := []string{"# " + heading, "", "以下内容根据字幕生成。区间按Unicode码点处理；时间沿用父字幕，未推算子句精确时间。", ""}
	allPoints := []videoNotePoint{}
	partial := false
	metadata, observer := contract.GenerationObservationFrom(ctx)
	for index, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return summaryResult{}, err
		}
		if progress != nil {
			progress(fmt.Sprintf("正在处理字幕核心区间 %d/%d", index+1, len(chunks)))
		}
		m := metadata
		m.ChunkIndex = index
		m.ChunkCount = len(chunks)
		m.CoreSpans = chunk.CoreSpans
		m.CueIDs = make([]int, len(chunk.Input.Cues))
		for i, cue := range chunk.Input.Cues {
			m.CueIDs[i] = cue.Cue
		}
		m.InputSHA256, err = summaryinput.SpanDigest(chunk.Input)
		if err != nil {
			return summaryResult{}, err
		}
		chunkContext := contract.WithGenerationObservation(ctx, m, observer)
		var points []videoNotePoint
		processing := ""
		_, err = client.JSON(chunkContext, spanSummaryPrompt(mode, summaryMaxPoints(mode)), chunk.Input, func(body []byte) error {
			var response struct {
				Processing string          `json:"processing"`
				Cards      json.RawMessage `json:"cards"`
			}
			if json.Unmarshal(body, &response) != nil || (response.Processing != "complete" && response.Processing != "insufficient") || len(response.Cards) == 0 || strings.TrimSpace(string(response.Cards))[0] != '[' {
				return fmt.Errorf("摘要必须返回明确处理状态与cards数组")
			}
			parsed, err := parseSummaryPoints(response.Cards, summaryMaxPoints(mode), heading)
			if err != nil {
				return err
			}
			for _, point := range parsed {
				valid := false
				for _, cue := range chunk.Input.Cues {
					if point.Cue == cue.Cue && strings.Contains(cue.Text, point.Quote) {
						valid = true
						break
					}
				}
				if !valid {
					return fmt.Errorf("摘要引文不属于本批核心区间")
				}
			}
			part := make([]documents.Segment, len(chunk.Input.Cues))
			for i, cue := range chunk.Input.Cues {
				part[i] = documents.Segment{Start: cue.StartMs, End: cue.EndMs, Text: cue.Text}
			}
			points = verifySummaryPoints(parsed, part, chunk.Input.Cues[0].Cue, segments, heading)
			if len(points) != len(parsed) {
				return fmt.Errorf("摘要引文验证失败")
			}
			processing = response.Processing
			return nil
		})
		if err != nil {
			return summaryResult{}, err
		}
		receipt, err := contract.ReadGenerationReceipt(chunkContext)
		if err != nil {
			return summaryResult{}, err
		}
		if receipt.Ref.Version != 3 || receipt.Event.State != "format-result" || receipt.Event.FormatValid == nil || !*receipt.Event.FormatValid {
			return summaryResult{}, contract.ObservationFailure(fmt.Errorf("摘要处理回执缺少格式凭证"))
		}
		event := receipt.Event
		event.State = "validated-points"
		pointHash, err := contract.SummaryPointsDigest(canonicalVideoPoints(points))
		if err != nil {
			return summaryResult{}, err
		}
		event.ValidatedPoints = &contract.ValidatedPointEvidence{AcceptedFormatRef: receipt.Ref, PointsSHA256: pointHash, PointCount: len(points), Processing: &contract.ChunkProcessingReceipt{State: processing, CoreSpanSHA256: contract.CoreSpansDigest(chunk.CoreSpans)}}
		if err := observer(chunkContext, event); err != nil {
			return summaryResult{}, contract.ObservationFailure(err)
		}
		partial = partial || processing == "insufficient"
		part := make([]documents.Segment, len(chunk.Input.Cues))
		for i, cue := range chunk.Input.Cues {
			part[i] = documents.Segment{Start: cue.StartMs, End: cue.EndMs, Text: cue.Text}
		}
		var section []videoNotePoint
		lines, section = appendSummarySection(lines, part, points)
		allPoints = append(allPoints, section...)
	}
	if len(allPoints) == 0 {
		lines = append(lines, "已处理全部核心区间，但没有可核实的要点。")
	}
	if partial {
		lines = append([]string{"> 部分结果：存在原文不足的处理区间；处理覆盖不等于语义完整。", ""}, lines...)
	}
	receipt, err := contract.ReadGenerationReceipt(ctx)
	if err != nil {
		return summaryResult{}, err
	}
	hash, err := contract.SummaryPointsDigest(canonicalVideoPoints(allPoints))
	if err != nil {
		return summaryResult{}, err
	}
	return summaryResult{Markdown: strings.Join(lines, "\n"), Points: allPoints, Evidence: &contract.SummaryEvidence{Version: 1, Partial: partial, SourceSHA256: metadata.SourceSHA256, ItemRecipeHash: metadata.ItemRecipeSHA256, FinalPointsSHA256: hash, PointCount: len(allPoints), ValidatedHead: receipt.Ref}}, nil
}
