package textbook

import (
	"fmt"
	"strings"
	"testing"
)

func TestTeacherAnnotationsAcrossBook(t *testing.T) {
	teacher := Comment{Kind: "teacher", Text: "老师提到定义", Quote: "先明确数列的极限", Timestamp: "00:01:23"}
	makeMark := func(n, page int, y float64, comments ...Comment) Highlight {
		return Highlight{ID: fmt.Sprintf("%08d-aaaa-4aaa-8aaa-aaaaaaaaaaaa", n), Page: page, Rect: [4]float64{5, y, 100, 10}, Quote: "极限的定义", Source: "teacher", Evidence: "原来记录的依据", Comments: comments}
	}
	a := makeMark(1, 2, 620, Comment{Kind: "personal", Text: "我的笔记"}, teacher)
	b := makeMark(2, 2, 608, teacher)
	c := makeMark(3, 7, 605, teacher)
	d := makeMark(4, 2, 630, Comment{Kind: "teacher", Text: "先看例题", Quote: "先看例题"})
	d.Source = "ai" // A mixed highlight may have originally been created by another source.
	service := New()
	service.books["b"] = book{pages: 7, annotations: []Highlight{c, b, makeMark(5, 1, 600), d, a, makeMark(6, 7, 510, Comment{Kind: "ai", Text: "这篇很重要"})}}
	result, err := service.TeacherAnnotations("b", 0)
	if err != nil || result.Total != 3 || len(result.Items) != 3 || result.Items[0].ID != d.ID || result.Items[1].ID != a.ID || result.Items[2].ID != c.ID {
		t.Fatalf("bad navigation order/dedup: %+v %v", result, err)
	}
	if len(result.Items[1].Comments) != 1 || result.Items[1].Comments[0] != teacher {
		t.Fatalf("wrong transcript evidence: %+v", result.Items[1])
	}
	end, err := service.TeacherAnnotations("b", 3)
	if err != nil || end.Items == nil || len(end.Items) != 0 || end.Total != 3 {
		t.Fatalf("bad empty page: %+v %v", end, err)
	}
	page, err := service.Annotations("b", 2)
	if err != nil || len(page) != 3 || len(a.Comments) != 2 || len(page[2].Comments) != 2 {
		t.Fatal("page view was modified")
	}
}

func TestTeacherAnnotationsPaging(t *testing.T) {
	// A long lecture must paginate by payload as well as by row count.
	service := New()
	var marks []Highlight
	for i := 0; i < 120; i++ {
		c := Comment{Kind: "teacher", Text: strings.Repeat("文", 2500), Quote: strings.Repeat("课", 2500)}
		marks = append(marks, Highlight{ID: fmt.Sprintf("%08d-aaaa-4aaa-8aaa-aaaaaaaaaaaa", i), Page: i + 1, Rect: [4]float64{1, 2, 3, 4}, Quote: "定义", Source: "teacher", Evidence: "据", Comments: []Comment{c}})
	}
	service.books["b"] = book{pages: 120, annotations: marks}
	for n := 0; n < len(marks); {
		page, err := service.TeacherAnnotations("b", n)
		if err != nil || len(page.Items) == 0 || len(page.Items) >= 50 || page.Total != len(marks) || page.Items[0].ID != marks[n].ID {
			t.Fatalf("bad page at %d: size=%d total=%d err=%v", n, len(page.Items), page.Total, err)
		}
		n += len(page.Items)
	}
	if _, err := service.TeacherAnnotations("b", -1); err == nil {
		t.Fatal("negative offset accepted")
	}
}
