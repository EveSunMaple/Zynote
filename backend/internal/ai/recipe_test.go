package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
)

func TestTextRecipeMatchesActualRequestAndIgnoresCredentialRotation(t *testing.T) {
	for _, format := range []string{"openai", "responses"} {
		t.Run(format, func(t *testing.T) {
			calls := 0
			var received map[string]any
			var endpoint, authorization string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				endpoint, authorization = "http://"+r.Host+r.URL.RequestURI(), r.Header.Get("Authorization")
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if format == "responses" {
					fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
				}
			}))
			defer server.Close()
			temperature, topP, maxTokens, effort := 0.4, 0.8, 1200, "medium"
			client := New(Profile{BaseURL: server.URL + "/v1/", Model: "requested-model", VisionModel: "unused-vision", APIFormat: format, Key: "secret-before", Parameters: map[string]config.ModelParameters{"requested-model": {Temperature: &temperature, TopP: &topP, MaxTokens: &maxTokens, ReasoningEffort: &effort}}})
			recipe, err := client.TextRequestConfiguration()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "controlled text"}}, nil); err != nil {
				t.Fatal(err)
			}
			if recipe.EndpointSHA256 != recipeDigest(endpoint) || recipe.RequestedModel != received["model"] || received["stream"] != false {
				t.Fatal("request identity mismatch", recipe, received)
			}
			delete(received, "model")
			delete(received, "stream")
			delete(received, "messages")
			delete(received, "input")
			// store=false is the fixed Responses transport policy, not a model parameter.
			if format == "responses" && received["store"] != false {
				t.Fatal("unexpected store policy")
			}
			delete(received, "store")
			expected, _ := json.Marshal(received)
			actual, _ := json.Marshal(recipe.EffectiveParameters)
			if string(expected) != string(actual) {
				t.Fatal("effective parameters diverged", string(expected), string(actual))
			}
			if authorization != "Bearer secret-before" || calls != 1 {
				t.Fatal("request not observed")
			}
			client.Profile.Key = "secret-after"
			rotated, err := client.TextRequestConfiguration()
			if err != nil || !reflect.DeepEqual(recipe, rotated) {
				t.Fatal("credential rotation changed public recipe", err)
			}
			encoded, _ := json.Marshal(recipe)
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), server.URL) || recipe.ServerModelState != "unknown" || recipe.AccountScopeState != "unknown" {
				t.Fatal("recipe leaks or overclaims", string(encoded))
			}
		})
	}
}

func TestTextRecipeRejectsAmbiguousOrCredentialBearingEndpoints(t *testing.T) {
	for _, base := range []string{"https://example.test/v1?key=secret", "https://user:secret@example.test/v1", "https://example.test/v1#fragment", "file:///tmp/model", "https://example.test/v1?"} {
		client := New(Profile{BaseURL: base, Model: "model"})
		if _, err := client.TextRequestConfiguration(); err == nil {
			t.Fatalf("accepted invalid endpoint %q", base)
		}
	}
}
