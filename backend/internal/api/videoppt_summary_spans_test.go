package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func TestSpanSummaryInterleavedRepairUsesChunkReceipt(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	source := "1\n00:00:01,000 --> 00:00:09,000\n" + strings.Repeat("甲", 15003) + "\n"
	task, err := service.CreateDirectOperation(source, "Repair fixture", "brief", "", "d6d6d6d6-d6d6-46d6-86d6-d6d6d6d6d6d6")
	if err != nil {
		t.Fatal(err)
	}
	var calls, second atomic.Int64
	repaired := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Messages []ai.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		wire, _ := request.Messages[1].Content.(string)
		var input summaryinput.SpanInput
		if json.NewDecoder(strings.NewReader(wire)).Decode(&input) != nil {
			t.Error("bad repair input")
			return
		}
		content := `{"processing":"complete","cards":[]}`
		if input.Cues[0].CoreStart == 0 {
			select {
			case <-repaired:
			case <-r.Context().Done():
				return
			}
			time.Sleep(40 * time.Millisecond)
		} else if second.Add(1) == 1 {
			content = `{"processing":"complete","cards":null}`
		} else {
			defer close(repaired)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
	deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSpanFixtureRecipes(ctx, client, modes)
	}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		return summarizeDerivedText(ctx, client, srt, mode, nil)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := service.Run(ctx, task.ID, deps)
	if err != nil || calls.Load() != 3 || len(result.Items[0].Outputs) != 2 {
		t.Fatalf("interleaved repair %+v %v calls=%d", result, err, calls.Load())
	}
	audit := service.AuditAttemptEvidence(context.Background(), result, 0)
	if audit.State != "complete" || audit.CompleteChunks != 2 {
		t.Fatalf("bad interleaved audit %+v", audit)
	}
}

func TestSpanSummarySuccessfulCacheRecoveryWithoutProviderReplay(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:01,000 --> 00:00:09,000\n"+strings.Repeat("甲", 15003)+"\n", "Cache fixture", "brief", "", "d5d5d5d5-d5d5-45d5-85d5-d5d5d5d5d5d5")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := `{"processing":"complete","cards":[]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": body}}}})
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
	fail := true
	deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSpanFixtureRecipes(ctx, client, modes)
	}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		value, err := summarizeDerivedText(ctx, client, srt, mode, nil)
		if err == nil && fail {
			return subtitlederived.SummaryResult{}, errors.New("fixture crash after validated cache before received")
		}
		return value, err
	}}
	first, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 2 || len(first.Items[0].Outputs) != 0 || first.Items[0].GenerationState != "dispatched" {
		t.Fatalf("setup %+v %v calls=%d", first, err, calls.Load())
	}
	original := *first.Items[0].AttemptEvidence
	fail = false
	restarted := subtitlederived.New(service.Root, service.Library)
	result, err := restarted.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 2 || len(result.Items[0].Outputs) != 2 || result.ExecutionGeneration != 2 || *result.Items[0].AttemptEvidence != original {
		t.Fatalf("cache replay/change %+v %v calls=%d", result, err, calls.Load())
	}
	audit := restarted.AuditAttemptEvidence(context.Background(), result, 0)
	if audit.State != "complete" {
		t.Fatalf("adopted provenance %+v", audit)
	}
}

func TestSpanSummaryCancellationAfterValidatedReceiptKeepsSuccessCache(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:01,000 --> 00:00:02,000\n原文结论\n", "Cancel cache fixture", "brief", "", "d7d7d7d7-d7d7-47d7-87d7-d7d7d7d7d7d7")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"processing":"complete","cards":[]}`}}}})
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
	cancelAfterValidated := true
	deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSpanFixtureRecipes(ctx, client, modes)
	}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		if !cancelAfterValidated {
			return summarizeDerivedText(ctx, client, srt, mode, nil)
		}
		local, cancel := context.WithCancel(ctx)
		defer cancel()
		metadata, original := contract.GenerationObservationFrom(ctx)
		wrapped := func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
			err := original(ctx, event)
			if err == nil && event.State == "validated-points" {
				cancel()
			}
			return err
		}
		return summarizeDerivedText(contract.WithGenerationObservation(local, metadata, wrapped), client, srt, mode, nil)
	}}
	first, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 1 || len(first.Items[0].Outputs) != 0 {
		t.Fatalf("cancel setup %+v %v", first, err)
	}
	cancelAfterValidated = false
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls.Load() != 1 || len(result.Items[0].Outputs) != 2 {
		t.Fatalf("lost paid success after durable receipt %+v %v calls=%d", result, err, calls.Load())
	}
}

func TestSpanSummaryDurablePayloadFaultBoundariesRecoverWithoutHTTP(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("afterValidated=%v", after), func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			task, err := service.CreateDirectOperation("1\n00:00:01,000 --> 00:00:02,000\n原文结论\n", "Fault fixture", "brief", "", "d8d8d8d8-d8d8-48d8-88d8-d8d8d8d8d8d8")
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"processing":"complete","cards":[{"cue":0,"quote":"原文","point":"原文结论"}]}`}}}})
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
			inject := true
			deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSpanFixtureRecipes(ctx, client, modes)
			}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				if !inject {
					return summarizeDerivedText(ctx, client, srt, mode, nil)
				}
				metadata, original := contract.GenerationObservationFrom(ctx)
				wrapped := func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
					if event.State == "validated-points" {
						if after {
							if err := original(ctx, event); err != nil {
								return err
							}
						}
						return errors.New("fixture fault at durable payload / validated link boundary")
					}
					return original(ctx, event)
				}
				return summarizeDerivedText(contract.WithGenerationObservation(ctx, metadata, wrapped), client, srt, mode, nil)
			}}
			first, err := service.Run(context.Background(), task.ID, deps)
			if !contract.IsObservationFailure(err) || calls.Load() != 1 || len(first.Items[0].Outputs) != 0 {
				t.Fatalf("fault setup %+v %v", first, err)
			}
			inject = false
			restarted := subtitlederived.New(service.Root, service.Library)
			result, err := restarted.Run(context.Background(), task.ID, deps)
			if err != nil || calls.Load() != 1 || len(result.Items[0].Outputs) != 2 {
				t.Fatalf("payload recovery %+v %v calls=%d", result, err, calls.Load())
			}
			audit := restarted.AuditAttemptEvidence(context.Background(), result, 0)
			if audit.State != "complete" {
				t.Fatalf("recovery audit %+v", audit)
			}
		})
	}
}

func TestSpanSummarySlidingWindowOrderedPublication(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	var source strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&source, "%d\n00:00:%02d,000 --> 00:00:%02d,000\n第%d核心%s\n\n", i+1, i+1, i+2, i, strings.Repeat("甲", 14996))
	}
	task, err := service.CreateDirectOperation(source.String(), "Sliding fixture", "brief", "", "d4d4d4d4-d4d4-44d4-84d4-d4d4d4d4d4d4")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	four := make(chan struct{})
	var calls, active, maximum atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		count := calls.Add(1)
		if count == 4 {
			close(four)
		}
		var request struct {
			Messages []ai.Message `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			t.Error("bad request")
			return
		}
		wire, _ := request.Messages[1].Content.(string)
		var input summaryinput.SpanInput
		if json.Unmarshal([]byte(wire), &input) != nil || len(input.Cues) != 1 {
			t.Error("bad chunk")
			return
		}
		cue := input.Cues[0]
		if cue.Cue == 0 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		quote := fmt.Sprintf("第%d核心", cue.Cue)
		body, _ := json.Marshal(map[string]any{"processing": "complete", "cards": []any{map[string]any{"cue": cue.Cue, "quote": quote, "point": fmt.Sprintf("结论%d", cue.Cue)}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "synthetic-fixture"})
	deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSpanFixtureRecipes(ctx, client, modes)
	}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		return summarizeDerivedText(ctx, client, srt, mode, nil)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type outcome struct {
		task subtitlederived.Task
		err  error
	}
	done := make(chan outcome, 1)
	go func() { value, err := service.Run(ctx, task.ID, deps); done <- outcome{value, err} }()
	select {
	case <-four:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("scheduler did not slide past slow front")
	}
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 4 {
		t.Errorf("window exceeded: %d", calls.Load())
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	item := result.task.Items[0]
	if maximum.Load() > 2 || calls.Load() != 6 || len(item.Outputs) != 2 {
		t.Fatalf("active=%d calls=%d item=%+v", maximum.Load(), calls.Load(), item)
	}
	audit := service.AuditAttemptEvidence(context.Background(), result.task, 0)
	if audit.State != "complete" || audit.CompleteChunks != 6 {
		t.Fatalf("audit=%+v", audit)
	}
	body, err := os.ReadFile(item.Outputs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for i := 0; i < 6; i++ {
		at := strings.Index(string(body), fmt.Sprintf("结论%d", i))
		if at <= last {
			t.Fatalf("publication out of source order at %d", i)
		}
		last = at
	}
}

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
				calls.Add(1)
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
				if cue.CoreStart == 0 {
					if cue.CoreStart != 0 || cue.CoreEnd != 15000 || len([]rune(cue.Text)) != 15000 {
						t.Error("bad first core")
					}
				}
				if cue.CoreStart == 15000 {
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
				return resolveSpanFixtureRecipes(context.Background(), client, modes)
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
				return resolveSpanFixtureRecipes(ctx, client, modes)
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
		return resolveSpanFixtureRecipes(ctx, client, modes)
	}
	restarted := subtitlederived.New(service.Root, service.Library)
	result, err := restarted.Run(context.Background(), task.ID, deps)
	if err != nil || len(result.Items[1].Outputs) != 2 || calls.Load() != 2 || result.Items[1].Recipe.Hash != frozen || result.Items[1].Recipe.ModeRecipe.Version != 1 || result.Items[1].AttemptEvidence.Version != 2 {
		t.Fatalf("frozen legacy resume: %+v err=%v calls=%d", result, err, calls.Load())
	}
}

func resolveSpanFixtureRecipes(ctx context.Context, client *ai.Client, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
	versions := map[string]int{}
	for _, mode := range modes {
		versions[mode] = 2
		if contract.SummaryRecipeVersion(ctx, mode) == 1 {
			versions[mode] = 1
		}
	}
	return resolveSubtitleRecipesForContext(contract.WithSummaryRecipeVersions(ctx, versions), client, modes)
}
