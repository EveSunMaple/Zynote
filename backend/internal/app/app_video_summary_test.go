package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVideoSummary(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		fmt.Fprintf(writer, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`,
			`{"processing":"complete","cards":[{"cue":0,"quote":"第一句","point":"核心结论","topic":"主题"}]}`)
	}))
	defer server.Close()
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	writeFixtureFile(t, dataRoot, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"通义千问","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","visionModel":"","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "摘要课"})
	if errReply != nil {
		t.Fatal(errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	raw, errReply := f.call("video-ppt:summary", map[string]any{
		"operationId": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "mode": "brief", "title": "第一讲", "courseId": course["id"],
		"srt": "1\n00:00:01,000 --> 00:00:03,000\n第一句 机器学习\n",
	})
	if errReply != nil {
		t.Fatalf("summary failed: %+v", errReply)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["sourceBinding"] != nil {
		t.Fatal("old unbound summary gained playback authority")
	}
	if result["count"] != float64(1) || !strings.Contains(result["markdown"].(string), "# 简要摘要") ||
		!strings.Contains(result["markdown"].(string), "核心结论") ||
		!strings.Contains(result["markdown"].(string), "00:00:01") {
		t.Fatalf("summary mismatch: %s", raw)
	}
	anchor := result["points"].([]any)[0].(map[string]any)["anchor"].(map[string]any)
	if anchor["sourceHash"] != result["sourceHash"] || anchor["cueIndex"] != float64(0) || anchor["startMs"] != float64(1000) || anchor["endMs"] != float64(3000) {
		t.Fatalf("RPC anchor mismatch: %+v", anchor)
	}
	path := result["path"].(string)
	if filepath.Dir(path) != filepath.Join(libraryRoot, "Notes", "摘要课") || !strings.Contains(result["markdown"].(string), "传入 SRT 快照") {
		t.Fatal("source provenance or course directory missing")
	}
	if _, errReply := f.call("video-ppt:summary", map[string]any{"operationId": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "mode": "brief", "title": "第一讲", "courseId": course["id"], "srt": "1\n00:00:01,000 --> 00:00:03,000\n第一句 机器学习\n"}); errReply != nil {
		t.Fatal(errReply)
	}
	if calls.Load() != 1 {
		t.Fatal("completed summary called provider twice")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != result["markdown"] {
		t.Fatalf("summary file mismatch: %v %q", err, body)
	}
	if _, err := os.Stat(strings.TrimSuffix(path, ".md") + ".json"); err != nil {
		t.Fatalf("evidence missing: %v", err)
	}
	evidence, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".json")
	if err != nil || !strings.Contains(string(evidence), result["sourceHash"].(string)) || !strings.Contains(string(evidence), `"startMs": 1000`) {
		t.Fatalf("persistent anchor missing: %s %v", evidence, err)
	}
	// An orphan evidence file must never be overwritten by a new summary.
	orphan := filepath.Join(filepath.Dir(path), "第二讲-简要摘要.json")
	if err := os.WriteFile(orphan, []byte("existing evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, errReply = f.call("video-ppt:summary", map[string]any{
		"mode": "brief", "title": "第二讲", "courseId": course["id"],
		"srt": "1\n00:00:01,000 --> 00:00:03,000\n第一句 机器学习\n",
	})
	if errReply != nil {
		t.Fatal(errReply)
	}
	if body, err := os.ReadFile(orphan); err != nil || string(body) != "existing evidence" {
		t.Fatalf("existing evidence overwritten: %q %v", body, err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(result["path"].(string)) != filepath.Dir(path) || !strings.HasPrefix(filepath.Base(result["path"].(string)), "第二讲-简要摘要-") {
		t.Fatalf("task-scoped output missing: %v", result["path"])
	}
}
