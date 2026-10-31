package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func TestObserverDirectChatCannotDetachInputOrFeedback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"[]"}}]}`)
	}))
	defer server.Close()
	client := New(Profile{BaseURL: server.URL, Model: "model"})
	base := `{"cues":[{"cue":0,"text":"original"}]}`
	for _, bad := range []string{"base", "cue", "retry-suffix", "retry-base", "attempt", "stream"} {
		t.Run(bad, func(t *testing.T) {
			metadata := contract.GenerationObservation{Purpose: "subtitle-summary", InputSHA256: recipeDigest(base), CueIDs: []int{0}, AttemptIndex: 0, FeedbackSHA256: recipeDigest("")}
			user := base
			switch bad {
			case "base":
				user = `{"cues":[{"cue":0,"text":"changed"}]}`
			case "cue":
				metadata.CueIDs = []int{1}
			case "retry-suffix":
				metadata.AttemptIndex = 1
				metadata.FeedbackSHA256 = recipeDigest(jsonRepairFeedback)
			case "retry-base":
				metadata.AttemptIndex = 1
				metadata.FeedbackSHA256 = recipeDigest(jsonRepairFeedback)
				user = `{"cues":[{"cue":0,"text":"changed"}]}` + jsonRepairFeedback
			case "attempt":
				metadata.AttemptIndex = 2
			}
			observed := 0
			ctx := contract.WithGenerationObservation(context.Background(), metadata, func(context.Context, contract.GenerationAttemptEvidence) error { observed++; return nil })
			var onText func(string)
			if bad == "stream" {
				onText = func(string) {}
			}
			if _, err := client.Chat(ctx, []Message{{Role: "system", Content: "prompt"}, {Role: "user", Content: user}}, onText); err == nil || calls.Load() != 0 || observed != 0 {
				t.Fatal("detached observed input dispatched", bad, calls.Load(), observed, err)
			}
		})
	}
}

func TestObserverRequestBoundaryRejectsWirePolicyDrift(t *testing.T) {
	for _, format := range []string{"openai", "responses"} {
		for _, bad := range []string{"stream", "wire-input", "model", "parameter", "store"} {
			t.Run(format+"/"+bad, func(t *testing.T) {
				if format != "responses" && bad == "store" {
					return
				}
				profile := Profile{BaseURL: "https://example.test/v1", Model: "model", APIFormat: format}
				input := `{"cues":[{"cue":0,"text":"original"}]}`
				metadata := contract.GenerationObservation{Purpose: "subtitle-summary", InputSHA256: recipeDigest(input), CueIDs: []int{0}, AttemptIndex: 0, FeedbackSHA256: recipeDigest("")}
				observed := 0
				ctx := contract.WithGenerationObservation(context.Background(), metadata, func(context.Context, contract.GenerationAttemptEvidence) error { observed++; return nil })
				messages := []Message{{Role: "system", Content: "prompt"}, {Role: "user", Content: input}}
				responses := format == "responses"
				wire := map[string]any{"messages": messages}
				if responses {
					wire = map[string]any{"input": responsesInput(messages), "store": false}
				}
				body := chatBody(profile, "model", wire, responses, false)
				switch bad {
				case "stream":
					body["stream"] = true
				case "wire-input":
					if responses {
						body["input"] = []map[string]any{{"role": "user", "content": "other"}}
					} else {
						body["messages"] = []Message{{Role: "user", Content: "other"}}
					}
				case "model":
					body["model"] = "other"
				case "parameter":
					body["temperature"] = 0.9
				case "store":
					body["store"] = true
				}
				encoded, _ := marshalNoEscape(body)
				_, observer, err := observationForRequest(ctx, profile, chatEndpoint(profile, responses), body, encoded, messages, nil)
				if err == nil || observer != nil || observed != 0 {
					t.Fatal("wire policy drift accepted", bad, err, observed)
				}
			})
		}
	}
}
