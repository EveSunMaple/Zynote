package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeAskAndClassificationSuggest(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		system := ""
		if len(body.Messages) > 0 {
			system = body.Messages[0].Content
		}
		if strings.Contains(system, "根据实际来源文字分类") {
			fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"type\":\"教育\",\"topic\":\"机器学习\",\"subtopics\":[\"特征\"],\"difficulty\":\"入门\",\"duration\":\"短视频\",\"language\":\"中文\",\"keywords\":[\"课堂\"]}"}}]}`)
			return
		}
		fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"paragraphs\":[{\"text\":\"资料说明了特征工程的重要性。\",\"citations\":[{\"id\":\"EVIDENCE\",\"quote\":\"机器学习的第一课\"}]}],\"limitation\":\"\"}"}}]}`)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	text := "机器学习的第一课，重点是特征工程与数据清洗。"
	if _, errReply := f.call("knowledge:import", map[string]any{
		"title": "机器学习", "text": text, "locator": "https://example.com/ai",
		"sourceHash": strings.Repeat("b", 64), "category": "课堂", "origin": "local",
	}); errReply != nil {
		t.Fatalf("import failed: %+v", errReply)
	}
	raw, errReply := f.call("knowledge:search", map[string]any{"query": "机器学习"})
	if errReply != nil {
		t.Fatalf("search failed: %+v", errReply)
	}
	var search map[string]any
	_ = json.Unmarshal(raw, &search)
	hit := search["hits"].([]any)[0].(map[string]any)
	evidence := hit["evidence_id"].(string)
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		system := ""
		if len(body.Messages) > 0 {
			system = body.Messages[0].Content
		}
		if strings.Contains(system, "根据实际来源文字分类") {
			fmt.Fprint(writer, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"type\":\"教育\",\"topic\":\"机器学习\",\"subtopics\":[\"特征\"],\"difficulty\":\"入门\",\"duration\":\"短视频\",\"language\":\"中文\",\"keywords\":[\"课堂\"]}"}}]}`)
			return
		}
		payload := fmt.Sprintf(`{"paragraphs":[{"text":"资料说明了特征工程的重要性。","citations":[{"id":"%s","quote":"机器学习的第一课"}]}],"limitation":""}`, evidence)
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, payload)
	})
	raw, errReply = f.call("knowledge:ask", map[string]any{"query": "机器学习"})
	if errReply != nil {
		t.Fatalf("ask failed: %+v", errReply)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	sources := result["sources"].([]any)
	if len(sources) == 0 || sources[0].(map[string]any)["evidence_id"] != evidence {
		t.Fatalf("ask sources mismatch: %s", raw)
	}
	answer := result["answer"].(map[string]any)
	if len(answer["paragraphs"].([]any)) != 1 {
		t.Fatalf("ask answer mismatch: %s", raw)
	}
	raw, errReply = f.call("knowledge:classification-suggest", map[string]any{
		"id": hit["bvid"],
	})
	if errReply != nil {
		t.Fatalf("suggest failed: %+v", errReply)
	}
	var suggestion map[string]any
	_ = json.Unmarshal(raw, &suggestion)
	if suggestion["selection"].(map[string]any)["type"] != "教育" || len(suggestion["token"].(string)) != 32 {
		t.Fatalf("suggest mismatch: %s", raw)
	}
	raw, errReply = f.call("knowledge:ask", map[string]any{"query": "不存在的内容"})
	if errReply != nil || !strings.Contains(string(raw), "未找到可引用") {
		t.Fatalf("empty ask mismatch: %s %+v", raw, errReply)
	}
}
