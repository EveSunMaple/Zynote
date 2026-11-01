package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func TestSubtitleProjectionBoundsAddedMetadataAt4000Items(t *testing.T) {
	modeRecipe, err := summaryModeRecipe("detailed")
	if err != nil {
		t.Fatal(err)
	}
	task := subtitlederived.Task{ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", EvidenceVersion: 1, Recipe: &subtitlederived.TaskRecipe{Version: 1, Configuration: contract.RequestedGenerationConfig{RequestedModel: strings.Repeat("m", 1024)}}, Items: make([]subtitlederived.Item, 4000)}
	for i := range task.Items {
		task.Items[i] = subtitlederived.Item{Source: subtitlederived.Source{URL: "https://www.bilibili.com/video/BV1xx411c7mD/?p=1", Title: strings.Repeat("课", 200), Path: strings.Repeat("p", 200), Hash: strings.Repeat("a", 64), Generation: "g1"}, Mode: "detailed", Status: "等待处理", Recipe: &subtitlederived.ItemRecipe{Version: 1, TaskRecipeHash: strings.Repeat("b", 64), SourceSHA256: strings.Repeat("a", 64), ModeRecipe: modeRecipe, Hash: strings.Repeat("c", 64)}, AttemptEvidence: &subtitlederived.AttemptEvidenceRef{Version: 2, HeadSHA256: strings.Repeat("d", 64), EventCount: 180000}}
	}

	for i := range task.Items {
		facts := &contract.SummaryEvidence{Version: 1, SourceSHA256: strings.Repeat("a", 64), ItemRecipeHash: strings.Repeat("c", 64), FinalPointsSHA256: strings.Repeat("e", 64), PointCount: 280000, ValidatedHead: contract.GenerationEventRef{Version: 2, Sequence: 180000, SHA256: strings.Repeat("d", 64)}}
		task.Items[i].SummaryEvidence = facts
		task.Items[i].PublicationPlan = &subtitlederived.PublicationRef{Version: 1, SHA256: strings.Repeat("f", 64)}
		task.Items[i].PublicationReceipt = &subtitlederived.PublicationRef{Version: 1, SHA256: strings.Repeat("a", 64)}
		task.Items[i].Received = &subtitlederived.ReceivedSummary{Version: 1, Markdown: strings.Repeat("received-only", 100), Points: []subtitlederived.Point{{Cue: 0, Quote: "Original", Point: "Result"}}, Facts: facts}
		task.Items[i].Prepared = &subtitlederived.Prepared{Markdown: "# Existing prepared shape", Points: []subtitlederived.Point{{Cue: 0, Quote: "Original", Point: "Result"}}, Evidence: facts}
	}
	projected := projectSubtitleTask(task)
	if projected.Items[0].PublicationRecord == nil || projected.Items[0].PublicationRecord.State != "recorded" || projected.Items[0].PublicationPlan != nil || projected.Items[0].PublicationReceipt != nil {
		t.Fatal("publication metadata projection failed")
	}
	if task.Recipe == nil || task.Items[0].Recipe == nil || projected.Recipe != nil || projected.Items[0].Recipe != nil {
		t.Fatal("projection mutated durable object or retained recipe")
	}
	if task.Items[0].Received == nil || projected.Items[0].Received != nil || task.Items[0].Prepared.Evidence == nil || projected.Items[0].Prepared.Evidence != nil || projected.Items[0].Prepared.Markdown != task.Items[0].Prepared.Markdown {
		t.Fatal("received/proof projection mutated or enlarged existing prepared")
	}
	baseline := projectSubtitleTask(projected)
	for i := range baseline.Items {
		baseline.Items[i].AttemptEvidence = nil
		baseline.Items[i].SummaryEvidence = nil
	}
	baseline.EvidenceVersion = 0
	base, _ := json.Marshal(baseline)
	output, _ := json.Marshal(projected)
	full, _ := json.Marshal(task)
	if len(output)-len(base) > 4000*1200+100 || len(output) >= len(full) || strings.Contains(string(output), "systemPromptSHA256") || strings.Contains(string(output), "cueIDs") || strings.Contains(string(output), "received-only") {
		t.Fatal("added metadata unbounded", len(base), len(output), len(full))
	}
	t.Logf("4000 items: baseline=%d projected=%d full=%d added=%d", len(base), len(output), len(full), len(output)-len(base))
}
func TestListAndQueuedResumeProjectWithoutMutatingDurableRecipe(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "cdcdcdcd-cdcd-4dcd-8dcd-cdcdcdcdcdcd")
	if err != nil {
		t.Fatal(err)
	}
	task.Recipe = &subtitlederived.TaskRecipe{Version: 1}
	task.Items[0].Recipe = &subtitlederived.ItemRecipe{Version: 1}
	task.Items[0].Prepared = &subtitlederived.Prepared{Markdown: "# Saved", Points: []subtitlederived.Point{{Cue: 0, Quote: "Hello", Point: "Greeting"}}}
	body, _ := json.Marshal(task)
	if err := os.WriteFile(filepath.Join(service.Root, task.ID+".json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	inv := NewInvoker()
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	value, err := inv.handleSubtitleDerivedResume(nil, state, params)
	if err != nil {
		t.Fatal(err)
	}
	resumed, ok := value.(subtitlederived.Task)
	if !ok || resumed.Recipe != nil || resumed.Items[0].Recipe != nil {
		t.Fatal("shared start/resume/direct result boundary not projected", value)
	}
	value, err = inv.handleSubtitleDerivedList(state)
	if err != nil {
		t.Fatal(err)
	}
	listed := value.([]subtitlederived.Task)
	if len(listed) != 1 || listed[0].Recipe != nil || listed[0].Items[0].Recipe != nil {
		t.Fatal("list not projected")
	}
	durable, err := service.Read(task.ID)
	if err != nil || durable.Recipe == nil || durable.Items[0].Recipe == nil {
		t.Fatal("projection changed persistence", err)
	}
}
