package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	client := New(Profile{BaseURL: server.URL + "/v1", Model: "test", APIFormat: "chat_completions"})
	chunks := []string{}
	reply, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(text string) {
		chunks = append(chunks, text)
	})
	if err != nil || reply != "你好" || strings.Join(chunks, "") != "你好" {
		t.Fatalf("stream mismatch: %q %q %v", reply, chunks, err)
	}
}

func TestVisionJSONOptionsAreRequestScoped(t *testing.T) {
	bodies := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies <- body
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"[]"}}]}`)
	}))
	defer server.Close()
	client := New(Profile{BaseURL: server.URL, Model: "text", VisionModel: "qwen3.8-flash"})
	options := JSONOptions{
		Images: []Image{{URL: "data:image/png;base64,AA=="}}, DisableThinking: true, HighResolutionImages: true,
	}
	if _, err := client.JSONWithOptions(context.Background(), "schedule", "", func([]byte) error { return nil }, options); err != nil {
		t.Fatal(err)
	}
	if body := <-bodies; body["enable_thinking"] != false || body["vl_high_resolution_images"] != true {
		t.Fatal("vision request did not carry selected image options")
	}
	if _, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatal(err)
	}
	if body := <-bodies; body["enable_thinking"] != nil || body["vl_high_resolution_images"] != nil {
		t.Fatal("vision options leaked into a subsequent ordinary request")
	}
}

func TestChatIncompleteStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"半\"}}]}\n\n")
	}))
	defer server.Close()
	client := New(Profile{BaseURL: server.URL, Model: "test"})
	if _, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string) {}); err == nil ||
		!strings.Contains(err.Error(), "缺少有效完成标记") {
		t.Fatalf("incomplete stream mismatch: %v", err)
	}
}

func TestChatJSONAndModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/chat/completions":
			fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"回复"}}]}`)
		case "/v1/responses":
			fmt.Fprint(writer, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"响应"}]}]}`)
		case "/v1/models":
			fmt.Fprint(writer, `{"data":[{"id":"a"},{"id":"b"},{"id":"a"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New(Profile{BaseURL: server.URL + "/v1", Model: "test", APIFormat: "chat_completions"})
	reply, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || reply != "回复" {
		t.Fatalf("json chat mismatch: %q %v", reply, err)
	}
	responses := New(Profile{BaseURL: server.URL + "/v1", Model: "test", APIFormat: "responses"})
	value, err := responses.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || value != "响应" {
		t.Fatalf("responses mismatch: %q %v", value, err)
	}
	models, err := client.Models(context.Background())
	if err != nil || len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("models mismatch: %v %v", models, err)
	}
	missing := New(Profile{BaseURL: server.URL + "/v1", Model: ""})
	if _, err := missing.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil ||
		!strings.Contains(err.Error(), "文本模型") {
		t.Fatalf("model selection mismatch: %v", err)
	}
}

func TestRepairJSONEscapes(t *testing.T) {
	raw := `[{"stem":"$\frac{dy}{dx}$\nA. $\frac{du}{dx}$","note":"C:\Users\eve","theta":"$\theta$","unicode":"\u4e2d"}]`
	var parsed []struct {
		Stem    string `json:"stem"`
		Note    string `json:"note"`
		Theta   string `json:"theta"`
		Unicode string `json:"unicode"`
	}
	if err := json.Unmarshal(repairJSONEscapes([]byte(raw)), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 ||
		parsed[0].Stem != "$\\frac{dy}{dx}$\nA. $\\frac{du}{dx}$" ||
		parsed[0].Note != `C:\Users\eve` ||
		parsed[0].Theta != `$\theta$` ||
		parsed[0].Unicode != "中" {
		t.Fatalf("unexpected repair: %+v", parsed)
	}
}

func TestChatAndModelListErrors(t *testing.T) {
	const privateDetail = "private-test-value"
	for _, test := range []struct {
		name       string
		status     int
		body       string
		hint       string
		retryAfter time.Duration
	}{
		{"activation", 403, `{"error":{"code":"AccessDenied.Unpurchased","message":"private-test-value"}}`, "业务空间专属地址", 0},
		{"activation top level", 403, `{"code":"AccessDenied.Unpurchased","message":"private-test-value"}`, "业务空间专属地址", 0},
		{"key", 401, `{"error":{"message":"private-test-value"}}`, "密钥验证失败", 0},
		{"permissions", 403, `{"error":{"code":"private-test-value","message":"AccessDenied.Unpurchased"}}`, "模型调用权限", 0},
		{"rate limit", 429, privateDetail, "请求受限", 2 * time.Second},
		{"temporary failure", 503, privateDetail, "暂时不可用", 2 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Retry-After", "2")
				writer.WriteHeader(test.status)
				fmt.Fprint(writer, test.body)
			}))
			defer server.Close()
			client := New(Profile{BaseURL: server.URL, Model: "test", Key: privateDetail})
			_, chatErr := client.Chat(context.Background(), []Message{{Role: "user", Content: privateDetail}}, nil)
			_, modelsErr := client.Models(context.Background())
			for _, err := range []error{chatErr, modelsErr} {
				if err == nil || !strings.Contains(err.Error(), test.hint) || strings.Contains(err.Error(), privateDetail) {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if IsRetryable(chatErr) != (test.retryAfter > 0) || RetryAfter(chatErr) != test.retryAfter {
				t.Fatalf("unexpected retry policy: %v", chatErr)
			}
		})
	}
}
