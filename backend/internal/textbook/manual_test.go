package textbook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManualSelectionWritesOnceAndKeepsInkGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, []byte("test-pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	service, caller := New(), &fakeCaller{}
	id := "11111111-1111-4111-8111-111111111111"
	if _, err := service.Select(caller, path, func(string) (string, error) { return id, nil }); err != nil {
		t.Fatal(err)
	}
	stroke := Highlight{ID: "22222222-2222-4222-8222-222222222222", Page: 1, Rect: [4]float64{10, 10, 60, 40}, Ink: [][2]float64{{10, 10}, {45, 25}, {15, 50}}, Source: "teacher", Quote: "笔迹", Evidence: "手动标注"}
	text := Highlight{ID: "33333333-3333-4333-8333-333333333333", Page: 1, Rect: [4]float64{15, 20, 25, 10}, Source: "focus", Quote: "教材原文", Evidence: "手动标注"}
	raw, _ := json.Marshal([]Highlight{stroke, text})
	if _, err := service.Manual(caller, id, raw); err != nil {
		t.Fatal(err)
	}
	if len(caller.writes) != 1 || len(caller.writes[0]) != 2 || caller.writes[0][1]["ink"] == nil {
		t.Fatal("expected one write containing the full stroke and selected text")
	}
	stroke.Ink[0][0] = 1000
	raw, _ = json.Marshal([]Highlight{text, stroke})
	if _, err := service.Manual(caller, id, raw); err == nil || len(caller.writes) != 1 {
		t.Fatal("invalid stroke in group must leave the original intact")
	}
}
