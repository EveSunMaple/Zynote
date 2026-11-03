package subtitlederived

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

type BoundaryWindow struct {
	State  string
	Choice int
	JSON   []byte
	Stage  summaryglobal.StageRef
}
type SummaryPlan struct {
	Version                    int
	SourceSHA256, RecipeSHA256 string
	Complete                   bool
	Windows                    []BoundaryWindow
	Digest                     string
}

func planDigest(plan SummaryPlan) string { plan.Digest = ""; return recipeHash(plan) }

func summaryPlanCounts(plan *SummaryPlan) (stages, fallbacks int) {
	if plan == nil {
		return
	}
	for _, window := range plan.Windows {
		if window.Stage.ID != "" {
			stages++
		}
		if strings.HasPrefix(window.State, "fallback_") {
			fallbacks++
		}
	}
	return
}

func (service *Service) ensureSummaryPlan(ctx context.Context, task *Task, item *Item, deps Dependencies, srt string) ([]summaryinput.SpanChunk, error) {
	segments, err := documents.ParseSrt(srt)
	if err != nil {
		return nil, err
	}
	policy := item.Recipe.ModeRecipe
	base, err := summaryinput.PlanSpans(item.Mode, segments, policy.ChunkLimit)
	if err != nil {
		return nil, err
	}
	if item.SummaryPlan == nil {
		item.SummaryPlan = &SummaryPlan{Version: 1, SourceSHA256: item.Source.Hash, RecipeSHA256: item.Recipe.Hash, Windows: []BoundaryWindow{}}
		item.SummaryPlan.Digest = planDigest(*item.SummaryPlan)
	}
	if item.SummaryPlan.Complete {
		return service.validatedSummaryPlan(ctx, *task, item, segments)
	}
	if len(item.SummaryPlan.Windows) > len(base) {
		return nil, observerError()
	}
	if item.SummaryPlan.Digest != planDigest(*item.SummaryPlan) || item.SummaryPlan.SourceSHA256 != item.Source.Hash || item.SummaryPlan.RecipeSHA256 != item.Recipe.Hash {
		return nil, observerError()
	}
	// Validate only the durable prefix; pending windows may have staged payloads
	// that must be adopted, and must never be fabricated as fallback decisions.
	if _, err := service.validateSummaryPlan(ctx, *task, item, segments, false); err != nil {
		return nil, err
	}
	factory := newSummaryStageFactory(service, task, item)
	stageContext := contract.WithSummaryStages(ctx, factory)
	for index := len(item.SummaryPlan.Windows); index < len(base); index++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		input := summaryinput.BoundaryCandidates(base[index].Input, policy.ChunkLimit)
		encodedInput, _ := summaryglobal.Encode(input)
		window := BoundaryWindow{Choice: -1, State: "deterministic"}
		if policy.PlanPolicy == "semantic-candidate-v1" {
			key := stageIdentity("summary-boundary", index, summarytree.Digest(encodedInput), policy.BoundaryPromptSHA256, item.Source.Hash, item.Recipe.Hash)
			if deps.ProposeBoundary == nil && item.Stages[key] != nil {
				return nil, contract.ObservationFailure(fmt.Errorf("已有边界调用证据，不能以能力不可用绕过恢复"))
			}
			switch {
			case index >= 256:
				window.State = "fallback_window_limit"
			case len(input.Candidates) == 0:
				window.State = "fallback_no_candidates"
			case len(encodedInput) > summarytree.MaxNodeBytes:
				window.State = "fallback_input_limit"
			case deps.ProposeBoundary == nil:
				window.State = "fallback_unavailable"
			default:
				raw, ref, callErr := deps.ProposeBoundary(stageContext, index, input)
				if errors.Is(callErr, generationjob.ErrBudget) {
					if item.Stages[key] != nil {
						return nil, contract.ObservationFailure(fmt.Errorf("已有边界尝试不能以预算回退绕过未知结果"))
					}
					window.State = "fallback_budget"
				} else if callErr != nil {
					return nil, callErr
				} else {
					if len(raw) > 16<<10 {
						return nil, fmt.Errorf("边界提议输出超限")
					}
					window.JSON = raw
					window.Stage = ref
					window.Choice, window.State = summaryinput.BoundaryChoice(raw, input)
				}
			}
		}
		item.SummaryPlan.Windows = append(item.SummaryPlan.Windows, window)
		item.SummaryPlan.Digest = planDigest(*item.SummaryPlan)
		if err := service.save(*task); err != nil {
			return nil, err
		}
	}
	item.SummaryPlan.Complete = true
	item.SummaryPlan.Digest = planDigest(*item.SummaryPlan)
	if err := service.save(*task); err != nil {
		return nil, err
	}
	return service.validatedSummaryPlan(ctx, *task, item, segments)
}

func (service *Service) validatedSummaryPlan(ctx context.Context, task Task, item *Item, segments []documents.Segment) ([]summaryinput.SpanChunk, error) {
	return service.validateSummaryPlan(ctx, task, item, segments, true)
}
func (service *Service) validateSummaryPlan(ctx context.Context, task Task, item *Item, segments []documents.Segment, requireComplete bool) ([]summaryinput.SpanChunk, error) {
	plan := item.SummaryPlan
	policy := item.Recipe.ModeRecipe
	if plan == nil || plan.Version != 1 || (requireComplete && !plan.Complete) || plan.SourceSHA256 != item.Source.Hash || plan.RecipeSHA256 != item.Recipe.Hash || plan.Digest != planDigest(*plan) {
		return nil, observerError()
	}
	base, err := summaryinput.PlanSpans(item.Mode, segments, policy.ChunkLimit)
	if err != nil || len(plan.Windows) > len(base) || (requireComplete && len(base) != len(plan.Windows)) {
		return nil, observerError()
	}
	factory := newSummaryStageFactory(service, &task, item)
	// The caller may pass a copied Item; locate its stable identity.
	for index := range task.Items {
		if task.Items[index].Recipe != nil && task.Items[index].Recipe.Hash == item.Recipe.Hash {
			factory = newSummaryStageFactory(service, &task, &task.Items[index])
			break
		}
	}
	choices := make([]int, len(base))
	for index := range choices {
		choices[index] = -1
	}
	for index, window := range plan.Windows {
		input := summaryinput.BoundaryCandidates(base[index].Input, policy.ChunkLimit)
		if len(window.JSON) > 0 {
			choice, state := summaryinput.BoundaryChoice(window.JSON, input)
			encoded, _ := summaryglobal.Encode(input)
			if policy.PlanPolicy != "semantic-candidate-v1" || index >= 256 || choice != window.Choice || state != window.State || window.Stage.Index != index || window.Stage.Purpose != "summary-boundary" || window.Stage.InputSHA256 != summarytree.Digest(encoded) || window.Stage.JSONSHA256 != summarytree.Digest(window.JSON) || factory.Check(ctx, window.Stage.ID, window.Stage.Format, window.Stage.Purpose, window.Stage.InputSHA256, window.Stage.JSONSHA256) != nil {
				return nil, observerError()
			}
		} else {
			if window.Choice != -1 || window.Stage != (summaryglobal.StageRef{}) {
				return nil, observerError()
			}
			if policy.PlanPolicy == "semantic-candidate-v1" {
				encoded, _ := summaryglobal.Encode(input)
				key := stageIdentity("summary-boundary", index, summarytree.Digest(encoded), policy.BoundaryPromptSHA256, item.Source.Hash, item.Recipe.Hash)
				if item.Stages[key] != nil {
					return nil, observerError()
				}
			}
			switch window.State {
			case "deterministic":
				if policy.PlanPolicy != "deterministic-v1" {
					return nil, observerError()
				}
			case "fallback_no_candidates":
				if policy.PlanPolicy != "semantic-candidate-v1" || len(input.Candidates) != 0 {
					return nil, observerError()
				}
			case "fallback_window_limit":
				if policy.PlanPolicy != "semantic-candidate-v1" || index < 256 {
					return nil, observerError()
				}
			case "fallback_input_limit":
				encoded, _ := summaryglobal.Encode(input)
				if policy.PlanPolicy != "semantic-candidate-v1" || len(encoded) <= summarytree.MaxNodeBytes {
					return nil, observerError()
				}
			case "fallback_budget", "fallback_unavailable":
				if policy.PlanPolicy != "semantic-candidate-v1" {
					return nil, observerError()
				}
			default:
				return nil, observerError()
			}
		}
		choices[index] = window.Choice
	}
	return summaryinput.ExperimentalPlan(item.Mode, segments, policy.ChunkLimit, policy.HaloBudget, choices)
}
