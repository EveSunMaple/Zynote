package api

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Native returns one PDF-space ink box per Unicode scalar; whitespace has no ink.
func (line textbookLine) quoteRect(quote string) ([4]float64, bool) {
	var empty [4]float64
	start := strings.Index(line.Text, quote)
	if start < 0 || start != strings.LastIndex(line.Text, quote) || strings.TrimSpace(quote) == "" {
		return empty, false
	}
	if len(line.Glyphs) == 0 {
		// An older helper only knows the full line's bounds. Never estimate a substring by length.
		if strings.TrimSpace(line.Text) == strings.TrimSpace(quote) && line.Width > 0 && line.Height > 0 {
			return [4]float64{line.X, line.Y, line.Width, line.Height}, true
		}
		return empty, false
	}
	if len(line.Glyphs) != utf8.RuneCountInString(line.Text) {
		return empty, false
	}
	index := utf8.RuneCountInString(line.Text[:start])
	left, bottom, right, top := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, ch := range quote {
		box := line.Glyphs[index]
		index++
		if unicode.IsSpace(ch) {
			continue
		}
		if box == nil || box[0] >= box[2] || box[1] >= box[3] {
			return empty, false
		}
		left, bottom = math.Min(left, box[0]), math.Min(bottom, box[1])
		right, top = math.Max(right, box[2]), math.Max(top, box[3])
	}
	return [4]float64{left, bottom, right - left, top - bottom}, true
}
