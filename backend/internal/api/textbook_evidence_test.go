package api

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextbookEvidenceCatalog(t *testing.T) {
	sources := textbookSources{
		Teacher: "磁\r\n场的方向\n\n   \n ——\n\n能量守恒。",
		Focus:   "注意定义所适用的边界条件！",
		PPT:     "[第 12 页]\n  Power and energy.  \n\n[课件来源限制]\n- OCR 未经检查",
		Senior:  strings.Repeat("有", 800),
	}
	catalog := makeTextbookEvidence(sources)
	if !reflect.DeepEqual(catalog, makeTextbookEvidence(sources)) {
		t.Fatal("citations should be stable across identical inputs")
	}
	var seen []string
	for i, entry := range catalog {
		if entry.ID != i+1 || !strings.Contains(textbookSourceValue(sources, entry.Source), entry.Text) ||
			!meaningfulEvidence(entry.Text) || utf8.RuneCountInString(entry.Text) > 360 {
			t.Fatalf("invalid entry: %+v", entry)
		}
		seen = append(seen, entry.Text)
	}
	want := []string{"磁\r\n场的方向", "能量守恒。", sources.Focus, "Power and energy.", strings.Repeat("有", 360), strings.Repeat("有", 360), strings.Repeat("有", 80)}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("catalog = %q; want %q", seen, want)
	}
}

func TestResolveTextbookEvidence(t *testing.T) {
	sources := textbookSources{
		Teacher: "电磁场的\r\n方向。\n\nThe magnetic\n  field is 1 0 units. Use 3 − 2.",
		PPT:     "课件中的另一条定义。\n\n[课件来源限制]\n- one diagnostic",
	}
	catalog := makeTextbookEvidence(sources)
	first, missing := 1, 77
	for _, tc := range []struct {
		name, source string
		id           *int
		claim, want  string
	}{
		{"id without retyping the quote", "", &first, "", "电磁场的\r\n方向。"},
		{"correct optional source", "teacher", &first, "场的方向", "场的\r\n方向"},
		{"fabricated id", "", &missing, "", ""},
		{"wrong source for id", "ppt", &first, "", ""},
		{"different segment of same source", "teacher", &first, "The magnetic", ""},
		{"old response with layout differences", "teacher", nil, "magnetic field", "magnetic\n  field"},
		{"no cross-category fallback", "ppt", nil, "电磁场的方向", ""},
		{"never glue together English words", "teacher", nil, "magneticfield", ""},
		{"unchanged digit separation", "teacher", nil, "10 units", ""},
		{"unchanged math operators", "teacher", nil, "3 + 2", ""},
		{"no invented claim", "teacher", nil, "方向与速度相同", ""},
		{"skip courseware diagnostics", "ppt", nil, "one diagnostic", ""},
		{"ignore punctuation-only quote", "teacher", nil, "。", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveTextbookEvidence(sources, catalog, tc.source, tc.id, tc.claim)
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("got %q, %t; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestLegacyEvidenceLengthBound(t *testing.T) {
	sources := textbookSources{Teacher: "开" + strings.Repeat("\n", 5000) + "始"}
	if _, ok := resolveTextbookEvidence(sources, nil, "teacher", nil, "开始"); ok {
		t.Fatal("restored quote would exceed the highlight metadata limit")
	}
}
