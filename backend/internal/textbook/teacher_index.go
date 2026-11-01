package textbook

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type TeacherMark struct {
	ID       string     `json:"id"`
	Page     int        `json:"page"`
	Rect     [4]float64 `json:"rect"`
	Quote    string     `json:"quote"`
	Comments []Comment  `json:"comments"`
}

type TeacherIndex struct {
	Items []TeacherMark `json:"items"`
	Total int           `json:"total"`
}

func teacherMarks(item book) []TeacherMark {
	// A teacher-looking colour on an old or manually marked line is not transcript evidence.
	var marks []TeacherMark
	for _, row := range item.annotations {
		if row.Chapter != nil || row.Page > item.pages || len(row.Comments) == 0 || !validHighlight(row) {
			continue
		}
		mark := TeacherMark{ID: row.ID, Page: row.Page, Rect: row.Rect, Quote: row.Quote}
		for _, comment := range row.Comments {
			if comment.Kind == "teacher" && strings.TrimSpace(comment.Quote) != "" {
				mark.Comments = append(mark.Comments, comment)
			}
		}
		if len(mark.Comments) > 0 {
			marks = append(marks, mark)
		}
	}
	return marks
}

// A passage can span several highlight rectangles. Keep its first anchor on each page.
func paginateTeacherMarks(marks []TeacherMark, offset int) (TeacherIndex, error) {
	result := TeacherIndex{Items: []TeacherMark{}}
	type passage struct {
		page              int
		text, quote, time string
	}
	seen := map[passage]bool{}
	size := 0
	for _, mark := range marks {
		comments := mark.Comments[:0]
		for _, comment := range mark.Comments {
			key := passage{mark.Page, mark.Quote, comment.Quote, comment.Timestamp}
			if !seen[key] {
				seen[key] = true
				comments = append(comments, comment)
			}
		}
		if len(comments) == 0 {
			continue
		}
		mark.Comments = comments
		result.Total++
		if result.Total <= offset || size >= 512*1024 || len(result.Items) >= 50 {
			continue
		}
		// One validated anchor fits the RPC frame, even with 100 long comments.
		raw, err := json.Marshal(mark)
		if err != nil {
			return TeacherIndex{}, contract.Unavailable("教材批注格式无效")
		}
		size += len(raw)
		result.Items = append(result.Items, mark)
	}
	return result, nil
}

func (service *Service) TeacherAnnotations(id string, offset int) (TeacherIndex, error) {
	result := TeacherIndex{Items: []TeacherMark{}}
	service.mu.Lock()
	item, ok := service.books[id]
	service.mu.Unlock()
	if !ok {
		return result, contract.InputInvalid(msgReselectTextbook)
	}
	if offset < 0 {
		return result, contract.InputInvalid("批注起始位置无效")
	}
	marks := teacherMarks(item)
	// PDF coordinates increase upwards. List the upper line first, then read left to right.
	sort.Slice(marks, func(i, j int) bool {
		a, b := marks[i], marks[j]
		if a.Page != b.Page {
			return a.Page < b.Page
		}
		if at, bt := a.Rect[1]+a.Rect[3], b.Rect[1]+b.Rect[3]; at != bt {
			return at > bt
		}
		if a.Rect[0] != b.Rect[0] {
			return a.Rect[0] < b.Rect[0]
		}
		return a.ID < b.ID
	})
	return paginateTeacherMarks(marks, offset)
}
