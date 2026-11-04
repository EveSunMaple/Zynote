package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

func organizeSummaryTree(ctx context.Context, client *ai.Client, treeRoot string, chunks []summaryinput.SpanChunk) (string, string, summaryglobal.Stats, error) {
	factory := contract.SummaryStagesFrom(ctx)
	metadata, _ := contract.GenerationObservationFrom(ctx)
	if factory == nil || treeRoot == "" {
		return "", "", summaryglobal.Stats{}, fmt.Errorf("全局整理缺少分页或独立阶段能力")
	}
	stageIndex, nodes := 0, 0
	seen := map[string]bool{}
	var build func(string, int) (string, summaryglobal.Record, error)
	build = func(treeRef string, depth int) (string, summaryglobal.Record, error) {
		if ctx.Err() != nil {
			return "", summaryglobal.Record{}, ctx.Err()
		}
		if depth > summarytree.MaxDepth || nodes >= summarytree.MaxNodes || seen[treeRef] {
			return "", summaryglobal.Record{}, fmt.Errorf("全局整理谱系超限或重复")
		}
		seen[treeRef] = true
		nodes++
		var tree summarytree.Node
		if err := summaryglobal.Load(ctx, factory.LoadArtifact, treeRef, &tree); err != nil {
			return "", summaryglobal.Record{}, err
		}
		record := summaryglobal.Record{Version: 1, SourceTreeRef: treeRef, Input: summaryglobal.LeafInput(tree), LeafCount: len(tree.Leaves), DisplaySummary: summaryglobal.EmptySummary}
		semanticChildren := []string{}
		childUnknown := 0
		if len(tree.Children) > 0 {
			record.Input.Children = []summaryglobal.Child{}
			record.LeafCount = 0
			for _, childRef := range tree.Children {
				ref, child, err := build(childRef, depth+1)
				if err != nil {
					return "", record, err
				}
				record.Children = append(record.Children, ref)
				semanticChildren = append(semanticChildren, child.SemanticID)
				record.LeafCount += child.LeafCount
				childUnknown += child.Unreviewed
				record.Input.Children = append(record.Input.Children, summaryglobal.Child{ID: child.SemanticID, Text: child.DisplaySummary})
			}
		}
		record.SemanticID = summaryglobal.SemanticID(metadata.SourceSHA256, metadata.ItemRecipeSHA256, tree, semanticChildren)
		if record.LeafCount > 0 {
			index := stageIndex
			stageIndex++
			output, receipt, err := runSummaryStage(ctx, client, "summary-reduce", index, summaryglobal.ReducePrompt, record.Input, func(body []byte) error { _, err := summaryglobal.ParseOrganization(body, record.Input); return err })
			if errors.Is(err, generationjob.ErrBudget) {
				record.Limited = true
				record.Unreviewed = record.LeafCount + childUnknown
				if len(tree.Children) > 0 {
					record.Unreviewed = childUnknown
				}
				record.DisplaySummary = summaryglobal.LimitedSummary
			} else if err != nil {
				return "", record, err
			} else {
				record.OrganizationJSON = output
				record.Reduce = receipt
				organization, _ := summaryglobal.ParseOrganization(output, record.Input)
				record.DisplaySummary = organization.Summary
				if len(tree.Children) > 0 {
					record.Unreviewed = childUnknown
					record.DisplaySummary = summaryglobal.UnreviewedSummary
				} else {
					if tree.Chunk < 0 || tree.Chunk >= len(chunks) {
						return "", record, fmt.Errorf("风险复核原核心区间无效")
					}
					reviewInput := summaryglobal.ReviewInput{Source: chunks[tree.Chunk].Input, Cards: summaryglobal.SelectRisks(tree), ProposedSummary: organization.Summary}
					record.ReviewInput = &reviewInput
					index = stageIndex
					stageIndex++
					reviewOutput, reviewReceipt, err := runSummaryStage(ctx, client, "summary-verify", index, summaryglobal.VerifyPrompt, reviewInput, func(body []byte) error { _, err := summaryglobal.ParseReview(body, reviewInput); return err })
					if errors.Is(err, generationjob.ErrBudget) {
						record.ReviewLimited = true
						record.Unreviewed = len(tree.Leaves)
						record.DisplaySummary = summaryglobal.UnreviewedSummary
					} else if err != nil {
						return "", record, err
					} else {
						record.ReviewJSON = reviewOutput
						record.Verify = reviewReceipt
						review, _ := summaryglobal.ParseReview(reviewOutput, reviewInput)
						record.Unreviewed = len(tree.Leaves) - len(reviewInput.Cards)
						if review.SummaryVerdict != "supported" {
							record.DisplaySummary = summaryglobal.UnreviewedSummary
						}
						for _, decision := range review.Decisions {
							if decision.Verdict != "supported" {
								record.DisplaySummary = summaryglobal.UnreviewedSummary
							}
						}
					}
				}
			}
		}
		body, err := summaryglobal.Encode(record)
		if err != nil || len(body) > summarytree.MaxNodeBytes {
			return "", record, fmt.Errorf("全局节点超过外存页面预算")
		}
		ref, err := factory.SaveArtifact(ctx, body)
		return ref, record, err
	}
	root, record, err := build(treeRoot, 0)
	if err != nil {
		return "", "", summaryglobal.Stats{}, err
	}
	check := func(ctx context.Context, ref summaryglobal.StageRef) error {
		return factory.Check(ctx, ref.ID, ref.Format, ref.Purpose, ref.InputSHA256, ref.JSONSHA256)
	}
	stats, err := summaryglobal.AuditRoot(ctx, factory.LoadArtifact, check, metadata.SourceSHA256, metadata.ItemRecipeSHA256, treeRoot, root, chunks)
	if err != nil {
		return "", "", stats, err
	}
	manifest := summaryglobal.Manifest{Version: 1, SourceSHA256: metadata.SourceSHA256, RecipeSHA256: metadata.ItemRecipeSHA256, TreeRoot: treeRoot, RootRecord: root, LeafDispositionSHA256: stats.DispositionSHA256, Leaves: stats.Leaves, Omitted: stats.Omitted, Unreviewed: stats.Unreviewed, UpperUnreviewed: stats.UpperUnreviewed, Stages: stats.Stages, Partial: stats.Partial}
	body, _ := json.Marshal(manifest)
	ref, err := factory.SaveArtifact(ctx, body)
	if err != nil {
		return "", "", stats, err
	}
	note := fmt.Sprintf("## 全局整理与原文风险复核\n\n%s\n\n> 本轮记录 %d 条叶证据、%d 个实际整理/复核调用、%d 条省略处置、%d 条未复核叶证据和 %d 个未复核上层概述。分层整理不证明全部教学命题完整；下列原始要点保留供核对。\n\n", record.DisplaySummary, stats.Leaves, stats.Stages, stats.Omitted, stats.Unreviewed, stats.UpperUnreviewed)
	if stats.UpperUnreviewed > 0 && len(stats.SupportedSummaries) > 0 {
		note += "### 已回查原文的分段概述\n\n" + strings.Join(stats.SupportedSummaries, "\n\n") + "\n\n"
	}
	return ref, note, stats, nil
}
