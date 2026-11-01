package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

type receivedBlockContext struct {
	context.Context
	taskPath string
	blocker  string
	once     sync.Once
	blocked  atomic.Bool
}

func (value *receivedBlockContext) Err() error {
	raw, err := os.ReadFile(value.taskPath)
	if err == nil {
		var task subtitlederived.Task
		if json.Unmarshal(raw, &task) == nil && len(task.Items) > 0 && task.Items[0].Received != nil && task.Items[0].Prepared == nil {
			value.once.Do(func() {
				if os.Mkdir(value.blocker, 0700) == nil {
					value.blocked.Store(true)
				}
			})
		}
	}
	return value.Context.Err()
}

type stopAfterLocalExport struct {
	context.Context
	taskPath string
}

func (value *stopAfterLocalExport) Err() error {
	raw, err := os.ReadFile(value.taskPath)
	if err == nil {
		var task subtitlederived.Task
		if json.Unmarshal(raw, &task) == nil && len(task.Items) > 0 && len(task.Items[0].Outputs) > 0 {
			return context.Canceled
		}
	}
	return value.Context.Err()
}

func pointEvidenceFixture(t *testing.T) (*subtitlederived.Service, subtitlederived.Task, *ai.Client, *atomic.Int32, *httptest.Server) {
	t.Helper()
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	initial, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "d0d0d0d0-d0d0-40d0-80d0-d0d0d0d0d0d0")
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.Create([]subtitlederived.Source{initial.Items[0].Source}, []string{"brief", "detailed"}, "")
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"[{\"cue\":0,\"quote\":\"Hello\",\"point\":\"First greeting\"},{\"cue\":0,\"quote\":\"Hello\",\"point\":\"Second greeting\"}]"}}]}`)
	}))
	return service, task, ai.New(ai.Profile{BaseURL: server.URL, Model: "controlled-model"}), calls, server
}
func pointEvidenceDependencies(client *ai.Client) subtitlederived.Dependencies {
	return subtitlederived.Dependencies{
		ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
			return resolveSubtitleRecipes(client, modes)
		},
		SummarizeWithEvidence: func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
			return summarizeDerivedText(ctx, client, srt, mode, nil)
		},
	}
}
func noModelOnRecovery(t *testing.T) subtitlederived.Dependencies {
	t.Helper()
	return subtitlederived.Dependencies{ResolveRecipes: func(context.Context, []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		t.Error("recovery read model profile")
		return contract.RequestedGenerationConfig{}, nil, fmt.Errorf("forbidden")
	}, ValidateModel: func(context.Context) error {
		t.Error("recovery validated current model")
		return fmt.Errorf("forbidden")
	}, SummarizeWithEvidence: func(context.Context, string, string) (subtitlederived.SummaryResult, error) {
		t.Error("recovery replayed provider")
		return subtitlederived.SummaryResult{}, fmt.Errorf("forbidden")
	}}
}
func TestPointEvidenceRejectsMutatedRowsEvenWithRecomputedFinalFacts(t *testing.T) {
	for _, change := range []string{"point", "quote", "topic", "time", "cue", "drop", "duplicate", "order", "scope", "head", "nil-facts"} {
		t.Run(change, func(t *testing.T) {
			service, task, client, calls, server := pointEvidenceFixture(t)
			defer server.Close()
			deps := pointEvidenceDependencies(client)
			deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
				result, err := summarizeDerivedText(ctx, client, srt, mode, nil)
				if err != nil {
					return result, err
				}
				switch change {
				case "point":
					result.Points[0].Point = "Changed"
				case "quote":
					result.Points[0].Quote = "course"
				case "topic":
					result.Points[0].Topic = "Other"
				case "time":
					result.Points[0].Timestamp = "00:00:01"
				case "cue":
					result.Points[0].Cue = 1
				case "drop":
					result.Points = result.Points[:1]
				case "duplicate":
					result.Points = append(result.Points, result.Points[0])
				case "order":
					result.Points[0], result.Points[1] = result.Points[1], result.Points[0]
				case "scope":
					result.Facts.SourceSHA256 = "wrong"
				case "head":
					result.Facts.ValidatedHead.SHA256 = "wrong"
				case "nil-facts":
					result.Facts = nil
				}
				if result.Facts != nil {
					rows := make([]contract.ValidatedSummaryPoint, 0, len(result.Points))
					for _, point := range result.Points {
						rows = append(rows, contract.ValidatedSummaryPoint{Cue: int(point.Cue), Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp})
					}
					result.Facts.FinalPointsSHA256, _ = contract.SummaryPointsDigest(rows)
					result.Facts.PointCount = len(rows)
				}
				return result, nil
			}
			result, err := service.Run(context.Background(), task.ID, deps)
			server.Close()
			if !contract.IsObservationFailure(err) || calls.Load() != 1 || result.Items[0].Received == nil || result.Items[0].Prepared != nil || len(result.Items[0].Outputs) != 0 || result.Items[1].GenerationState != "" {
				t.Fatal("unverified received result exported or advanced", change, result, calls.Load(), err)
			}
			fresh := subtitlederived.New(service.Root, service.Library)
			recovered, recoverErr := fresh.Run(context.Background(), task.ID, noModelOnRecovery(t))
			if !contract.IsObservationFailure(recoverErr) || recovered.Items[0].Received == nil || len(recovered.Items[0].Outputs) != 0 || calls.Load() != 1 {
				t.Fatal("unverified recovery bypassed evidence", recovered, recoverErr)
			}
		})
	}
}
func TestReceivedAndPreparedSaveFailureKeepCheckpointAndStableItemPointers(t *testing.T) {
	for _, failure := range []string{"received-save", "prepared-save"} {
		t.Run(failure, func(t *testing.T) {
			service, task, client, calls, server := pointEvidenceFixture(t)
			defer server.Close()
			deps := pointEvidenceDependencies(client)
			taskPath := filepath.Join(service.Root, task.ID+".json")
			blocker := taskPath + ".tmp"
			var ctx context.Context = context.Background()
			if failure == "received-save" {
				deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
					result, err := summarizeDerivedText(ctx, client, srt, mode, nil)
					if err == nil {
						if err := os.Mkdir(blocker, 0700); err != nil {
							t.Fatal(err)
						}
					}
					return result, err
				}
			} else {
				ctx = &receivedBlockContext{Context: context.Background(), taskPath: taskPath, blocker: blocker}
			}
			result, err := service.Run(ctx, task.ID, deps)
			server.Close()
			if err == nil || calls.Load() != 1 || result.Items[0].Prepared != nil || result.Items[1].GenerationState != "" {
				t.Fatal("checkpoint save failure continued or mutated prepared", result, err, calls.Load())
			}
			disk, readErr := service.Read(task.ID)
			if readErr != nil || disk.Items[0].Prepared != nil {
				t.Fatal(disk, readErr)
			}
			if failure == "received-save" && disk.Items[0].Received != nil {
				t.Fatal("failed received save left fake checkpoint")
			}
			if failure == "prepared-save" && (disk.Items[0].Received == nil || !ctx.(*receivedBlockContext).blocked.Load()) {
				t.Fatal("received checkpoint lost", disk)
			}
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			fresh := subtitlederived.New(service.Root, service.Library)
			// Recover only the already received item; cancellation after its local
			// export prevents the intentionally pending second mode from new work.
			recovery := noModelOnRecovery(t)
			recovered, recoverErr := fresh.Run(&stopAfterLocalExport{Context: context.Background(), taskPath: taskPath}, task.ID, recovery)
			if failure == "prepared-save" && errors.Is(recoverErr, context.Canceled) {
				recovered, readErr = fresh.Read(task.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				recoverErr = nil
			}
			if failure == "received-save" {
				if recoverErr != nil || recovered.Items[0].Received != nil || len(recovered.Items[0].Outputs) != 0 || recovered.Items[1].GenerationState != "" {
					t.Fatal("lost receive replayed", recovered, recoverErr)
				}
			} else {
				if recoverErr != nil || recovered.Items[0].Status != "已完成" || len(recovered.Items[0].Outputs) != 2 || recovered.Items[0].Received != nil || recovered.Items[0].SummaryEvidence == nil {
					t.Fatal("local received recovery/pointers failed", recovered, recoverErr)
				}
				// Second mode may fail local profile validation, but no provider is called.
			}
		})
	}
}
func TestProductionSummaryAdapterBindsDuplicateCueRowsWithoutReordering(t *testing.T) {
	service, task, client, calls, server := pointEvidenceFixture(t)
	defer server.Close()
	result, err := service.Run(context.Background(), task.ID, pointEvidenceDependencies(client))
	server.Close()
	if err != nil || calls.Load() != 2 {
		t.Fatal(result, err, calls.Load())
	}
	for _, item := range result.Items {
		if item.Status != "已完成" || item.SummaryEvidence == nil || item.SummaryEvidence.PointCount != 2 || item.Received != nil || len(item.Outputs) != 2 {
			t.Fatal("adapter lost point evidence", item)
		}
		var points []subtitlederived.Point
		for _, output := range item.Outputs {
			if filepath.Ext(output.Path) == ".json" {
				raw, _ := os.ReadFile(output.Path)
				if json.Unmarshal(raw, &points) != nil {
					t.Fatal("invalid exported points")
				}
			}
		}
		if len(points) != 2 || points[0].Cue != 0 || points[1].Cue != 0 || points[0].Point != "First greeting" || points[1].Point != "Second greeting" || points[0].Anchor == nil {
			t.Fatal("adapter reordered or deduplicated existing cue semantics", points)
		}
	}
}

func TestPointEvidenceDistinguishesEmptyVerifiedChunkFromUnobserved(t *testing.T) {
	for _, allEmpty := range []bool{false, true} {
		t.Run(fmt.Sprint(allEmpty), func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			srt := "1\n00:00:00,000 --> 00:00:02,000\nHello " + strings.Repeat("a", 15000) + "\n\n2\n00:00:02,000 --> 00:00:04,000\nHello second\n"
			task, err := service.CreateDirectOperation(srt, "Course", "brief", "", "d1d1d1d1-d1d1-41d1-81d1-d1d1d1d1d1d1")
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				quote := "not present"
				cue := 0
				if call == 2 {
					cue = 1
					if !allEmpty {
						quote = "Hello"
					}
				}
				reply := fmt.Sprintf(`[{"cue":%d,"quote":%q,"point":"Greeting"}]`, cue, quote)
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "model"})
			result, err := service.Run(context.Background(), task.ID, pointEvidenceDependencies(client))
			server.Close()
			if err != nil || calls.Load() != 2 {
				t.Fatal(result, err, calls.Load())
			}
			audit := service.AuditAttemptEvidence(context.Background(), result, 0)
			if audit.State != "complete" || audit.CheckedEvents != 10 {
				t.Fatal("zero chunk lost validated record", audit)
			}
			if allEmpty {
				if result.Items[0].Status != "失败" || result.Items[0].Received != nil || result.Items[0].SummaryEvidence != nil || len(result.Items[0].Outputs) != 0 {
					t.Fatal("whole empty result changed old failure behavior", result)
				}
			} else if result.Items[0].Status != "已完成" || result.Items[0].SummaryEvidence == nil || result.Items[0].SummaryEvidence.PointCount != 1 {
				t.Fatal("empty chunk confused with unknown", result)
			}
		})
	}
}
func TestValidatedPointEventCannotReferenceOtherFormatReceipt(t *testing.T) {
	service, task, client, calls, server := pointEvidenceFixture(t)
	defer server.Close()
	deps := pointEvidenceDependencies(client)
	deps.SummarizeWithEvidence = func(ctx context.Context, srt, mode string) (subtitlederived.SummaryResult, error) {
		metadata, persist := contract.GenerationObservationFrom(ctx)
		ctx = contract.WithGenerationObservation(ctx, metadata, func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
			if event.State == "validated-points" {
				copy := *event.ValidatedPoints
				copy.AcceptedFormatRef.Sequence--
				event.ValidatedPoints = &copy
			}
			return persist(ctx, event)
		})
		return summarizeDerivedText(ctx, client, srt, mode, nil)
	}
	result, err := service.Run(context.Background(), task.ID, deps)
	server.Close()
	if !contract.IsObservationFailure(err) || calls.Load() != 1 || result.Items[0].Received != nil || result.Items[1].GenerationState != "" {
		t.Fatal("forged response linkage accepted", result, err, calls.Load())
	}
}
