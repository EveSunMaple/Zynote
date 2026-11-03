package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
)

func TestExperimentalFrozenBoundaryHaloAndFallbackRecovery(t *testing.T) {
	for _, scenario := range []string{"applied", "invalid", "null", "budget", "unavailable", "halo_only"} {
		t.Run(scenario, func(t *testing.T) {
			srt := "1\n00:00:01,000 --> 00:59:00,000\n" + strings.Repeat("原文限定结论。", 2600) + "\n"
			service, task, client, calls, deps := globalRecoveryFixture(t, srt, scenario)
			policy := "semantic-candidate-v1"
			if scenario == "halo_only" {
				policy = "deterministic-v1"
			}
			deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				if contract.SummaryRecipeVersion(ctx, "brief") == 0 {
					ctx = contract.WithSummaryRecipeVersions(ctx, map[string]int{"brief": 4})
					ctx = contract.WithSummaryPlanPolicies(ctx, map[string]contract.SummaryPlanPolicy{"brief": {PlanPolicy: policy, HaloBudget: 128}})
				}
				return resolveSubtitleRecipesForContext(ctx, client, modes)
			}
			if scenario == "budget" {
				deps.ProposeBoundary = func(context.Context, int, any) ([]byte, summaryglobal.StageRef, error) {
					return nil, summaryglobal.StageRef{}, generationjob.ErrBudget
				}
			}
			if scenario == "unavailable" {
				deps.ProposeBoundary = nil
			}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil {
				t.Fatal(err)
			}
			item := result.Items[0]
			plan := item.SummaryPlan
			if plan == nil || !plan.Complete || item.Recipe.ModeRecipe.Version != 4 || item.Recipe.ModeRecipe.HaloBudget != 128 {
				t.Fatalf("missing frozen plan %+v", item)
			}
			expected := map[string]string{"applied": "applied", "invalid": "fallback_invalid", "null": "fallback_invalid", "budget": "fallback_budget", "unavailable": "fallback_unavailable", "halo_only": "deterministic"}[scenario]
			if plan.Windows[0].State != expected {
				t.Fatalf("expected %s got %+v", expected, plan.Windows)
			}
			pages := 2
			if scenario == "applied" {
				pages = 3
			}
			if item.SummaryEvidence.TreePages != pages {
				t.Fatalf("wrong applied core plan %+v", item.SummaryEvidence)
			}
			before := calls.Load()
			for range 2 {
				if _, err := service.Run(context.Background(), task.ID, deps); err != nil {
					t.Fatal(err)
				}
			}
			if calls.Load() != before {
				t.Fatal("resume replayed paid plan/extraction")
			}
			// Mutation with recomputed outer digest still cannot move a model boundary
			// away from the original model evidence and actual source candidates.
			stored, err := service.Read(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			stored.Items[0].SummaryPlan.Windows[0].Choice = 1
			stored.Items[0].SummaryPlan.Digest = "invalid"
			if service.AuditAttemptEvidence(context.Background(), stored, 0).State != "invalid" {
				t.Fatal("mutated persisted plan accepted")
			}
		})
	}
}

type unknownBoundaryTransport struct{ calls atomic.Int64 }

func TestExperimentalBoundarySplitFourAttemptCapPublishesPartial(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:59:00,000\n" + strings.Repeat("原文限定结论。", 1600) + "\n"
	service, task, client, calls, deps := globalRecoveryFixture(t, srt)
	deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		if contract.SummaryRecipeVersion(ctx, "brief") == 0 {
			ctx = contract.WithSummaryRecipeVersions(ctx, map[string]int{"brief": 4})
			ctx = contract.WithSummaryPlanPolicies(ctx, map[string]contract.SummaryPlanPolicy{"brief": {PlanPolicy: "semantic-candidate-v1"}})
		}
		return resolveSubtitleRecipesForContext(ctx, client, modes)
	}
	var used atomic.Int64
	capContext := func(ctx context.Context) context.Context {
		return contract.WithPhysicalRequestGuard(ctx, realProfileCap{inner: contract.PhysicalRequestGuardFrom(ctx), used: &used, limit: 4})
	}
	deps.ProposeBoundary = func(ctx context.Context, index int, input any) ([]byte, summaryglobal.StageRef, error) {
		return proposeSummaryBoundary(capContext(ctx), client, index, input)
	}
	deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		return summarizeDerivedText(capContext(ctx), client, srt, mode, nil)
	}
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 4 || len(result.Items[0].Outputs) != 2 || !result.Items[0].SummaryEvidence.Partial {
		t.Fatalf("fourcap partial err=%v result=%+v calls=%d", err, result, calls.Load())
	}
}

func (transport *unknownBoundaryTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("fixture dispatched boundary response unknown")
}

func TestUnknownBoundaryCannotReplayOrBecomeFallback(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:59:00,000\n" + strings.Repeat("原文限定结论。", 2600) + "\n"
	service, task, client, _, deps := globalRecoveryFixture(t, srt)
	transport := &unknownBoundaryTransport{}
	client.HTTP = &http.Client{Transport: transport}
	deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		if contract.SummaryRecipeVersion(ctx, "brief") == 0 {
			ctx = contract.WithSummaryRecipeVersions(ctx, map[string]int{"brief": 4})
			ctx = contract.WithSummaryPlanPolicies(ctx, map[string]contract.SummaryPlanPolicy{"brief": {PlanPolicy: "semantic-candidate-v1"}})
		}
		return resolveSubtitleRecipesForContext(ctx, client, modes)
	}
	first, _ := service.Run(context.Background(), task.ID, deps)
	if transport.calls.Load() != 1 || len(first.Items[0].Outputs) != 0 || len(first.Items[0].Stages) != 1 {
		t.Fatal("unknown setup failed")
	}
	if _, err := service.Run(context.Background(), task.ID, deps); !contract.IsObservationFailure(err) || transport.calls.Load() != 1 {
		t.Fatalf("unknown boundary replayed err=%v calls=%d", err, transport.calls.Load())
	}
	propose := deps.ProposeBoundary
	deps.ProposeBoundary = nil
	if _, err := service.Run(context.Background(), task.ID, deps); !contract.IsObservationFailure(err) || transport.calls.Load() != 1 {
		t.Fatal("unknown became unavailable fallback", err)
	}
	deps.ProposeBoundary = func(context.Context, int, any) ([]byte, summaryglobal.StageRef, error) {
		return nil, summaryglobal.StageRef{}, generationjob.ErrBudget
	}
	if _, err := service.Run(context.Background(), task.ID, deps); !contract.IsObservationFailure(err) || transport.calls.Load() != 1 {
		t.Fatal("unknown became budget fallback", err)
	}
	deps.ProposeBoundary = propose
}

func TestBoundaryDurableStageBeforePlanRecoveryAndFrozenPolicy(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:59:00,000\n" + strings.Repeat("原文限定结论。", 2600) + "\n"
	service, task, client, calls, deps := globalRecoveryFixture(t, srt)
	deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		if contract.SummaryRecipeVersion(ctx, "brief") == 0 {
			ctx = contract.WithSummaryRecipeVersions(ctx, map[string]int{"brief": 4})
			ctx = contract.WithSummaryPlanPolicies(ctx, map[string]contract.SummaryPlanPolicy{"brief": {PlanPolicy: "semantic-candidate-v1", HaloBudget: 0}})
		}
		return resolveSubtitleRecipesForContext(ctx, client, modes)
	}
	propose := deps.ProposeBoundary
	fault := true
	deps.ProposeBoundary = func(ctx context.Context, index int, input any) ([]byte, summaryglobal.StageRef, error) {
		body, ref, err := propose(ctx, index, input)
		if err == nil && fault {
			fault = false
			return nil, summaryglobal.StageRef{}, contract.ObservationFailure(errors.New("fixture crash window after durable boundary before plan"))
		}
		return body, ref, err
	}
	if _, err := service.Run(context.Background(), task.ID, deps); err == nil || calls.Load() != 1 {
		t.Fatalf("fault did not stop before extraction err=%v calls=%d", err, calls.Load())
	}
	// Ambient toggles cannot change an already frozen experimental plan.
	t.Setenv("ZYNOTE_SUMMARY_EXPERIMENTAL_PLAN", "0")
	t.Setenv("ZYNOTE_SUMMARY_HALO", "1")
	t.Setenv("ZYNOTE_SUMMARY_SEMANTIC_BOUNDARIES", "0")
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionGeneration != 2 || result.Items[0].Recipe.ModeRecipe.HaloBudget != 0 || result.Items[0].SummaryEvidence.BoundaryStages != 1 || result.Items[0].SummaryPlan.Windows[0].State != "applied" {
		t.Fatal("durable boundary adoption or frozen policy lost")
	}
	if calls.Load() != 11 {
		t.Fatalf("paid boundary replayed calls=%d", calls.Load())
	}
	before := calls.Load()
	if _, err := service.Run(context.Background(), task.ID, deps); err != nil || calls.Load() != before {
		t.Fatal("subsequent resume replay", err)
	}
}
