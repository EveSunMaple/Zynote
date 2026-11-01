package api

import "testing"

func TestTextbookContentsFilteringKeepsBodyOnSamePage(t *testing.T) {
	toc := []textbookLine{
		{ID: 2, Text: "目 录"}, {ID: 4, Text: "2.1 动量……13"},
		{ID: 6, Text: "2.2 动量守恒 ...... 20"}, {ID: 8, Text: "— 2 —"},
	}
	if body, skipped := textbookBodyLines(toc); !skipped || len(body) != 0 {
		t.Fatalf("contents-only page: %+v %v", body, skipped)
	}
	definition := textbookLine{ID: 14, Text: "动量的定义：p = mv"}
	body, skipped := textbookBodyLines(append(toc, definition))
	if skipped || len(body) != 1 || body[0].ID != definition.ID {
		t.Fatalf("lost definition or its original line ID: %+v %v", body, skipped)
	}
	examples := []textbookLine{{Text: "使用目录定位教材文件。"}, {Text: "f(x) = ... 1"}, {Text: "F=ma"}}
	if body, skipped = textbookBodyLines(examples); skipped || len(body) != len(examples) {
		t.Fatalf("body examples filtered: %+v", body)
	}
}

func TestTextbookSparseImageAndFormulaRouting(t *testing.T) {
	mixed := textbookPageContent{
		ViewBox: [4]float64{-15, 50, 585, 850}, HasBodyImage: true,
		Items: []textbookLine{{Text: "教材页眉", Y: 816, Height: 12}, {Text: "第 3 页", Y: 60, Height: 12}},
	}
	if !textbookNeedsPageImage(mixed) {
		t.Fatal("selectable header and footer hid the scanned body")
	}
	mixed.Items = append(mixed.Items, textbookLine{Text: "Figure 3", Y: 220, Height: 12})
	if !textbookNeedsPageImage(mixed) {
		t.Fatal("isolated caption hid the scanned body")
	}
	mixed.Items = append(mixed.Items, textbookLine{Text: "Explanation of magnetic flux", Y: 620, Height: 12})
	if textbookNeedsPageImage(mixed) {
		t.Fatal("well separated body text should use its exact PDF coordinates")
	}
	formula := textbookPageContent{Items: []textbookLine{{Text: "E = mc²", Y: 600, Height: 12}}}
	if textbookNeedsPageImage(formula) {
		t.Fatal("short formula on a native text page routed to vision")
	}
}

func TestTextbookContentsModelReply(t *testing.T) {
	if !textbookContentsReply([]byte(`{"pageType":"contents"}`)) {
		t.Fatal("pure contents marker rejected")
	}
	for _, value := range []string{`[]`, `{"pageType":"body"}`, `{"pageType":"contents","rows":[1]}`} {
		if textbookContentsReply([]byte(value)) {
			t.Fatalf("accepted ambiguous page marker: %s", value)
		}
	}
}
