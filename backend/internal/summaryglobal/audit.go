package summaryglobal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"

	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

const EmptySummary = "本页无可核实要点"
const LimitedSummary = "全局整理受预算限制，原始要点保留供核对"
const UnreviewedSummary = "本页全局概述尚未完成原文风险复核"

type Stats struct {
	SupportedSummaries                  []string
	OrderedReviews                      []string
	Leaves, Omitted, Unreviewed, Stages int
	UpperUnreviewed                     int
	Partial                             bool
	DispositionSHA256                   string
}
type ArtifactLoader func(context.Context, string) ([]byte, error)
type StageChecker func(context.Context, StageRef) error

func Load(ctx context.Context, load ArtifactLoader, ref string, value any) error {
	if len(ref) != 64 {
		return fmt.Errorf("全局谱系引用无效")
	}
	body, err := load(ctx, ref)
	if err != nil {
		return err
	}
	if len(body) > summarytree.MaxNodeBytes || summarytree.Digest(body) != ref || Decode(body, value) != nil {
		return fmt.Errorf("全局谱系内容无效")
	}
	return nil
}
func same(a, b any) bool {
	left, _ := Encode(a)
	right, _ := Encode(b)
	return string(left) == string(right)
}

func emptyJSON(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// AuditRoot derives all leaf dispositions from the actual immutable source
// tree, not from an asserted leaf count or a model's claimed union.
func AuditRoot(ctx context.Context, load ArtifactLoader, check StageChecker, source, recipe, treeRoot, root string, chunks []summaryinput.SpanChunk) (Stats, error) {
	stats := Stats{}
	hash := sha256.New()
	seenTree, seenRecords := map[string]bool{}, map[string]bool{}
	nodes, bytes := 0, 0
	boundedLoad := func(ctx context.Context, ref string) ([]byte, error) {
		body, err := load(ctx, ref)
		if err != nil {
			return nil, err
		}
		bytes += len(body)
		if bytes > 2*summarytree.MaxSpoolBytes {
			return nil, fmt.Errorf("全局谱系总字节超限")
		}
		return body, nil
	}
	var walk func(string, string, string, int) (Record, error)
	walk = func(treeRef, recordRef, inherited string, depth int) (Record, error) {
		if ctx.Err() != nil {
			return Record{}, ctx.Err()
		}
		if depth > summarytree.MaxDepth || nodes >= summarytree.MaxNodes || seenTree[treeRef] || seenRecords[recordRef] {
			return Record{}, fmt.Errorf("全局谱系重复、有环或超限")
		}
		seenTree[treeRef] = true
		seenRecords[recordRef] = true
		nodes++
		var tree summarytree.Node
		var record Record
		if Load(ctx, boundedLoad, treeRef, &tree) != nil || Load(ctx, boundedLoad, recordRef, &record) != nil || record.Version != 1 || record.SourceTreeRef != treeRef || tree.Scope.SourceSHA256 != source || tree.Scope.RecipeSHA256 != recipe || len(record.Children) != len(tree.Children) {
			return Record{}, fmt.Errorf("全局节点与来源不一致")
		}
		childRecords := make([]Record, len(tree.Children))
		semanticChildren := []string{}
		for i, childRef := range record.Children {
			if err := Load(ctx, boundedLoad, childRef, &childRecords[i]); err != nil {
				return Record{}, err
			}
			semanticChildren = append(semanticChildren, childRecords[i].SemanticID)
		}
		expectedInput := LeafInput(tree)
		if len(tree.Children) > 0 {
			expectedInput.Children = []Child{}
			for _, child := range childRecords {
				expectedInput.Children = append(expectedInput.Children, Child{child.SemanticID, child.DisplaySummary})
			}
		}
		if !same(record.Input, expectedInput) || record.SemanticID != SemanticID(source, recipe, tree, semanticChildren) {
			return Record{}, fmt.Errorf("全局输入集合或语义身份无效")
		}
		dispositions := map[string]Disposition{}
		verdicts := map[string]string{}
		localPartial := false
		localUnknown := 0
		expectedDisplay := EmptySummary
		if record.LeafCount > 0 {
			if record.Limited {
				expectedDisplay = LimitedSummary
				localPartial = true
				localUnknown = record.LeafCount
				if len(tree.Children) > 0 {
					localUnknown = 0
					stats.UpperUnreviewed++
				}
				for _, child := range record.Input.Children {
					dispositions[child.ID] = Disposition{child.ID, "omitted", "budget_limited"}
				}
				if !emptyJSON(record.OrganizationJSON) || record.Reduce != (StageRef{}) || !emptyJSON(record.ReviewJSON) || record.ReviewInput != nil || record.Verify != (StageRef{}) || record.ReviewLimited {
					return Record{}, fmt.Errorf("受限节点伪称模型完成")
				}
			} else {
				organization, err := ParseOrganization(record.OrganizationJSON, record.Input)
				if err != nil {
					return Record{}, err
				}
				encoded, _ := Encode(record.Input)
				if record.Reduce.Purpose != "summary-reduce" || record.Reduce.InputSHA256 != summarytree.Digest(encoded) || record.Reduce.JSONSHA256 != summarytree.Digest(record.OrganizationJSON) || check(ctx, record.Reduce) != nil {
					return Record{}, fmt.Errorf("全局整理实际调用证据无效")
				}
				stats.Stages++
				expectedDisplay = organization.Summary
				for _, disposition := range organization.Dispositions {
					dispositions[disposition.ID] = disposition
					if disposition.State == "omitted" {
						localPartial = true
					}
				}
				if len(tree.Children) > 0 {
					localUnknown = 0
					stats.UpperUnreviewed++
					expectedDisplay = UnreviewedSummary
					localPartial = true
				} else {
					if tree.Chunk < 0 || tree.Chunk >= len(chunks) {
						return Record{}, fmt.Errorf("复核原核心区间丢失")
					}
					expectedReview := ReviewInput{Source: chunks[tree.Chunk].Input, Cards: SelectRisks(tree), ProposedSummary: organization.Summary}
					if record.ReviewLimited {
						expectedDisplay = UnreviewedSummary
						localPartial = true
						localUnknown = len(tree.Leaves)
						if record.Verify.ID != "" || !emptyJSON(record.ReviewJSON) {
							return Record{}, fmt.Errorf("预算受限复核伪称完成")
						}
					} else {
						if record.ReviewInput == nil || !same(*record.ReviewInput, expectedReview) {
							return Record{}, fmt.Errorf("风险复核未回查原核心正文")
						}
						review, err := ParseReview(record.ReviewJSON, expectedReview)
						if err != nil {
							return Record{}, err
						}
						encoded, _ := Encode(expectedReview)
						if record.Verify.Purpose != "summary-verify" || record.Verify.InputSHA256 != summarytree.Digest(encoded) || record.Verify.JSONSHA256 != summarytree.Digest(record.ReviewJSON) || check(ctx, record.Verify) != nil {
							return Record{}, fmt.Errorf("风险复核实际调用证据无效")
						}
						stats.Stages++
						localUnknown = len(tree.Leaves) - len(expectedReview.Cards)
						if localUnknown > 0 {
							localPartial = true
						}
						if review.SummaryVerdict != "supported" {
							expectedDisplay = UnreviewedSummary
							localPartial = true
						}
						for _, decision := range review.Decisions {
							verdicts[decision.ID] = decision.Verdict
							if decision.Verdict != "supported" {
								localPartial = true
								expectedDisplay = UnreviewedSummary
							}
						}
					}
				}
			}
		} else if record.Limited || record.Reduce.ID != "" || !emptyJSON(record.OrganizationJSON) || !emptyJSON(record.ReviewJSON) || record.ReviewInput != nil || record.Verify.ID != "" || record.ReviewLimited {
			return Record{}, fmt.Errorf("空节点伪造处理证据")
		}
		if expectedDisplay != record.DisplaySummary {
			return Record{}, fmt.Errorf("未获支持的概述仍被发布")
		}
		if inherited == "" && len(tree.Children) == 0 && expectedDisplay != EmptySummary && expectedDisplay != LimitedSummary && expectedDisplay != UnreviewedSummary {
			stats.SupportedSummaries = append(stats.SupportedSummaries, expectedDisplay)
		}
		totalLeaves := len(tree.Leaves)
		unknown := localUnknown
		if len(tree.Children) > 0 {
			totalLeaves = 0
			for i, childTree := range tree.Children {
				inheritedReason := inherited
				disposition := dispositions[childRecords[i].SemanticID]
				if record.LeafCount > 0 && disposition.State == "omitted" {
					inheritedReason = disposition.Reason
				}
				child, err := walk(childTree, record.Children[i], inheritedReason, depth+1)
				if err != nil {
					return Record{}, err
				}
				if !reflect.DeepEqual(child, childRecords[i]) {
					return Record{}, fmt.Errorf("子节点被替换")
				}
				totalLeaves += child.LeafCount
				unknown += child.Unreviewed
			}
		} else {
			for _, leaf := range tree.Leaves {
				state, reason := "consumed", ""
				if inherited != "" {
					state, reason = "omitted", inherited
				}
				if disposition := dispositions[leaf.ID]; disposition.State == "omitted" {
					state, reason = "omitted", disposition.Reason
				}
				if state == "omitted" {
					stats.Omitted++
					localPartial = true
				}
				stats.Leaves++
				reviewState := "not_reviewed"
				if verdicts[leaf.ID] != "" {
					reviewState = verdicts[leaf.ID]
				}
				if state == "omitted" {
					reviewState = "omitted"
				}
				stats.OrderedReviews = append(stats.OrderedReviews, reviewState)
				row, _ := Encode(struct{ ID, State, Reason string }{leaf.ID, state, reason})
				_, _ = hash.Write(append(row, '\n'))
			}
		}
		if totalLeaves != record.LeafCount || unknown != record.Unreviewed {
			return Record{}, fmt.Errorf("全局节点覆盖或未复核计数无效")
		}
		stats.Unreviewed += localUnknown
		stats.Partial = stats.Partial || localPartial
		return record, nil
	}
	if _, err := walk(treeRoot, root, "", 0); err != nil {
		return Stats{}, err
	}
	stats.DispositionSHA256 = hex.EncodeToString(hash.Sum(nil))
	return stats, nil
}
