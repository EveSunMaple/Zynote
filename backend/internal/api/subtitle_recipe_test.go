package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

// Pin the historical transport/receipt fixtures to their original recipe. The
// default production resolver is v2/v3 and has separate end-to-end span tests.
func resolveLegacySubtitleRecipes(client *ai.Client, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
	configuration, recipes, err := resolveSubtitleRecipes(client, modes)
	for mode, recipe := range recipes {
		prompt := summaryPrompt(mode, summaryMaxPoints(mode))
		sum := sha256.Sum256([]byte(prompt))
		recipe.Version = 1
		recipe.ChunkPolicy = "subtitle-text-runes"
		recipe.SystemPromptSHA256 = hex.EncodeToString(sum[:])
		recipes[mode] = recipe
	}
	return configuration, recipes, err
}

func TestSubtitleRecipeDurableBeforeActualSummaryRequest(t *testing.T) {
	state := &contract.State{LibraryRoot: t.TempDir()}
	service := subtitleDerivedService(state)
	task, err := service.CreateDirectOperation("1\n00:00:00,000 --> 00:00:02,000\nHello course\n", "Course", "brief", "", "bcbcbcbc-bcbc-4cbc-8cbc-bcbcbcbcbcbc")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		durable, err := service.Read(task.ID)
		if err != nil || durable.EvidenceVersion != 1 || durable.Recipe == nil || durable.Items[0].Recipe == nil || durable.Items[0].GenerationState != "dispatched" {
			t.Error("provider reached without durable recipe", err)
		}
		var body struct {
			Model    string       `json:"model"`
			Messages []ai.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if durable.Recipe == nil || body.Model != durable.Recipe.Configuration.RequestedModel || len(body.Messages) != 2 || body.Messages[0].Content != summaryPrompt("brief", summaryMaxPoints("brief")) {
			t.Error("actual request differs from resolver")
		}

		if len(body.Messages) == 2 && durable.Items[0].Recipe != nil {
			prompt, ok := body.Messages[0].Content.(string)
			sum := sha256.Sum256([]byte(prompt))
			if !ok || hex.EncodeToString(sum[:]) != durable.Items[0].Recipe.ModeRecipe.SystemPromptSHA256 {
				t.Error("durable prompt digest differs from actual HTTP input")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"[{\"cue\":0,\"quote\":\"Hello\",\"point\":\"Course greeting\"}]"}}]}`)
	}))
	defer server.Close()
	client := ai.New(ai.Profile{BaseURL: server.URL, Model: "local-controlled-model"})
	result, err := service.Run(context.Background(), task.ID, subtitlederived.Dependencies{
		ResolveRecipes: func(_ context.Context, modes []string) (contract.RequestedGenerationConfig, map[string]subtitlederived.ModeRecipe, error) {
			return resolveLegacySubtitleRecipes(client, modes)
		},
		Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
			result, err := summarizeVideoText(ctx, client, srt, mode, nil)
			return result.Markdown, nil, err
		},
	})
	server.Close()
	if err != nil || calls.Load() != 1 || result.Items[0].Status != "已完成" {
		t.Fatal("controlled summary failed", result, calls.Load(), err)
	}
}
