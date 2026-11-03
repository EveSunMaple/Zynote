package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func TestSpanSummaryRealServiceReceiptsEmptyCardsAndInsufficiency(t *testing.T) {
	for _, insufficient := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "insufficient"}[insufficient], func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			text := strings.Repeat("甲", 14999) + "😀乙𠮷甲"
			srt := "1\n00:00:01,000 --> 00:00:09,000\n" + text + "\n"
			task, err := service.CreateDirectOperation(srt, "Core span fixture", "brief", "", "d2d2d2d2-d2d2-42d2-82d2-d2d2d2d2d2d2")
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				var request struct {
					Messages []ai.Message `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
					t.Error("bad request")
					return
				}
				var input summaryinput.SpanInput
				wire, _ := request.Messages[1].Content.(string)
				if json.Unmarshal([]byte(wire), &input) != nil || len(input.Cues) != 1 {
					t.Error("bad core input")
					return
				}
				cue := input.Cues[0]
				if cue.StartMs != 1000 || cue.EndMs != 9000 || cue.Time != "00:00:01" {
					t.Error("invented child times")
				}
				processing := "complete"
				cards := []map[string]any{}
				if call == 1 {
					if cue.CoreStart != 0 || cue.CoreEnd != 15000 || len([]rune(cue.Text)) != 15000 {
						t.Error("bad first core")
					}
				}
				if call == 2 {
					if cue.CoreStart != 15000 || cue.CoreEnd != 15003 || cue.Text != "乙𠮷甲" {
						t.Error("bad non-BMP second core")
					}
					if insufficient {
						processing = "insufficient"
					}
					cards = append(cards, map[string]any{"cue": 0, "quote": "乙𠮷", "point": "核心区间结论"})
				}
				body, _ := json.Marshal(map[string]any{"processing": processing, "cards": cards})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(body)}}}})
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipes(client, modes)
			}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				return summarizeDerivedText(ctx, client, srt, mode, nil)
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil || len(result.Items[0].Outputs) != 2 || calls.Load() != 2 {
				t.Fatalf("run err=%v task=%+v calls=%d", err, result, calls.Load())
			}
			item := result.Items[0]
			if item.AttemptEvidence.Version != 3 || item.SummaryEvidence == nil || item.SummaryEvidence.Partial != insufficient || item.SummaryEvidence.PointCount != 1 {
				t.Fatalf("bad facts: %+v", item)
			}
			audit := service.AuditAttemptEvidence(context.Background(), result, 0)
			if audit.State != "complete" || audit.TotalChunks != 2 || audit.CompleteChunks != 2 || audit.InsufficientChunks != map[bool]int{false: 0, true: 1}[insufficient] {
				t.Fatalf("bad audit: %+v", audit)
			}
			body, _ := os.ReadFile(item.Outputs[0].Path)
			if insufficient && !strings.Contains(string(body), "部分结果") {
				t.Fatal("insufficient output presented as complete")
			}
			if _, err := service.Run(context.Background(), task.ID, noModelOnRecovery(t)); err != nil || calls.Load() != 2 {
				t.Fatalf("replayed completed spans: %v", err)
			}
		})
	}
}

func TestSpanSummaryAllEmptyCardsFinishAdapter(t *testing.T) {
	for _, processing := range []string{"complete", "insufficient"} {
		t.Run(processing, func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			task, err := service.CreateDirectOperation("1\n00:00:01,000 --> 00:00:02,000\n空白内容\n", "Empty fixture", "brief", "", "d3d3d3d3-d3d3-43d3-83d3-d3d3d3d3d3d3")
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := json.Marshal(map[string]any{"processing": processing, "cards": []any{}})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipesForContext(ctx, client, modes)
			}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				return summarizeDerivedText(ctx, client, srt, mode, nil)
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil || len(result.Items[0].Outputs) != 2 {
				t.Fatalf("empty publish: %+v %v", result, err)
			}
			for range 2 {
				value, err := NewInvoker().finishVideoSummary(nil, state, task.ID)
				if err != nil || value.(map[string]any)["count"] != 0 || calls.Load() != 1 {
					t.Fatalf("empty adapter rejected/replayed: value=%+v err=%v calls=%d", value, err, calls.Load())
				}
			}
		})
	}
}

func TestLegacyFrozenRecipeRemainingModeUsesRealResolver(t *testing.T) {
	service, task, client, calls, server := pointEvidenceFixture(t)
	defer server.Close()
	deps := pointEvidenceDependencies(client)
	ctx := &stopAfterLocalExport{Context: context.Background(), taskPath: service.Root + "/" + task.ID + ".json"}
	_, _ = service.Run(ctx, task.ID, deps)
	checkpoint, err := service.Read(task.ID)
	if err != nil || len(checkpoint.Items[0].Outputs) != 2 || checkpoint.Items[1].GenerationState != "" {
		t.Fatalf("legacy setup: %+v %v", checkpoint, err)
	}
	frozen := checkpoint.Items[1].Recipe.Hash
	deps.ResolveRecipes = func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSubtitleRecipesForContext(ctx, client, modes)
	}
	restarted := subtitlederived.New(service.Root, service.Library)
	result, err := restarted.Run(context.Background(), task.ID, deps)
	if err != nil || len(result.Items[1].Outputs) != 2 || calls.Load() != 2 || result.Items[1].Recipe.Hash != frozen || result.Items[1].Recipe.ModeRecipe.Version != 1 || result.Items[1].AttemptEvidence.Version != 2 {
		t.Fatalf("frozen legacy resume: %+v err=%v calls=%d", result, err, calls.Load())
	}
}
