package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func TestRetainedSummaryPublicQueueHTTPAndPrivateDeclaration(t *testing.T) {
	var calls atomic.Int32
	var removeOnResponse atomic.Bool
	var managedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if removeOnResponse.Load() {
			if err := os.Remove(managedPath); err != nil {
				t.Error(err)
			}
		}
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, `[{"cue":0,"quote":"Saved words","point":"Key point","topic":"Topic"}]`)
	}))
	defer server.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	resources := t.TempDir()
	writeFixtureFile(t, data, "settings.json", `{"profiles":[],"activeProfile":"","theme":"light","prompts":{},"qwenProfile":{"id":"qwen-default","name":"test","provider":"qwen","baseUrl":"`+server.URL+`/v1","model":"test","apiFormat":"chat_completions","encryptedKey":"encrypted"}}`)
	video := videoppt.New(filepath.Join(root, "VideoTasks"))
	source := filepath.Join(root, "original.mp4")
	if err := os.WriteFile(source, []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := videoppt.VideoDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	task, err := video.CreateOperationTask("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "fixture", videoppt.VideoOperation{Version: 1, Stage: "subtitle", Path: source, SourceHash: hash, OutputName: "fixture.srt", Page: 1, RetainPlayback: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	managedPath = task.Path
	if _, err := video.ReservePlaybackRetention(ctx, task.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := videoppt.CopyVideoReady(ctx, source, task.Path); err != nil {
		t.Fatal(err)
	}
	const srt = "1\n00:00:00,000 --> 00:00:01,000\nSaved words\n"
	if err := videoppt.SavePreparedSubtitle(task, srt, 1); err != nil {
		t.Fatal(err)
	}
	if err := video.PreparePlaybackRetention(ctx, task.ID, srt); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "Transcripts")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	saved, err := videoppt.PublishSubtitle(ctx, task, directory, "fixture.srt", []byte(srt))
	if err != nil {
		t.Fatal(err)
	}
	if err := video.MarkSubtitleSaved(task.ID, saved); err != nil {
		t.Fatal(err)
	}
	binding, err := video.CompletePlaybackRetention(ctx, task.ID, saved)
	if err != nil {
		t.Fatal(err)
	}
	f := startCore(t, data, resources)
	if _, reply := f.call("core.hello", map[string]any{"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10", "platform": testPlatform(), "dataRoot": data, "libraryRoot": root, "resourcesRoot": resources, "promptsRoot": resources, "portable": false, "packaged": false}); reply != nil {
		t.Fatal(reply)
	}
	if _, reply := f.call("db:bootstrap", nil); reply != nil {
		t.Fatal(reply)
	}
	request := map[string]any{"operationId": "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "mode": "brief", "retention": map[string]any{"videoTaskID": task.ID, "expectedRetentionReceiptSHA256": binding.RetentionReceiptSHA256}}
	raw, reply := f.call("video-ppt:summary-from-retained", request)
	if reply != nil {
		t.Fatal(reply)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	ref, ok := result["sourceBinding"].(map[string]any)
	if !ok || ref["summaryTaskID"] != request["operationId"] || ref["sourceBindingSHA256"] == "" || result["count"] != float64(1) || calls.Load() != 1 {
		t.Fatal("new public pipeline failed", string(raw), calls.Load())
	}
	query := map[string]any{"summaryTaskID": ref["summaryTaskID"], "itemIndex": 0, "expectedSourceBindingSHA256": ref["sourceBindingSHA256"]}
	declaration, reply := f.call("video-ppt:bound-playback-source", query)
	if reply != nil {
		t.Fatal(reply)
	}
	var declared map[string]any
	_ = json.Unmarshal(declaration, &declared)
	if len(declared) != 10 || declared["taskID"] != task.ID || declared["sourceSHA256"] != hash {
		t.Fatal("declaration mismatch", string(declaration))
	}
	continuousQuery := map[string]any{"summaryTaskID": ref["summaryTaskID"], "itemIndex": 0, "expectedSourceBindingSHA256": ref["sourceBindingSHA256"], "expectedBindingAuthoritySHA256": declared["bindingAuthoritySHA256"]}
	continuous, reply := f.call("video-ppt:bound-playback-check", continuousQuery)
	if reply != nil || string(continuous) != string(declaration) {
		t.Fatal("continuous authority wire mismatch", reply, string(continuous))
	}
	if _, reply := f.call("video-ppt:summary-from-retained", request); reply != nil || calls.Load() != 1 {
		t.Fatal("new public retry replayed provider", reply, calls.Load())
	}
	// The second real HTTP response arrives after media loss. Its observed
	// response must still be persisted/published, never discarded and replayed.
	removeOnResponse.Store(true)
	request["operationId"] = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if _, reply := f.call("video-ppt:summary-from-retained", request); reply != nil || calls.Load() != 2 {
		t.Fatal("observed HTTP result lost after media removal", reply, calls.Load())
	}
	if _, reply := f.call("video-ppt:bound-playback-source", query); reply == nil {
		t.Fatal("missing video remained live")
	}
	if _, reply := f.call("video-ppt:bound-playback-check", continuousQuery); reply == nil {
		t.Fatal("continuous check accepted missing media")
	}
	if _, reply := f.call("video-ppt:summary-from-retained", request); reply != nil || calls.Load() != 2 {
		t.Fatal("same-ID text recovery required live media", reply, calls.Load())
	}
	request["mode"] = "detailed"
	if _, reply := f.call("video-ppt:summary-from-retained", request); reply == nil || calls.Load() != 2 {
		t.Fatal("same-ID recovery allowed mode drift", reply, calls.Load())
	}
	if _, reply := f.call("subtitle-derived:resume", map[string]any{"id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}); reply != nil || calls.Load() != 2 {
		t.Fatal("historical text resume replayed", reply, calls.Load())
	}
}
