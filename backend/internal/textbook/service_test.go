package textbook

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCaller struct {
	previous []Highlight
	writes   [][]map[string]any
}

func (caller *fakeCaller) Call(method string, params any, timeoutMS int) (json.RawMessage, error) {
	switch method {
	case "pdf.inspect":
		return json.Marshal(inspectResult{Pages: 1, Sizes: [][]float64{{600, 800, 0}}, Previous: caller.previous})
	case "pdf.write":
		values := params.(map[string]any)
		caller.writes = append(caller.writes, values["rows"].([]map[string]any))
		if err := os.WriteFile(values["output"].(string), []byte("updated-pdf"), 0o644); err != nil {
			return nil, err
		}
		return json.Marshal(writeResult{Pages: 1, Count: len(caller.previous)})
	}
	return json.RawMessage(`{}`), nil
}

func TestPrioritizedHighlights(t *testing.T) {
	rows := prioritizedHighlights([]Highlight{
		{ID: "11111111-1111-4111-8111-111111111111", Page: 1, Rect: [4]float64{0, 0, 100, 10}, Source: "teacher", Quote: "q", Evidence: "e"},
		{ID: "22222222-2222-4222-8222-222222222222", Page: 1, Rect: [4]float64{0, 0, 50, 10}, Source: "focus", Quote: "q", Evidence: "e"},
	})
	if len(rows) != 2 || rows[0].Source != "focus" || rows[1].Rect != [4]float64{50, 0, 50, 10} {
		t.Fatalf("priority mismatch: %+v", rows)
	}
	covered := prioritizedHighlights([]Highlight{
		{ID: "33333333-3333-4333-8333-333333333333", Page: 1, Rect: [4]float64{0, 0, 100, 10}, Source: "focus", Quote: "q", Evidence: "e"},
		{ID: "44444444-4444-4444-8444-444444444444", Page: 1, Rect: [4]float64{0, 0, 100, 10}, Source: "ppt", Quote: "q", Evidence: "e"},
	})
	if len(covered) != 1 || covered[0].Source != "focus" {
		t.Fatalf("covered mismatch: %+v", covered)
	}
}

func TestServiceFlow(t *testing.T) { // NOSONAR: end-to-end test intentionally exercises one workflow; extracting helpers would obscure the scenario.
	root := t.TempDir()
	path := filepath.Join(root, "book.pdf")
	if err := os.WriteFile(path, []byte("original-pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	caller := &fakeCaller{}
	service := New()
	selected, err := service.Select(caller, path)
	if err != nil {
		t.Fatal(err)
	}
	selection := selected.(map[string]any)
	id := selection["id"].(string)
	if selection["pages"] != 1 {
		t.Fatalf("selection mismatch: %+v", selection)
	}
	bytes, err := service.Bytes(id)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(bytes.(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "original-pdf" {
		t.Fatalf("file server mismatch: %q", body)
	}
	request, _ := http.NewRequest(http.MethodGet, bytes.(string), nil)
	request.Header.Set("Range", "bytes=0-3")
	partial, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	partialBody, _ := io.ReadAll(partial.Body)
	partial.Body.Close()
	if partial.StatusCode != http.StatusPartialContent || string(partialBody) != "orig" {
		t.Fatalf("range mismatch: %d %q", partial.StatusCode, partialBody)
	}
	if openPath, err := service.OpenPath(id); err != nil || openPath != path {
		t.Fatalf("open path mismatch: %q %v", openPath, err)
	}
	highlight := map[string]any{
		"id": "55555555-5555-4555-8555-555555555555", "page": 1,
		"rect": []float64{10, 10, 100, 10}, "source": "focus",
		"quote": "重点", "evidence": "依据", "extension": false,
	}
	raw, _ := json.Marshal(highlight)
	result, err := service.Manual(caller, id, raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["count"] != 1 || len(caller.writes) != 1 || len(caller.writes[0]) != 1 {
		t.Fatalf("manual mismatch: %+v %+v", result, caller.writes)
	}
	if caller.writes[0][0]["description"] == "" || caller.writes[0][0]["color"] == nil {
		t.Fatalf("annotation payload mismatch: %+v", caller.writes[0][0])
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "updated-pdf" {
		t.Fatalf("write mismatch: %q %v", body, err)
	}
	if _, err := os.Stat(path + ".划线前备份.pdf"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	caller.previous = []Highlight{{ID: "66666666-6666-4666-8666-666666666666", Page: 1, Rect: [4]float64{1, 1, 10, 10}, Source: "focus", Quote: "q", Evidence: "e"}}
	cleared, err := service.Clear(caller, id)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.(map[string]any)["count"] != 1 || len(caller.writes[1]) != 0 {
		t.Fatalf("clear mismatch: %+v %+v", cleared, caller.writes[1])
	}
	service2 := New()
	selected2, _ := service2.Select(caller, path)
	bookID := selected2.(map[string]any)["id"].(string)
	draftID := service2.AddDraft(bookID, mustHash(t, path), []Highlight{{
		ID: "77777777-7777-4777-8777-777777777777", Page: 1, Rect: [4]float64{2, 2, 20, 10},
		Source: "teacher", Quote: "q", Evidence: "e",
	}})
	applied, err := service2.Apply(caller, draftID, []string{"77777777-7777-4777-8777-777777777777"})
	if err != nil {
		t.Fatal(err)
	}
	if applied.(map[string]any)["count"] != 1 {
		t.Fatalf("apply mismatch: %+v", applied)
	}
	if _, err := service2.Apply(caller, draftID, []string{"77777777-7777-4777-8777-777777777777"}); err == nil {
		t.Fatal("draft must be consumed")
	}
	if !strings.Contains(path, "book.pdf") {
		t.Fatal("unexpected path")
	}
}

func mustHash(t *testing.T, path string) string {
	t.Helper()
	value, err := fileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
