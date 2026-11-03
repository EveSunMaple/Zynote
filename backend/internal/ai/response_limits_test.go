package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingResponse struct{}

func (failingResponse) Read([]byte) (int, error) {
	return 0, errors.New("fixture-secret-must-not-escape")
}

func TestResponseReadErrorsArePublicSafe(t *testing.T) {
	response := &http.Response{Body: io.NopCloser(failingResponse{})}
	_, nonstreamErr := decodeChatResponse(response, false)
	_, streamErr := readSSE(failingResponse{}, func(string) {})
	for _, err := range []error{nonstreamErr, streamErr} {
		if err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	if !errors.Is(publicResponseReadError(errChatResponseLimit), errChatResponseLimit) {
		t.Fatal("lost limit classification")
	}
}

type repeatedResponse struct{ read int64 }

func (source *repeatedResponse) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	source.read += int64(len(buffer))
	return len(buffer), nil
}

func TestResponseWireLimitStopsUnendingBody(t *testing.T) {
	source := &repeatedResponse{}
	_, err := io.Copy(io.Discard, boundedChatResponse(source))
	if err == nil || source.read != maxChatResponseBytes+1 {
		t.Fatalf("err=%v read=%d", err, source.read)
	}
	if _, err := io.ReadAll(boundedChatResponse(strings.NewReader(strings.Repeat("x", int(maxChatResponseBytes))))); err != nil {
		t.Fatal(err)
	}
}

func TestResponseJSONBounds(t *testing.T) {
	if err := validateResponseJSONBounds([]byte(`"` + strings.Repeat("x", maxResponseJSONStringBytes) + `"`)); err != nil {
		t.Fatal("exact string limit rejected", err)
	}
	for _, input := range []string{
		strings.Repeat("[", maxResponseJSONDepth+1) + strings.Repeat("]", maxResponseJSONDepth+1),
		`"` + strings.Repeat("x", maxResponseJSONStringBytes+1) + `"`,
	} {
		if err := validateResponseJSONBounds([]byte(input)); err == nil {
			t.Fatal("excessive JSON accepted")
		}
	}
	if err := validateResponseJSONBounds([]byte(`{"escaped":"\\\"[[[","normal":"text"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestChatRejectsOversizedWireResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if stream {
					fmt.Fprint(writer, ":")
				} else {
					fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
				}
				// Even whitespace after valid JSON or ignored SSE comments counts.
				_, _ = io.CopyN(writer, &repeatedResponse{}, maxChatResponseBytes+1024)
			}))
			defer server.Close()
			client := New(Profile{BaseURL: server.URL, Model: "fixture", APIFormat: "chat_completions"})
			var onText func(string)
			if stream {
				onText = func(string) {}
			}
			if result, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "fixture"}}, onText); err == nil || result != "" {
				t.Fatalf("result=%q err=%v", result, err)
			}
		})
	}
}

func TestSSERejectsDeepJSONBeforeDecode(t *testing.T) {
	input := "data: " + strings.Repeat("[", maxResponseJSONDepth+1) + "0" + strings.Repeat("]", maxResponseJSONDepth+1) + "\n\ndata: [DONE]\n\n"
	if _, err := readSSE(strings.NewReader(input), func(string) {}); err == nil {
		t.Fatal("deep SSE accepted")
	}
}
