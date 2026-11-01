package textbook

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type Comment struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Quote     string `json:"quote,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type ChapterRef struct {
	Page  int        `json:"page"`
	Line  int        `json:"line"`
	Quote string     `json:"quote"`
	Rect  [4]float64 `json:"rect"`
}

type ChapterSummary struct {
	Title    string       `json:"title"`
	Text     string       `json:"text"`
	LastPage int          `json:"lastPage"`
	Refs     []ChapterRef `json:"refs"`
}

type Highlight struct {
	Comments  []Comment       `json:"comments,omitempty"`
	Chapter   *ChapterSummary `json:"chapter,omitempty"`
	ID        string          `json:"id"`
	Page      int             `json:"page"`
	Rect      [4]float64      `json:"rect"`
	Ink       [][2]float64    `json:"ink,omitempty"`
	Source    string          `json:"source"`
	Quote     string          `json:"quote"`
	Evidence  string          `json:"evidence"`
	Extension bool            `json:"extension"`
}

var highlightSources = map[string]bool{"teacher": true, "focus": true, "ppt": true, "senior": true, "ai": true, "personal": true}
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var highlightColors = map[string][3]float64{
	"teacher": {0.76, 0.61, 0.44},
	"focus":   {1, 0.57, 0.57},
	"ai":      {0.55, 0.81, 0.68}, "personal": {0.99, 0.85, 0.45},
	"ppt":    {0.53, 0.76, 1},
	"senior": {1, 0.66, 0.81},
}

var highlightLabels = map[string]string{
	"focus": "老师重点", "senior": "学长学姐重点", "teacher": "老师讲授内容", "ppt": "PPT 内容", "ai": "AI 批注", "personal": "我的笔记",
}

var highlightPriority = map[string]int{"focus": 0, "senior": 1, "teacher": 2, "ppt": 3, "ai": 4, "personal": 5}

func validHighlightCore(row Highlight) bool {
	return uuidPattern.MatchString(row.ID) && row.Page >= 1 && highlightSources[row.Source] &&
		len(row.Quote) >= 1 && len([]rune(row.Quote)) <= 3000 &&
		len(row.Evidence) >= 1 && len([]rune(row.Evidence)) <= 5000
}

func validComment(c Comment) bool {
	if !boundedText(c.Text, 5000) || (c.Kind != "ai" && c.Kind != "personal" && c.Kind != "teacher") {
		return false
	}
	if c.Kind == "teacher" && !boundedText(c.Quote, 3000) {
		return false
	}
	return len([]rune(c.Quote)) <= 3000 && len([]rune(c.Timestamp)) <= 100
}

func validChapter(row Highlight, chapter *ChapterSummary) bool {
	if row.Source != "ai" || len(row.Ink) > 0 || len(row.Comments) > 0 ||
		!boundedText(chapter.Title, 500) || !boundedText(chapter.Text, 600) ||
		chapter.LastPage < row.Page || len(chapter.Refs) < 1 || len(chapter.Refs) > 3 {
		return false
	}
	for _, ref := range chapter.Refs {
		if ref.Page < row.Page || ref.Page > chapter.LastPage || ref.Line < 1 || !boundedText(ref.Quote, 500) ||
			ref.Rect[2] <= 0 || ref.Rect[3] <= 0 {
			return false
		}
	}
	return true
}

func validInk(row Highlight) bool {
	if len(row.Ink) < 2 || len(row.Ink) > 4096 {
		return false
	}
	for _, point := range row.Ink {
		x, y := point[0], point[1]
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) ||
			x < row.Rect[0]-.01 || x > row.Rect[0]+row.Rect[2]+.01 || y < row.Rect[1]-.01 || y > row.Rect[1]+row.Rect[3]+.01 {
			return false
		}
	}
	return true
}

func validHighlight(row Highlight) bool {
	if !validHighlightCore(row) || len(row.Comments) > 100 {
		return false
	}
	for _, comment := range row.Comments {
		if !validComment(comment) {
			return false
		}
	}
	if row.Chapter != nil && !validChapter(row, row.Chapter) {
		return false
	}
	for _, value := range row.Rect {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	if row.Rect[2] <= 0 || row.Rect[3] <= 0 {
		return false
	}
	return row.Ink == nil || validInk(row)
}

func sortHighlights(rows []Highlight) {
	sort.SliceStable(rows, func(left, right int) bool {
		return highlightPriority[rows[left].Source] < highlightPriority[rows[right].Source]
	})
}

func PrioritizedHighlights(rows []Highlight) []Highlight {
	return prioritizedHighlights(rows)
}

func boundedText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len([]rune(value)) <= limit
}

func sameAnchor(a, b Highlight) bool {
	if a.Page != b.Page || a.Quote != b.Quote || !slices.Equal(a.Ink, b.Ink) {
		return false
	}
	for i, n := range a.Rect {
		// PDF Rect uses single-precision numbers; reading it back introduces sub-point rounding.
		if math.Abs(n-b.Rect[i]) > 0.001 {
			return false
		}
	}
	return true
}

func chapterMatches(saved, row Highlight) bool {
	return row.Chapter != nil && saved.Chapter != nil && row.Page == saved.Page && row.Chapter.Title == saved.Chapter.Title
}

func mergeOne(result []Highlight, row Highlight) bool {
	for i, saved := range result {
		if row.Chapter != nil || saved.Chapter != nil {
			if !chapterMatches(saved, row) {
				continue
			}
			row.ID = saved.ID
			result[i] = row
			return true
		}
		if !sameAnchor(row, saved) {
			continue
		}
		// Legacy evidence has no explicit author. Keep distinct older annotations intact.
		if (len(row.Comments) == 0 || len(saved.Comments) == 0) && (row.Source != saved.Source || row.Evidence != saved.Evidence || row.Extension != saved.Extension) {
			continue
		}
		merged := slices.Clone(saved.Comments)
		for _, comment := range row.Comments {
			if !slices.Contains(merged, comment) {
				merged = append(merged, comment)
			}
		}
		result[i].Comments = merged
		return true
	}
	return false
}

func mergeHighlights(existing, additions []Highlight) []Highlight {
	result := slices.Clone(existing)
	for _, row := range additions {
		if !mergeOne(result, row) {
			result = append(result, row)
		}
	}
	sortHighlights(result)
	return result
}

func prioritizedHighlights(rows []Highlight) []Highlight {
	return mergeHighlights(nil, rows)
}
