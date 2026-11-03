package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

// Opt-in only. The launcher supplies the key in the child environment; no
// configuration, request headers, raw provider errors or credentials are saved.
func TestRealDeepSeekSummaryProfiles(t *testing.T) {
	runRealDeepSeekSummaryProfiles(t, false)
}

func TestRealDeepSeekMultiChunkProfiles(t *testing.T) {
	runRealDeepSeekSummaryProfiles(t, true)
}

func TestPublicFullCourseChunkPlan(t *testing.T) {
	body, err := os.ReadFile("F:/BiliSum-dependencies/zynote-acceptance/full-course-20261031/mit-computation-full.srt")
	if os.IsNotExist(err) {
		t.Skip("public fixture not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	segments, err := documents.ParseSrt(string(body))
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := summaryinput.PlanSpans("brief", segments, summaryChunkLimit)
	if err != nil || len(chunks) < 2 {
		t.Fatal("fixture does not exercise multiple chunks", err)
	}
	t.Logf("public full-course source bytes=%d cues=%d chunks=%d", len(body), len(segments), len(chunks))
}

func runRealDeepSeekSummaryProfiles(t *testing.T, multi bool) {
	key := os.Getenv("ZYNOTE_REAL_API_KEY")
	if os.Getenv("ZYNOTE_REAL_PROVIDER_TEST") != "1" || key == "" {
		t.Skip("real provider test requires explicit ephemeral launcher authorization")
	}
	sourcePath := "F:/BiliSum-dependencies/zynote-acceptance/mit-computation-long.srt"
	if multi {
		sourcePath = "F:/BiliSum-dependencies/zynote-acceptance/full-course-20261031/mit-computation-full.srt"
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal("authorized public fixture unavailable")
	}
	sourceHash := sha256.Sum256(source)
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal("report timezone unavailable")
	}
	reportRoot := filepath.Join("F:/BiliSum/deliverables/zynote-evidence-pipeline-v21-implementation-20261103", "real-deepseek-flash-"+time.Now().In(zone).Format("20060102-150405"))
	if os.MkdirAll(reportRoot, 0700) != nil {
		t.Fatal("isolated report directory unavailable")
	}
	temperature := 0.0
	maxTokens := 8192
	client := ai.New(ai.Profile{BaseURL: "https://api.deepseek.com", Model: "deepseek-flash", Key: key, APIFormat: "chat", Parameters: map[string]config.ModelParameters{"deepseek-flash": {Temperature: &temperature, MaxTokens: &maxTokens}}})
	type resultRow struct {
		Profile                                               string
		RecipeVersion                                         int
		ElapsedMS                                             int64
		Success                                               bool
		PointCount, CompleteChunks, TotalChunks, GlobalStages int
		Partial                                               bool
		PhysicalAttempts                                      int64
		TokenUsage                                            any
		Failure                                               string `json:"failure,omitempty"`
	}
	results := []resultRow{}
	defer func() {
		body, _ := json.MarshalIndent(map[string]any{"sourcePath": sourcePath, "sourceSHA256": hex.EncodeToString(sourceHash[:]), "sourceBytes": len(source), "provider": "https://api.deepseek.com", "requestedModel": "deepseek-flash", "requestedModelSnapshot": "deepseek-flash alias; latest official documentation snapshot Oct17 V4.1-Flash-1017; actual server snapshot not captured", "timezone": "America/New_York", "runs": results, "measurementLimit": "one run per profile, exploratory; no p95, speedup or quality non-inferiority claim; token usage unknown"}, "", "  ")
		_ = os.WriteFile(filepath.Join(reportRoot, "report.json"), body, 0600)
	}()
	for _, profile := range []struct {
		name    string
		version int
	}{{"legacy-sequential", 1}, {"parallel-core", 2}, {"experimental-global-risk", 3}} {
		row := resultRow{Profile: profile.name, RecipeVersion: profile.version}
		state := &contract.State{LibraryRoot: t.TempDir()}
		service := subtitleDerivedService(state)
		task, err := service.CreateDirectOperation(string(source), "MIT public transcript fixture", "brief", "", "e1e1e1e1-e1e1-41e1-81e1-e1e1e1e1e1e1")
		if err != nil {
			row.Failure = "fixture_task_create"
			results = append(results, row)
			t.Fatal("real fixture task creation failed")
		}
		deps := subtitlederived.Dependencies{ResolveRecipes: func(ctx context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
			versions := map[string]int{}
			for _, mode := range modes {
				versions[mode] = profile.version
			}
			return resolveSubtitleRecipesForContext(contract.WithSummaryRecipeVersions(ctx, versions), client, modes)
		}, SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
			return summarizeDerivedText(ctx, client, srt, mode, nil)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		started := time.Now()
		value, runErr := service.Run(ctx, task.ID, deps)
		row.ElapsedMS = time.Since(started).Milliseconds()
		cancel()
		if runErr != nil || len(value.Items) != 1 || len(value.Items[0].Outputs) != 2 {
			row.Failure = "provider_or_evidence_failure"
			results = append(results, row)
			t.Fatal("real profile did not reach verified publication; raw provider error suppressed")
		}
		item := value.Items[0]
		audit := service.AuditAttemptEvidence(context.Background(), value, 0)
		row.Success = audit.State == "complete"
		row.CompleteChunks = audit.CompleteChunks
		row.TotalChunks = audit.TotalChunks
		if multi && row.TotalChunks < 2 {
			t.Fatal("authorized fixture failed to exercise multiple chunks")
		}
		if item.SummaryEvidence != nil {
			row.PointCount = item.SummaryEvidence.PointCount
			row.Partial = item.SummaryEvidence.Partial
			row.GlobalStages = item.SummaryEvidence.GlobalStages
		}
		ledger, openErr := generationjob.Open(filepath.Join(service.Root, "job-state"))
		if openErr == nil {
			budget, budgetErr := ledger.Budget(context.Background(), task.ID)
			if budgetErr == nil {
				row.PhysicalAttempts = budget.Spent + budget.UnknownHeld + budget.Reserved
			}
			_ = ledger.Close()
		}
		for i, output := range item.Outputs {
			body, readErr := os.ReadFile(output.Path)
			if readErr != nil {
				row.Success = false
				row.Failure = "artifact_read"
				break
			}
			extension := filepath.Ext(output.Path)
			if os.WriteFile(filepath.Join(reportRoot, profile.name+"-"+string(rune('0'+i))+extension), body, 0600) != nil {
				row.Success = false
				row.Failure = "artifact_write"
				break
			}
		}
		results = append(results, row)
		if !row.Success {
			t.Fatal("real profile evidence or artifact verification failed")
		}
	}
}
