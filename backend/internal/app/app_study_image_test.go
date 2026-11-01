package app_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func checkImportPhases(t *testing.T, events []wireEnvelope, requestID, filename string, expected ...string) {
	t.Helper()
	var phases []string
	for _, env := range events {
		var event struct {
			Kind string            `json:"kind"`
			Data map[string]string `json:"data"`
		}
		if json.Unmarshal(env.Params, &event) != nil || event.Kind != "study-import" {
			continue
		}
		if len(event.Data) != 3 || event.Data["requestId"] != requestID || event.Data["fileName"] != filename {
			t.Fatalf("unexpected import metadata: %s", env.Params)
		}
		phases = append(phases, event.Data["phase"])
	}
	if !slices.Equal(phases, expected) {
		t.Fatalf("import phases: got %v, want %v", phases, expected)
	}
}

func TestStudyImageDraftAndSave(t *testing.T) { // NOSONAR: end-to-end import test; extracting steps would obscure the scenario.
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "课表.png")
	if err := os.WriteFile(path, picture.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(picture.Bytes())
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil || r.URL.Path != "/v1/chat/completions" || !containsAll(string(payload), `"model":"vision-test"`, `"type":"image_url"`, url) {
			t.Errorf("expected a vision chat with the selected PNG")
		}
		blocks := `[{"title":" 数据思维（甲）I ","weekday":"3","startPeriod":5,"endPeriod":"7","location":"A106","lectureId":"invented"},null,{"title":"","weekday":3,"startPeriod":9,"endPeriod":"看不清"},{"title":"晚课","weekday":8,"startPeriod":9,"endPeriod":7}]`
		if attempts.Add(1) == 1 {
			blocks = "not JSON"
		}
		content, _ := json.Marshal(blocks)
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%s}}]}`, content)
	}))
	defer server.Close()
	f := startStudyParseFixture(t, server.URL)
	// The renderer cannot substitute its own file path for the native picker.
	requestID := "33333333-3333-4333-8333-333333333333"
	args := map[string]any{"source": "image", "requestId": requestID, "timezone": "Asia/Shanghai", "path": path}
	testPickPath = ""
	defer func() { testPickPath = "" }()
	if raw, failure := f.call("study:parse", args); failure != nil || string(raw) != "null" {
		t.Fatalf("picker cancellation: %s %+v", raw, failure)
	}
	checkImportPhases(t, f.events, requestID, filepath.Base(path))
	testPickPath = path
	raw, failure := f.call("study:parse", args)
	var rows []map[string]any
	if failure != nil || json.Unmarshal(raw, &rows) != nil || len(rows) != 3 {
		t.Fatalf("image parse: %s %+v", raw, failure)
	}
	checkImportPhases(t, f.events, requestID, filepath.Base(path), "reading", "ready", "uploading", "sent", "received", "retrying", "sent", "received")
	first, covered := rows[0], rows[1]
	if first["title"] != "数据思维（甲）I" || first["weekly"] != true || first["courseId"] != "" ||
		first["chapter"] != "" || first["endsAt"] != "" || first["lectureId"] != nil || first["weekday"] != 3.0 ||
		first["startPeriod"] != 5.0 || first["endPeriod"] != 7.0 || first["location"] != "A106" {
		t.Fatalf("weekly fields: %+v", first)
	}
	if covered["title"] != "" || covered["endPeriod"] != nil || covered["id"] == first["id"] {
		t.Fatalf("partially covered block: %+v", covered)
	}
	if rows[2]["title"] != "晚课" || rows[2]["weekday"] != nil || rows[2]["startPeriod"] != nil || rows[2]["endPeriod"] != nil {
		t.Fatalf("uncertain slot should be editable and unplaced: %+v", rows[2])
	}
	if raw, failure := f.call("study:get", nil); failure != nil || string(raw) != "[]" {
		t.Fatalf("recognition wrote unconfirmed entries: %s %+v", raw, failure)
	}
	covered["title"] = "补录课程"
	covered["endPeriod"] = 9
	covered["skipZhiyunMatch"] = true
	if _, failure = f.call("study:save", map[string]any{"entries": rows}); failure != nil {
		t.Fatalf("save without a linked course or chapter: %+v", failure)
	}
	first["endPeriod"] = 2
	if _, failure = f.call("study:save", map[string]any{"entries": rows}); failure == nil {
		t.Fatal("invalid period range was accepted")
	}
	raw, failure = f.call("study:get", nil)
	if failure != nil || !containsAll(string(raw), "补录课程", "A106", `"endPeriod":7`, `"skipZhiyunMatch":true`) {
		t.Fatalf("saved weekly entries were lost: %s %+v", raw, failure)
	}
	if err := os.WriteFile(path, []byte("not a screenshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInvalid := len(f.events)
	if _, failure = f.call("study:parse", args); failure == nil || !strings.Contains(failure.Message, "PNG") {
		t.Fatalf("invalid image accepted: %+v", failure)
	}
	checkImportPhases(t, f.events[beforeInvalid:], requestID, filepath.Base(path), "reading")
	beforeLegacy := len(f.events)
	delete(args, "requestId")
	if _, failure = f.call("study:parse", args); failure == nil || !strings.Contains(failure.Message, "PNG") {
		t.Fatalf("legacy image request did not reach the reader: %+v", failure)
	}
	checkImportPhases(t, f.events[beforeLegacy:], "", "")
}
