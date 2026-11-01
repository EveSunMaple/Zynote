package api

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type textbookEvidence struct {
	ID        int    `json:"id"`
	Source    string `json:"source"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
}

func evidenceSource(sources textbookSources, kind string) string {
	text := textbookSourceValue(sources, kind)
	if kind == "ppt" {
		// The courseware importer appends diagnostics after the original material.
		text, _, _ = strings.Cut(text, "\n\n[课件来源限制]\n")
	}
	return text
}

func meaningfulEvidence(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

func makeTextbookEvidence(sources textbookSources) []textbookEvidence {
	var entries []textbookEvidence
	for _, kind := range []string{"teacher", "focus", "ppt", "senior"} {
		if kind == "teacher" && len(sources.Replay) != 0 {
			for _, record := range sources.Replay {
				// Chunk long utterances without inventing a timestamp within the segment.
				for _, item := range makeTextbookEvidence(textbookSources{Teacher: record.Text}) {
					item.ID, item.Timestamp = len(entries)+1, record.Timestamp
					entries = append(entries, item)
				}
			}
			continue
		}
		body := evidenceSource(sources, kind)
		start, offset, count := 0, 0, 0
		flush := func(end int) {
			text := strings.TrimSpace(body[start:end])
			if meaningfulEvidence(text) {
				entries = append(entries, textbookEvidence{ID: len(entries) + 1, Source: kind, Text: text})
			}
			start, count = end, 0
		}
		for line := range strings.Lines(body) {
			trimmed := strings.TrimSpace(line)
			end := offset + len(line)
			if trimmed == "" || (kind == "ppt" && textbookPageMarker.FindString(trimmed) == trimmed) {
				flush(offset)
				start, offset = end, end
				continue
			}
			for i, r := range line {
				count++
				// Keep short subtitle fragments together. Never send a whole unpunctuated chapter as one quote.
				sentenceEnd := strings.ContainsRune("。！？!?；;", r)
				if r == '.' {
					next, size := utf8.DecodeRuneInString(line[i+1:])
					sentenceEnd = size == 0 || unicode.IsSpace(next)
				}
				if count >= 360 || (count >= 230 && unicode.IsSpace(r)) || (count >= 24 && sentenceEnd) {
					_, width := utf8.DecodeRuneInString(line[i:])
					flush(offset + i + width)
				}
			}
			if count >= 24 || kind != "teacher" {
				flush(end)
			}
			offset = end
		}
		flush(len(body))
	}
	return entries
}

func cjkEvidenceRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// Preserve separators between Western words and numbers, and all punctuation/operators.
// The byte offsets let callers restore the exact original quote, including subtitle newlines.
func visitEvidenceRunes(text string, visit func(r rune, start, end int) bool) {
	var prev rune
	space := -1
	for i, r := range text {
		if unicode.IsSpace(r) {
			if space < 0 {
				space = i
			}
			continue
		}
		if space >= 0 && space > 0 && !cjkEvidenceRune(prev) && !cjkEvidenceRune(r) && !visit(' ', space, i) {
			return
		}
		_, width := utf8.DecodeRuneInString(text[i:])
		if !visit(r, i, i+width) {
			return
		}
		prev, space = r, -1
	}
}

func evidenceQuote(source, claimed string) (string, bool) {
	claimed = strings.TrimSpace(claimed)
	if !meaningfulEvidence(claimed) || utf8.RuneCountInString(claimed) > 5000 {
		return "", false
	}
	if i := strings.Index(source, claimed); i >= 0 {
		return source[i : i+len(claimed)], true
	}
	normalized := func(text string) string {
		var result strings.Builder
		result.Grow(len(text))
		visitEvidenceRunes(text, func(r rune, _, _ int) bool { result.WriteRune(r); return true })
		return result.String()
	}
	needle := normalized(claimed)
	from := strings.Index(normalized(source), needle)
	if from < 0 {
		return "", false
	}
	// Recover the byte range only after a match, without allocating a per-character index for long transcripts.
	position, first, last := 0, 0, 0
	visitEvidenceRunes(source, func(r rune, start, end int) bool {
		if position == from {
			first = start
		}
		position += utf8.RuneLen(r)
		if position == from+len(needle) {
			last = end
			return false
		}
		return true
	})
	quote := source[first:last]
	if utf8.RuneCountInString(quote) > 5000 {
		return "", false
	}
	return quote, true
}

func resolveTextbookEvidence(sources textbookSources, catalog []textbookEvidence, source string, id *int, claimed string) (string, bool) {
	if id == nil {
		return evidenceQuote(evidenceSource(sources, source), claimed)
	}
	if *id < 1 || *id > len(catalog) {
		return "", false
	}
	item := catalog[*id-1]
	if item.ID != *id || (source != "" && source != item.Source) {
		return "", false
	}
	if claimed == "" {
		return item.Text, item.Text != ""
	}
	return evidenceQuote(item.Text, claimed)
}
