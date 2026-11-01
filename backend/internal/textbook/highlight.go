package textbook

import (
	"math"
	"regexp"
	"sort"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

type Highlight struct {
	ID        string     `json:"id"`
	Page      int        `json:"page"`
	Rect      [4]float64 `json:"rect"`
	Source    string     `json:"source"`
	Quote     string     `json:"quote"`
	Evidence  string     `json:"evidence"`
	Extension bool       `json:"extension"`
}

var highlightSources = map[string]bool{"teacher": true, "focus": true, "ppt": true, "senior": true}
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var highlightColors = map[string][3]float64{
	"teacher": {0.76, 0.61, 0.44},
	"focus":   {1, 0.57, 0.57},
	"ppt":     {0.53, 0.76, 1},
	"senior":  {1, 0.66, 0.81},
}

var highlightLabels = map[string]string{
	"focus": "老师重点", "senior": "学长学姐重点", "teacher": "老师讲授内容", "ppt": "PPT 内容",
}

var highlightPriority = map[string]int{"focus": 0, "senior": 1, "teacher": 2, "ppt": 3}

func validHighlight(row Highlight) bool {
	if !uuidPattern.MatchString(row.ID) || row.Page < 1 || !highlightSources[row.Source] ||
		len(row.Quote) < 1 || len([]rune(row.Quote)) > 3000 ||
		len(row.Evidence) < 1 || len([]rune(row.Evidence)) > 5000 {
		return false
	}
	for _, value := range row.Rect {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func sortHighlights(rows []Highlight) {
	sort.SliceStable(rows, func(left, right int) bool {
		return highlightPriority[rows[left].Source] < highlightPriority[rows[right].Source]
	})
}

func PrioritizedHighlights(rows []Highlight) []Highlight {
	return prioritizedHighlights(rows)
}

func subtractHighlightRect(rect, above [4]float64) [][4]float64 {
	x, y, width, height := rect[0], rect[1], rect[2], rect[3]
	ax, ay, aw, ah := above[0], above[1], above[2], above[3]
	left := math.Max(x, ax)
	bottom := math.Max(y, ay)
	right := math.Min(x+width, ax+aw)
	top := math.Min(y+height, ay+ah)
	if right <= left || top <= bottom {
		return [][4]float64{rect}
	}
	pieces := [][4]float64{}
	if bottom > y {
		pieces = append(pieces, [4]float64{x, y, width, bottom - y})
	}
	if top < y+height {
		pieces = append(pieces, [4]float64{x, top, width, y + height - top})
	}
	if left > x {
		pieces = append(pieces, [4]float64{x, bottom, left - x, top - bottom})
	}
	if right < x+width {
		pieces = append(pieces, [4]float64{right, bottom, x + width - right, top - bottom})
	}
	result := [][4]float64{}
	for _, piece := range pieces {
		if piece[2] > 0.2 && piece[3] > 0.2 {
			result = append(result, piece)
		}
	}
	return result
}

func subtractHighlightRects(rects [][4]float64, above [4]float64) [][4]float64 {
	next := [][4]float64{}
	for _, rect := range rects {
		next = append(next, subtractHighlightRect(rect, above)...)
	}
	return next
}

func prioritizedHighlights(rows []Highlight) []Highlight {
	sorted := append([]Highlight(nil), rows...)
	sortHighlights(sorted)
	result := []Highlight{}
	for _, row := range sorted {
		rects := [][4]float64{row.Rect}
		for _, above := range result {
			if above.Page != row.Page {
				continue
			}
			rects = subtractHighlightRects(rects, above.Rect)
		}
		for index, rect := range rects {
			copied := row
			if index > 0 {
				copied.ID = ids.New()
			}
			copied.Rect = rect
			result = append(result, copied)
		}
	}
	return result
}
