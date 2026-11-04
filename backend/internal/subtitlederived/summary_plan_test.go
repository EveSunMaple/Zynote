package subtitlederived

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

func TestBoundaryPlanInputAndWindowLimitsFallbackAndTamper(t *testing.T) {
	for _, scenario := range []string{"input_limit", "window_limit"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			service := New(root, root)
			srt := "1\n00:00:00,000 --> 00:01:00,000\n" + strings.Repeat("。", 600) + "\n"
			limit := 2
			if scenario == "input_limit" {
				var source strings.Builder
				for i := 1; i <= 6000; i++ {
					fmt.Fprintf(&source, "%d\n%02d:%02d:%02d,000 --> %02d:%02d:%02d,000\n。\n\n", i, i/3600, (i/60)%60, i%60, (i+1)/3600, ((i+1)/60)%60, (i+1)%60)
				}
				srt = source.String()
				limit = 10000
			}
			task := Task{ID: "e4e4e4e4-e4e4-44e4-84e4-e4e4e4e4e4e4", Items: []Item{{Mode: "brief", Source: Source{Hash: strings.Repeat("a", 64)}, Recipe: &ItemRecipe{Hash: strings.Repeat("b", 64), ModeRecipe: ModeRecipe{Version: 4, PlanPolicy: "semantic-candidate-v1", ChunkLimit: limit}}}}}
			item := &task.Items[0]
			chunks, err := service.ensureSummaryPlan(context.Background(), &task, item, Dependencies{}, srt)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "input_limit" && item.SummaryPlan.Windows[0].State != "fallback_input_limit" {
				t.Fatal("oversized proposal input not explicit fallback")
			}
			if scenario == "window_limit" && (len(chunks) != 300 || item.SummaryPlan.Windows[256].State != "fallback_window_limit") {
				t.Fatal("proposal window budget not bounded")
			}
			segments, _ := documents.ParseSrt(srt)
			item.SummaryPlan.Windows[0].Choice = 1
			item.SummaryPlan.Digest = planDigest(*item.SummaryPlan)
			if _, err := service.validatedSummaryPlan(context.Background(), task, item, segments); err == nil {
				t.Fatal("rehashed forged boundary accepted")
			}
			item.SummaryPlan.Windows[0].Choice = -1
			item.SummaryPlan.SourceSHA256 = strings.Repeat("c", 64)
			item.SummaryPlan.Digest = planDigest(*item.SummaryPlan)
			if _, err := service.validatedSummaryPlan(context.Background(), task, item, segments); err == nil {
				t.Fatal("rehashed plan source replacement accepted")
			}
		})
	}
}

func TestRehashedUnknownBoundaryCannotBeDurableFallbackCompleteOrPrefix(t *testing.T) {
	segments := []documents.Segment{{Text: strings.Repeat("证据。", 20), Start: 0, End: 1000}}
	base, _ := summaryinput.PlanSpans("brief", segments, 30)
	for _, complete := range []bool{false, true} {
		for _, state := range []string{"fallback_budget", "fallback_unavailable", "fallback_no_candidates"} {
			service := New(t.TempDir(), t.TempDir())
			task := Task{Items: []Item{{Mode: "brief", Source: Source{Hash: strings.Repeat("a", 64)}, Recipe: &ItemRecipe{Hash: strings.Repeat("b", 64), ModeRecipe: ModeRecipe{Version: 4, PlanPolicy: "semantic-candidate-v1", ChunkLimit: 30, BoundaryPromptSHA256: strings.Repeat("c", 64)}}, Stages: map[string]*AttemptEvidenceRef{}}}}
			item := &task.Items[0]
			input := summaryinput.BoundaryCandidates(base[0].Input, 30)
			encoded, _ := summaryglobal.Encode(input)
			key := stageIdentity("summary-boundary", 0, summarytree.Digest(encoded), item.Recipe.ModeRecipe.BoundaryPromptSHA256, item.Source.Hash, item.Recipe.Hash)
			item.Stages[key] = &AttemptEvidenceRef{Version: 4, EventCount: 2, HeadSHA256: strings.Repeat("d", 64)}
			plan := &SummaryPlan{Version: 1, SourceSHA256: item.Source.Hash, RecipeSHA256: item.Recipe.Hash, Complete: complete, Windows: []BoundaryWindow{{State: state, Choice: -1}}}
			if complete {
				for len(plan.Windows) < len(base) {
					plan.Windows = append(plan.Windows, BoundaryWindow{State: "fallback_unavailable", Choice: -1})
				}
			}
			plan.Digest = planDigest(*plan)
			item.SummaryPlan = plan
			if _, err := service.validateSummaryPlan(context.Background(), task, item, segments, complete); err == nil {
				t.Fatalf("unknown stage accepted as %s complete=%v", state, complete)
			}
		}
	}
}
