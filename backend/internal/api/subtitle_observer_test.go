package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func TestSummaryObserverActualChunksRepairAndFailureStops(t *testing.T) {
	for _, failure := range []string{"", "dispatch-intent", "http-response-received", "response-decoded", "format-result"} {
		t.Run("failure-"+failure, func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			srt := "1\n00:00:00,000 --> 00:00:02,000\nHello " + strings.Repeat("a", 15000) + "\n\n2\n00:00:02,000 --> 00:00:04,000\nHello second\n"
			task, err := service.CreateDirectOperation(srt, "Course", "brief", "", "bdbdbdbd-bdbd-4dbd-8dbd-bdbdbdbdbdbd")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			var receivedIDs [][]int
			var requestHashes []string
			var requestInputs []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Messages []ai.Message `json:"messages"`
				}
				raw, _ := io.ReadAll(r.Body)
				sum := sha256.Sum256(raw)
				requestHashes = append(requestHashes, hex.EncodeToString(sum[:]))
				if err := json.Unmarshal(raw, &request); err != nil {
					t.Error(err)
				}
				var input struct {
					Cues []summaryCue `json:"cues"`
				}
				content, _ := request.Messages[1].Content.(string)
				if err := json.NewDecoder(strings.NewReader(content)).Decode(&input); err != nil {
					t.Error(err)
				}
				inputBytes, _ := json.Marshal(input)
				requestInputs = append(requestInputs, string(inputBytes))
				ids := []int{}
				for _, cue := range input.Cues {
					if cue.Text != []string{"Hello " + strings.Repeat("a", 15000), "Hello second"}[cue.Cue] || cue.Time != []string{"00:00:00", "00:00:02"}[cue.Cue] {
						t.Error("HTTP cue text differs from original SRT")
					}
					ids = append(ids, cue.Cue)
				}
				receivedIDs = append(receivedIDs, ids)
				reply := "invalid-json"
				if calls > 1 {
					reply = fmt.Sprintf(`[{"cue":%d,"quote":"Hello","point":"Greeting"}]`, input.Cues[0].Cue)
				}
				body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": reply}}}})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "controlled-model"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipes(client, modes)
			}, Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
				if failure != "" {
					metadata, persist := contract.GenerationObservationFrom(ctx)
					ctx = contract.WithGenerationObservation(ctx, metadata, func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
						if event.State == failure {
							return fmt.Errorf("controlled observer persistence failure")
						}
						return persist(ctx, event)
					})
				}
				result, err := summarizeVideoText(ctx, client, srt, mode, nil)
				return result.Markdown, nil, err
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			if err != nil && !contract.IsObservationFailure(err) {
				t.Fatal(err)
			}
			expected := 3
			if failure != "" {
				expected = 1
			}
			if failure == "dispatch-intent" {
				expected = 0
			}
			if calls != expected {
				t.Fatal("observer failure caused request/retry", calls, expected, result.Items[0].Error)
			}
			if failure == "" {
				if result.Items[0].Status != "已完成" || result.Items[0].AttemptEvidence == nil || result.Items[0].AttemptEvidence.EventCount != 12 {
					t.Fatal(result)
				}
				files, err := filepath.Glob(filepath.Join(service.Root, "evidence", task.ID, "0", "*.json"))
				if err != nil || len(files) != 12 {
					t.Fatal(files, err)
				}
				intents := 0
				for _, path := range files {
					raw, _ := os.ReadFile(path)
					var record struct {
						Event contract.GenerationAttemptEvidence `json:"event"`
					}
					if json.Unmarshal(raw, &record) != nil {
						t.Fatal("invalid event")
					}
					if record.Event.State == "dispatch-intent" {
						intents++
						expectedRequest := record.Event.Observation.ChunkIndex*2 + record.Event.Observation.AttemptIndex
						if record.Event.Observation.ChunkIndex == 1 {
							expectedRequest = 2
						}
						if record.Event.RequestBodySHA256 != requestHashes[expectedRequest] {
							t.Fatal("actual request digest differs from sidecar")
						}

						m := record.Event.Observation
						if len(m.CueIDs) != 1 || m.CueIDs[0] != m.ChunkIndex {
							t.Fatal("cue mapping differs", m)
						}
						if m.AttemptIndex == 1 && m.FeedbackSHA256 != ai.JSONRecipePolicy().RepairFeedbackSHA256 {
							t.Fatal("repair provenance missing")
						}
					}
				}
				if intents != 3 {
					t.Fatal(intents)
				}
			} else {
				if result.Items[0].Status != "失败" || result.Items[0].GenerationState != "dispatched" {
					t.Fatal(result)
				}
				before := calls
				if _, err := service.Run(context.Background(), task.ID, deps); err != nil || calls != before {
					t.Fatal("unknown observer outcome replayed", err, calls)
				}
			}
			if failure == "" && requestInputs[0] != requestInputs[1] {
				t.Fatal("repair changed base input")
			}
			if failure == "" && (len(receivedIDs) != 3 || receivedIDs[0][0] != 0 || receivedIDs[1][0] != 0 || receivedIDs[2][0] != 1) {
				t.Fatal("actual chunk/repair inputs differ", receivedIDs)
			}
		})
	}
}

func TestObservedSummaryRejectsRedirectButOrdinaryChatKeepsIt(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer origin.Close()
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "bebebebe-bebe-4ebe-8ebe-bebebebebebe")
	if err != nil {
		t.Fatal(err)
	}
	client := ai.New(ai.Profile{BaseURL: origin.URL, Model: "model"})
	deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSubtitleRecipes(client, modes)
	}, Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
		result, err := summarizeVideoText(ctx, client, srt, mode, nil)
		return result.Markdown, nil, err
	}}
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || targetCalls != 0 || result.Items[0].AttemptEvidence == nil || result.Items[0].AttemptEvidence.EventCount != 2 {
		t.Fatal("observed redirect followed", result, targetCalls, err)
	}
	if _, err := client.Chat(context.Background(), []ai.Message{{Role: "user", Content: "ordinary"}}, nil); err != nil || targetCalls != 1 {
		t.Fatal("ordinary redirect changed", targetCalls, err)
	}
}

func TestSummaryObserverTransportAndCancellationRemainUnknown(t *testing.T) {
	for _, kind := range []string{"transport", "timeout", "cancel", "config-drift"} {
		t.Run(kind, func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			task, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "bfbfbfbf-bfbf-4fbf-8fbf-bfbfbfbfbfbf")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			cancelChannel := make(chan context.CancelFunc, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.Copy(io.Discard, r.Body)
				if kind == "cancel" {
					(<-cancelChannel)()
				}
				if kind == "timeout" || kind == "cancel" {
					<-r.Context().Done()
					return
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "model"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipes(client, modes)
			}, Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
				if kind == "config-drift" {
					client.Profile.Model = "changed-after-binding"
				}
				if kind == "timeout" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
					defer cancel()
				}
				if kind == "cancel" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					cancelChannel <- cancel
				}
				result, err := summarizeVideoText(ctx, client, srt, mode, nil)
				return result.Markdown, nil, err
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			if (err != nil && !contract.IsObservationFailure(err)) || result.Items[0].Status != "失败" {
				t.Fatal(result, err)
			}
			expected := 1
			if kind == "config-drift" {
				expected = 0
			}
			if calls != expected {
				t.Fatal("unexpected transport replay", calls)
			}
			before := calls
			if _, err := service.Run(context.Background(), task.ID, deps); err != nil || calls != before {
				t.Fatal("UNKNOWN replay", err, calls)
			}
		})
	}
}

func TestSummaryObserverResponsesWireInputAndReceipts(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "cacacaca-caca-4aca-8aca-cacacacacaca")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input  []ai.Message `json:"input"`
			Store  *bool        `json:"store"`
			Stream *bool        `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/responses" || body.Store == nil || *body.Store || body.Stream == nil || *body.Stream || len(body.Input) != 2 || body.Input[0].Content != summaryPrompt("brief", 4) {
			t.Error("invalid Responses transport", body)
		}
		var input struct {
			Cues []summaryCue `json:"cues"`
		}
		user, _ := body.Input[1].Content.(string)
		if json.Unmarshal([]byte(user), &input) != nil || len(input.Cues) != 1 || input.Cues[0].Cue != 0 || input.Cues[0].Text != "Hello course" || input.Cues[0].Time != "00:00:00" {
			t.Error("Responses wire input differs from SRT", user)
		}
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"[{\"cue\":0,\"quote\":\"Hello\",\"point\":\"Greeting\"}]"}]}]}`)
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "model", APIFormat: "responses"})
	deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
		return resolveSubtitleRecipes(client, modes)
	}, Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
		result, err := summarizeVideoText(ctx, client, srt, mode, nil)
		return result.Markdown, nil, err
	}}
	result, err := service.Run(context.Background(), task.ID, deps)
	if err != nil || calls != 1 || result.Items[0].Status != "已完成" || result.Items[0].AttemptEvidence == nil || result.Items[0].AttemptEvidence.EventCount != 4 {
		t.Fatal(result, calls, err)
	}
}

func TestObserverPersistenceFailureStopsEntireTwoModeTask(t *testing.T) {
	for _, failure := range []string{"dispatch-intent", "http-response-received", "response-decoded", "format-result"} {
		t.Run(failure, func(t *testing.T) {
			state := &contract.State{LibraryRoot: t.TempDir()}
			service := subtitleDerivedService(state)
			initial, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "cbcbcbcb-cbcb-4bcb-8bcb-cbcbcbcbcbcb")
			if err != nil {
				t.Fatal(err)
			}
			task, err := service.Create([]subtitlederived.Source{initial.Items[0].Source}, []string{"brief", "detailed"}, "")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				fmt.Fprint(w, `{"choices":[{"message":{"content":"invalid-format"}}]}`)
			}))
			defer server.Close()
			client := ai.New(ai.Profile{BaseURL: server.URL, Model: "model"})
			deps := subtitlederived.Dependencies{ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
				return resolveSubtitleRecipes(client, modes)
			}, Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
				metadata, persist := contract.GenerationObservationFrom(ctx)
				ctx = contract.WithGenerationObservation(ctx, metadata, func(ctx context.Context, event contract.GenerationAttemptEvidence) error {
					if event.State == failure {
						return fmt.Errorf("controlled evidence persistence failure")
					}
					return persist(ctx, event)
				})
				result, err := summarizeVideoText(ctx, client, srt, mode, nil)
				return result.Markdown, nil, err
			}}
			result, err := service.Run(context.Background(), task.ID, deps)
			expected := 1
			if failure == "dispatch-intent" {
				expected = 0
			}
			if !contract.IsObservationFailure(err) || calls != expected || result.Items[1].GenerationState != "" || result.Items[1].Status != "等待处理" {
				t.Fatal("persistence failure continued another item", result, calls, err)
			}
			disk, readErr := service.Read(task.ID)
			if readErr != nil || disk.Items[0].GenerationState != "dispatched" || disk.Items[1].AttemptEvidence != nil {
				t.Fatal(disk, readErr)
			}
			before := calls
			fresh := subtitlederived.New(service.Root, state.LibraryRoot)
			recovered, recoverErr := fresh.Run(context.Background(), task.ID, deps)
			if recoverErr != nil || calls != before || recovered.Items[1].GenerationState != "" {
				t.Fatal("fresh recovery skipped UNKNOWN and dispatched next mode", recovered, calls, recoverErr)
			}

		})
	}
}
