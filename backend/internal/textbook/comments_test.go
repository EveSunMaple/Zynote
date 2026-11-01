package textbook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOverlappingCommentsSurviveEditsAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, []byte("body"), 0600); err != nil {
		t.Fatal(err)
	}
	a := Highlight{ID: "22222222-2222-4222-8222-222222222222", Page: 1, Rect: [4]float64{20, 10, 100, 10}, Source: "teacher", Quote: "定义", Evidence: "本节课原话", Comments: []Comment{{Kind: "teacher", Text: "老师提到定义", Quote: "本节课原话", Timestamp: "00:02:13"}}}
	b := a
	b.ID = "33333333-3333-4333-8333-333333333333"
	b.Source = "ai"
	b.Comments = []Comment{{Kind: "ai", Text: "有三个条件"}}
	partial := b
	partial.ID = "44444444-4444-4444-8444-444444444444"
	partial.Rect[0] += 10
	partial.Quote = "定义的一部分"
	rows := prioritizedHighlights([]Highlight{a, b, b, partial})
	if len(rows) != 2 || rows[0].ID != a.ID || len(rows[0].Comments) != 2 || rows[1].Rect != partial.Rect {
		t.Fatalf("lost overlapping sources: %#v", rows)
	}
	caller := &fakeCaller{previous: rows}
	service := New()
	id := "11111111-1111-4111-8111-111111111111"
	if _, err := service.Resolve(caller, id, path); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Comment(caller, id, a.ID, "复习条件", nil); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Annotations(id, 1)
	if err != nil || len(saved) != 2 || len(saved[0].Comments) != 3 {
		t.Fatalf("saved marks: %#v %v", saved, err)
	}
	syncWrites := func() {
		raw, err := json.Marshal(caller.writes[len(caller.writes)-1])
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &caller.previous); err != nil {
			t.Fatal(err)
		}
	}
	syncWrites()
	if _, err = service.Comment(caller, id, a.ID, "复习条件", nil); err != nil {
		t.Fatal(err)
	}
	syncWrites()
	prev := "复习条件"
	if _, err = service.Comment(caller, id, a.ID, "先看推导", &prev); err != nil {
		t.Fatal(err)
	}
	syncWrites()
	if _, err = service.Comment(caller, id, a.ID, "不能覆盖并发更新", &prev); err == nil {
		t.Fatal("a stale edit replaced newer content")
	}
	restored := New()
	if _, err = restored.Resolve(caller, id, path); err != nil {
		t.Fatal(err)
	}
	saved, err = restored.Annotations(id, 1)
	if err != nil || len(saved[0].Comments) != 3 || saved[0].Comments[2].Text != "先看推导" || saved[0].Comments[0].Quote != a.Evidence {
		t.Fatalf("reopen: %#v %v", saved, err)
	}
}

func TestChapterSummaryReplacesOnlySameChapter(t *testing.T) {
	summary := Highlight{ID: "22222222-2222-4222-8222-222222222222", Page: 2, Source: "ai", Rect: [4]float64{10, 10, 150, 40}, Quote: "第一章", Evidence: "小结",
		Chapter: &ChapterSummary{Title: "第一章", Text: "本章建立极限的定义。", LastPage: 7, Refs: []ChapterRef{{Page: 3, Line: 7, Quote: "定义", Rect: [4]float64{20, 200, 290, 14}}}}}
	if !validHighlight(summary) {
		t.Fatal("rejected chapter")
	}
	changed := summary
	changed.ID = "33333333-3333-4333-8333-333333333333"
	c := *summary.Chapter
	c.Text = "先理解定义。"
	changed.Chapter = &c
	rows := mergeHighlights([]Highlight{summary}, []Highlight{changed})
	if len(rows) != 1 || rows[0].ID != summary.ID || rows[0].Chapter.Text != c.Text || rows[0].Chapter.Refs[0].Line != 7 {
		t.Fatalf("chapter replacement: %#v", rows)
	}
	invalid := summary
	bad := *summary.Chapter
	bad.LastPage = 2
	invalid.Chapter = &bad
	if validHighlight(invalid) {
		t.Fatal("accepted reference outside this chapter")
	}
}
