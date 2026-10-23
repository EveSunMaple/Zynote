package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
