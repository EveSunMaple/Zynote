package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

type stageFaultFactory struct {
	contract.SummaryStageFactory
	after bool
	fired *bool
}

func (factory stageFaultFactory) Open(ctx context.Context, purpose string, index int, input, prompt string) (context.Context, error) {
	ctx, err := factory.SummaryStageFactory.Open(ctx, purpose, index, input, prompt)
	if err != nil {
		return ctx, err
	}
	metadata, original := contract.GenerationObservationFrom(ctx)
	observer := func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
		if purpose == "summary-reduce" && event.State == "format-result" && !*factory.fired {
			*factory.fired = true
			if factory.after {
				if err := original(ctx, event); err != nil {
					return err
				}
			}
			return errors.New("fixture fault at durable stage payload/format boundary")
		}
		return original(ctx, event)
	}
	return contract.WithGenerationObservation(ctx, metadata, observer), nil
}

type denyGlobalBudget struct{ inner contract.PhysicalRequestGuard }

func (guard denyGlobalBudget) Reserve(ctx context.Context, digest string, size int64) (contract.PhysicalRequestAttempt, error) {
	metadata, _ := contract.GenerationObservationFrom(ctx)
	if metadata.Purpose != "subtitle-summary" {
		return nil, generationjob.ErrBudget
	}
	return guard.inner.Reserve(ctx, digest, size)
}
func globalRecoveryFixture(t *testing.T, source ...string) (*subtitlederived.Service, subtitlederived.Task, *ai.Client, *atomic.Int64, subtitlederived.Dependencies) {
	t.Helper()
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	srt := "1\n00:00:01,000 --> 00:00:02,000\n原文限定结论\n"
	if len(source) > 0 {
		srt = source[0]
	}
	task, err := service.CreateDirectOperation(srt, "Global recovery", "brief", "", "e3e3e3e3-e3e3-43e3-83e3-e3e3e3e3e3e3")
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Messages []ai.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		prompt, _ := request.Messages[0].Content.(string)
		wire, _ := request.Messages[1].Content.(string)
		var output any
		switch prompt {
		case summaryinput.BoundaryPrompt:
			var input summaryinput.BoundaryInput
			_ = json.Unmarshal([]byte(wire), &input)
			boundary := -1
			if len(input.Candidates) > 0 {
				boundary = input.Candidates[0]
			}
			if len(source) > 1 && source[1] == "invalid" {
				boundary = -999
			}
			output = map[string]any{"boundary": boundary}
			if len(source) > 1 && source[1] == "null" {
				output = nil
			}
		case summaryglobal.ReducePrompt:
			var input summaryglobal.Input
			_ = json.Unmarshal([]byte(wire), &input)
			dispositions := []summaryglobal.Disposition{}
			for _, child := range input.Children {
				dispositions = append(dispositions, summaryglobal.Disposition{ID: child.ID, State: "consumed"})
			}
			summary := "已核对的分段概述"
			if len(input.Children) > 1 {
				summary = "未经原文复核的父层新增事实"
			}
			output = summaryglobal.Organization{Title: "主题", Summary: summary, Dispositions: dispositions}
		case summaryglobal.VerifyPrompt:
			var input summaryglobal.ReviewInput
			_ = json.Unmarshal([]byte(wire), &input)
			decisions := []summaryglobal.RiskDecision{}
			for _, card := range input.Cards {
				decisions = append(decisions, summaryglobal.RiskDecision{ID: card.ID, Verdict: "supported", Reason: "原文限定核对"})
			}
			output = summaryglobal.Review{SummaryVerdict: "supported", Decisions: decisions}
		default:
			var input summaryinput.SpanInput
			_ = json.Unmarshal([]byte(wire), &input)
			if len(input.Cues) == 0 {
				t.Error("missing source core")
				return
			}
			quote := []rune(input.Cues[0].Text)
			if len(quote) > 3 {
				quote = quote[:3]
			}
			output = map[string]any{"processing": "complete", "cards": []any{map[string]any{"cue": input.Cues[0].Cue, "quote": string(quote), "point": "原文限定结论"}}}
		}
		body, _ := json.Marshal(output)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
	}))
	t.Cleanup(server.Close)
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
	deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		versions := map[string]int{}
		for _, mode := range modes {
			versions[mode] = 3
		}
		return resolveSubtitleRecipesForContext(contract.WithSummaryRecipeVersions(ctx, versions), client, modes)
	}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		return summarizeDerivedText(ctx, client, srt, mode, nil)
	}}
	deps.ProposeBoundary = func(ctx context.Context, index int, input any) ([]byte, summaryglobal.StageRef, error) {
		return proposeSummaryBoundary(ctx, client, index, input)
	}
	return service, task, client, calls, deps
}
func TestGlobalMultiPageParentProseIsSuppressedAndLeafSummariesRetained(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:59:00,000\n" + strings.Repeat("原文限定结论", 6000) + "\n"
	service, task, _, calls, deps := globalRecoveryFixture(t, srt)
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil {
		t.Fatal(err)
	}
	facts := result.Items[0].SummaryEvidence
	if facts == nil || facts.TreePages != 3 || facts.GlobalStages != 7 || facts.GlobalUpperUnreviewed != 1 || facts.GlobalUnreviewed != 0 || !facts.Partial || calls.Load() != 10 {
		t.Fatalf("bad parent/leaf evidence %+v calls=%d", facts, calls.Load())
	}
	for _, output := range result.Items[0].Outputs {
		if !strings.HasSuffix(output.Path, ".md") {
			continue
		}
		body, err := os.ReadFile(output.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "未经原文复核的父层新增事实") || !strings.Contains(string(body), "已回查原文的分段概述") || strings.Count(string(body), "已核对的分段概述") != 3 {
			t.Fatal("unverified parent leaked or useful supported children lost")
		}
	}
}

func TestIndependentStageDurablePayloadFaultRecoveryWithoutHTTPReplay(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_format_link", true: "after_format_link"}[after], func(t *testing.T) {
			service, task, client, calls, deps := globalRecoveryFixture(t)
			inject := true
			fired := false
			deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				if inject {
					ctx = contract.WithSummaryStages(ctx, stageFaultFactory{contract.SummaryStagesFrom(ctx), after, &fired})
				}
				return summarizeDerivedText(ctx, client, srt, mode, nil)
			}
			first, err := service.Run(context.Background(), task.ID, deps)
			if !contract.IsObservationFailure(err) || !fired || calls.Load() != 2 || len(first.Items[0].Outputs) != 0 {
				t.Fatalf("stage fault setup %+v %v calls=%d", first, err, calls.Load())
			}
			inject = false
			restarted := subtitlederived.New(service.Root, service.Library)
			result, err := restarted.Run(context.Background(), task.ID, deps)
			if err != nil || calls.Load() != 3 || len(result.Items[0].Outputs) != 2 || result.Items[0].SummaryEvidence.GlobalStages != 2 {
				t.Fatalf("stage replayed extraction/reduce %+v %v calls=%d", result, err, calls.Load())
			}
		})
	}
}
func TestExperimentalGlobalBudgetUnavailablePublishesExplicitPartialEvidence(t *testing.T) {
	service, task, client, calls, deps := globalRecoveryFixture(t)
	deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		ctx = contract.WithPhysicalRequestGuard(ctx, denyGlobalBudget{contract.PhysicalRequestGuardFrom(ctx)})
		return summarizeDerivedText(ctx, client, srt, mode, nil)
	}
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 1 || len(result.Items[0].Outputs) != 2 {
		t.Fatalf("budget fallback %+v %v calls=%d", result, err, calls.Load())
	}
	facts := result.Items[0].SummaryEvidence
	if !facts.Partial || facts.GlobalState != "partial" || facts.GlobalStages != 0 || facts.GlobalUnreviewed != 1 {
		t.Fatalf("budget limitation hidden %+v", facts)
	}
	body, _ := os.ReadFile(result.Items[0].Outputs[0].Path)
	if !strings.Contains(string(body), summaryglobal.LimitedSummary) || !strings.Contains(string(body), contract.SummaryReviewLabel("omitted")) {
		t.Fatal("budget-limited original evidence shown as verified conclusion")
	}
}

func TestExperimentalGlobalOrganizationAndOriginalSpanRiskReview(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(map[bool]string{true: "supported", false: "unsupported"}[supported], func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			sourceText := "原文并不表示所有概括都正确，需要核对原始语境。"
			task, err := service.CreateDirectOperation("1\n00:00:01,000 --> 00:00:02,000\n"+sourceText+"\n", "Global fixture", "mindmap", "", "d9d9d9d9-d9d9-49d9-89d9-d9d9d9d9d9d9")
			if err != nil {
				t.Fatal(err)
			}
			var calls, reduce, verify atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					Messages []ai.Message `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
					t.Error("bad request")
					return
				}
				prompt, _ := request.Messages[0].Content.(string)
				wire, _ := request.Messages[1].Content.(string)
				var output any
				switch prompt {
				case summaryglobal.ReducePrompt:
					reduce.Add(1)
					var input summaryglobal.Input
					if json.Unmarshal([]byte(wire), &input) != nil || len(input.Children) != 1 {
						t.Error("bad reduce leaf input")
						return
					}
					output = summaryglobal.Organization{Title: "全局主题", Summary: "经过全局组织的概述", Dispositions: []summaryglobal.Disposition{{ID: input.Children[0].ID, State: "consumed", Reason: ""}}}
				case summaryglobal.VerifyPrompt:
					verify.Add(1)
					var input summaryglobal.ReviewInput
					if json.Unmarshal([]byte(wire), &input) != nil || len(input.Source.Cues) != 1 || input.Source.Cues[0].Text != sourceText || len(input.Cards) != 1 {
						t.Error("risk review did not receive original full core")
						return
					}
					verdict := "supported"
					if !supported {
						verdict = "unsupported"
					}
					output = summaryglobal.Review{SummaryVerdict: verdict, Decisions: []summaryglobal.RiskDecision{{ID: input.Cards[0].ID, Verdict: verdict, Reason: "fixture checks original core and negation"}}}
				default:
					output = map[string]any{"processing": "complete", "cards": []any{map[string]any{"cue": 0, "quote": "并不表示", "point": "原文语境概括", "topic": "主题"}}}
				}
				body, _ := json.Marshal(output)
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				versions := map[string]int{}
				for _, mode := range modes {
					versions[mode] = 3
				}
				return resolveSubtitleRecipesForContext(contract.WithSummaryRecipeVersions(ctx, versions), client, modes)
			}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				return summarizeDerivedText(ctx, client, srt, mode, nil)
			}, RenderSVG: func(title string, points []subtitlederived.Point) string {
				converted := []notePoint{}
				for _, point := range points {
					converted = append(converted, notePoint{Cue: point.Cue, Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp, ReviewState: point.ReviewState})
				}
				return notesSVG(title, converted)
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil || calls.Load() != 3 || reduce.Load() != 1 || verify.Load() != 1 || len(result.Items[0].Outputs) != 3 {
				t.Fatalf("global integration %+v %v calls=%d reduce=%d verify=%d", result, err, calls.Load(), reduce.Load(), verify.Load())
			}
			item := result.Items[0]
			facts := item.SummaryEvidence
			if facts == nil || facts.GlobalRootSHA256 == "" || facts.TreeRootSHA256 == "" || facts.GlobalStages != 2 || facts.Partial == supported || item.Recipe.ModeRecipe.Version != 3 || len(item.Stages) != 2 {
				t.Fatalf("bad global evidence %+v", item)
			}
			expected := "supported"
			if !supported {
				expected = "unsupported"
			}
			for _, output := range item.Outputs {
				body, err := os.ReadFile(output.Path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(output.Path, ".json") {
					if !strings.Contains(string(body), `"reviewState": "`+expected+`"`) {
						t.Fatalf("JSON lost risk state %s", body)
					}
				} else {
					if !strings.Contains(string(body), contract.SummaryReviewLabel(expected)) {
						t.Fatalf("export lost risk label %s", output.Path)
					}
				}
				if !supported && strings.HasSuffix(output.Path, ".md") && strings.Contains(string(body), "经过全局组织的概述") {
					t.Fatal("unsupported global prose still published")
				}
			}
			// A frozen recipe3 remains resumable while the experimental default is off.
			deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipesForContext(ctx, client, modes)
			}
			if _, err := service.Run(context.Background(), task.ID, deps); err != nil || calls.Load() != 3 {
				t.Fatalf("frozen profile replay %v calls=%d", err, calls.Load())
			}
		})
	}
}
