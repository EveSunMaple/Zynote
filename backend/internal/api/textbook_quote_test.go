package api

import "testing"

func TestTextbookQuoteUsesRealGlyphs(t *testing.T) {
	line := textbookLine{
		Text: "𝑊 i界𝑥",
		Glyphs: []*[4]float64{
			{30, 50, 50, 60}, nil, {53, 49, 55, 59}, {55, 48, 70, 61}, {70, 50, 80, 60},
		},
	}
	got, ok := line.quoteRect("i界")
	if !ok || got != [4]float64{53, 48, 17, 13} {
		t.Fatalf("glyph union: %v %v", got, ok)
	}
	line.Glyphs[2] = nil
	if _, ok := line.quoteRect("i界"); ok {
		t.Fatal("accepted a quote missing a glyph")
	}
}

func TestTextbookQuoteRejectsAmbiguousAndUnmeasuredSubstrings(t *testing.T) {
	line := textbookLine{Text: "ababa", X: 82, Y: 160, Width: 35, Height: 9}
	for _, quote := range []string{"aba", "bab"} {
		if _, ok := line.quoteRect(quote); ok {
			t.Errorf("accepted %q without unique glyph geometry", quote)
		}
	}
	if r, ok := line.quoteRect("ababa"); !ok || r != [4]float64{82, 160, 35, 9} {
		t.Fatalf("legacy full-line bounds: %v %v", r, ok)
	}
}
