package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/orderedwork"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
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
	tree := contract.SummaryTreeFrom(ctx)
	var treeEvidence contract.SummaryTreeEvidence
	globalRoot, globalNote := "", ""
	var globalStats summaryglobal.Stats
	const maxSummaryOutputBytes = 16 << 20
	outputBytes := 0
	chunkPointCounts := make([]int, len(chunks))
	metadata, observer := contract.GenerationObservationFrom(ctx)
	type chunkResult struct {
		points     []videoNotePoint
		processing string
	}
	type cachedChunk struct {
		Version    int
		Points     []videoNotePoint
		Processing string
		Receipt    contract.GenerationEventRef
	}
	cache := contract.SuccessCacheFrom(ctx)
	err = orderedwork.Run(ctx, len(chunks), func(ctx context.Context, index int) (chunkResult, error) {
		chunk := chunks[index]
		if err := ctx.Err(); err != nil {
			return chunkResult{}, err
		}
		m := metadata
		m.ChunkIndex = index
		m.ChunkCount = len(chunks)
		m.CoreSpans = chunk.CoreSpans
		m.CueIDs = make([]int, len(chunk.Input.Cues))
		for i, cue := range chunk.Input.Cues {
			m.CueIDs[i] = cue.Cue
		}
		inputHash, err := summaryinput.SpanDigest(chunk.Input)
		m.InputSHA256 = inputHash
		if err != nil {
			return chunkResult{}, err
		}
		chunkContext := contract.WithGenerationObservation(ctx, m, observer)
		key := fmt.Sprintf("summary:%d:%d", metadata.ItemIndex, index)
		identity := ""
		if cache != nil {
			dm := m
			dm.AttemptIndex = 0
			empty := sha256.Sum256(nil)
			dm.FeedbackSHA256 = hex.EncodeToString(empty[:])
			wire, err := client.JSONRequestDigest(contract.WithGenerationObservation(ctx, dm, observer), spanSummaryPrompt(mode, summaryMaxPoints(mode)), chunk.Input, ai.JSONOptions{})
			if err != nil {
				return chunkResult{}, err
			}
			body, _ := json.Marshal(struct {
				Version int
				Scope   contract.GenerationObservation
				Wire    string
				Policy  any
			}{1, m, wire, ai.JSONRecipePolicy()})
			hash := sha256.Sum256(body)
			identity = hex.EncodeToString(hash[:])
			raw, found, err := cache.Adopt(chunkContext, key, identity)
			if err != nil {
				return chunkResult{}, err
			}
			if found {
				var saved cachedChunk
				if json.Unmarshal(raw, &saved) != nil || saved.Version != 1 {
					return chunkResult{}, contract.ObservationFailure(fmt.Errorf("摘要成功缓存无效"))
				}
				receipt, err := contract.ReadGenerationReceipt(chunkContext)
				if err != nil {
					return chunkResult{}, err
				}
				pointHash, err := contract.SummaryPointsDigest(canonicalVideoPoints(saved.Points))
				if err != nil || len(saved.Points) > summaryMaxPoints(mode) || (saved.Processing != "complete" && saved.Processing != "insufficient") {
					return chunkResult{}, contract.ObservationFailure(fmt.Errorf("摘要缓存结构无效"))
				}
				for _, point := range saved.Points {
					valid := false
					for _, cue := range chunk.Input.Cues {
						if point.Cue == cue.Cue && strings.Contains(cue.Text, point.Quote) && len([]rune(point.Quote)) >= 2 {
							valid = true
							break
						}
					}
					if !valid {
						return chunkResult{}, contract.ObservationFailure(fmt.Errorf("摘要缓存与核心原文不一致"))
					}
				}
				if receipt.Event.State == "format-result" && receipt.Ref == saved.Receipt && receipt.Event.FormatValid != nil && *receipt.Event.FormatValid {
					event := receipt.Event
					event.State = "validated-points"
					event.ValidatedPoints = &contract.ValidatedPointEvidence{AcceptedFormatRef: receipt.Ref, PointsSHA256: pointHash, PointCount: len(saved.Points), Processing: &contract.ChunkProcessingReceipt{State: saved.Processing, CoreSpanSHA256: contract.CoreSpansDigest(chunk.CoreSpans)}}
					if err := observer(chunkContext, event); err != nil {
						return chunkResult{}, contract.ObservationFailure(err)
					}
					receipt, err = contract.ReadGenerationReceipt(chunkContext)
					if err != nil {
						return chunkResult{}, err
					}
				}
				evidence := receipt.Event.ValidatedPoints
				if receipt.Event.State != "validated-points" || evidence == nil || evidence.AcceptedFormatRef != saved.Receipt || evidence.Processing == nil || evidence.PointsSHA256 != pointHash || evidence.PointCount != len(saved.Points) || evidence.Processing.State != saved.Processing || evidence.Processing.CoreSpanSHA256 != contract.CoreSpansDigest(chunk.CoreSpans) {
					return chunkResult{}, contract.ObservationFailure(fmt.Errorf("摘要缓存与原处理回执不一致"))
				}
				return chunkResult{saved.Points, saved.Processing}, nil
			}
		}
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
			return chunkResult{}, err
		}
		// A locally complete validated response survives sibling cancellation.
		// The finite generation/epoch fence still owns every evidence/cache write.
		localContext, finishLocal := context.WithTimeout(context.WithoutCancel(chunkContext), 5*time.Second)
		defer finishLocal()
		receipt, err := contract.ReadGenerationReceipt(localContext)
		if err != nil {
			return chunkResult{}, err
		}
		if receipt.Ref.Version != 3 || receipt.Event.State != "format-result" || receipt.Event.FormatValid == nil || !*receipt.Event.FormatValid {
			return chunkResult{}, contract.ObservationFailure(fmt.Errorf("摘要处理回执缺少格式凭证"))
		}
		event := receipt.Event
		event.State = "validated-points"
		pointHash, err := contract.SummaryPointsDigest(canonicalVideoPoints(points))
		if err != nil {
			return chunkResult{}, err
		}
		event.ValidatedPoints = &contract.ValidatedPointEvidence{AcceptedFormatRef: receipt.Ref, PointsSHA256: pointHash, PointCount: len(points), Processing: &contract.ChunkProcessingReceipt{State: processing, CoreSpanSHA256: contract.CoreSpansDigest(chunk.CoreSpans)}}
		// Persist the locally validated payload before linking the validated
		// event. A crash at either boundary can reuse the original format ref.
		if cache != nil {
			body, err := json.Marshal(cachedChunk{1, points, processing, receipt.Ref})
			if err != nil {
				return chunkResult{}, err
			}
			if err := cache.Save(localContext, key, identity, body); err != nil {
				return chunkResult{}, err
			}
		}
		if err := observer(localContext, event); err != nil {
			return chunkResult{}, contract.ObservationFailure(err)
		}
		return chunkResult{points, processing}, nil
	}, func(result chunkResult, index int) error {
		chunk := chunks[index]
		points, processing := result.points, result.processing
		chunkPointCounts[index] = len(points)
		if progress != nil {
			progress(fmt.Sprintf("已处理字幕核心区间 %d/%d", index+1, len(chunks)))
		}
		partial = partial || processing == "insufficient"
		part := make([]documents.Segment, len(chunk.Input.Cues))
		for i, cue := range chunk.Input.Cues {
			part[i] = documents.Segment{Start: cue.StartMs, End: cue.EndMs, Text: cue.Text}
		}
		body, _ := json.Marshal(points)
		outputBytes += len(body)
		if outputBytes > maxSummaryOutputBytes {
			return fmt.Errorf("摘要成果超过有界输出预算，未截断保存")
		}
		if tree != nil {
			if err := tree.Append(ctx, index, canonicalVideoPoints(points)); err != nil {
				return err
			}
		}
		var section []videoNotePoint
		lines, section = appendSummarySection(lines, part, points)
		if tree == nil {
			allPoints = append(allPoints, section...)
		}
		return nil
	})
	if err != nil {
		return summaryResult{}, err
	}
	if tree != nil {
		treeEvidence, err = tree.Finish(ctx, func(point contract.ValidatedSummaryPoint) error {
			allPoints = append(allPoints, videoNotePoint{Cue: point.Cue, Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp})
			return nil
		})
		if err != nil {
			return summaryResult{}, err
		}
	}

	if contract.SummaryStagesFrom(ctx) != nil {
		globalRoot, globalNote, globalStats, err = organizeSummaryTree(ctx, client, treeEvidence.RootSHA256, chunks)
		if err != nil {
			return summaryResult{}, err
		}
		partial = partial || globalStats.Partial
		if len(globalStats.OrderedReviews) != len(allPoints) {
			return summaryResult{}, fmt.Errorf("风险复核投影数量无效")
		}
		for i := range allPoints {
			allPoints[i].ReviewState = globalStats.OrderedReviews[i]
		}
		lines = []string{"# " + heading, "", "以下原始要点逐项标明核验状态；未复核、不确定、不支持和省略项只作证据供核对，不作为已确认结论。", ""}
		offset := 0
		for index, chunk := range chunks {
			part := make([]documents.Segment, len(chunk.Input.Cues))
			for i, cue := range chunk.Input.Cues {
				part[i] = documents.Segment{Start: cue.StartMs, End: cue.EndMs, Text: cue.Text}
			}
			lines, _ = appendSummarySection(lines, part, allPoints[offset:offset+chunkPointCounts[index]])
			offset += chunkPointCounts[index]
		}
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
	evidence := &contract.SummaryEvidence{Version: 1, Partial: partial, SourceSHA256: metadata.SourceSHA256, ItemRecipeHash: metadata.ItemRecipeSHA256, FinalPointsSHA256: hash, PointCount: len(allPoints), ValidatedHead: receipt.Ref, TreeRootSHA256: treeEvidence.RootSHA256, TreeLeafSetSHA256: treeEvidence.LeafSetSHA256, TreeDispositionSHA256: treeEvidence.DispositionSHA256, TreeGeneration: treeEvidence.Generation, TreeEpoch: treeEvidence.Epoch, TreePages: treeEvidence.Pages, TreeNodes: treeEvidence.Nodes}
	evidence.GlobalRootSHA256 = globalRoot
	if globalRoot != "" {
		evidence.GlobalState = "complete_selected_review"
		if globalStats.Partial {
			evidence.GlobalState = "partial"
		}
		evidence.GlobalStages = globalStats.Stages
		evidence.GlobalUnreviewed = globalStats.Unreviewed
		evidence.GlobalUpperUnreviewed = globalStats.UpperUnreviewed
	}
	return summaryResult{Markdown: globalNote + strings.Join(lines, "\n"), Points: allPoints, Evidence: evidence}, nil
}
