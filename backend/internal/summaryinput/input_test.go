package summaryinput_test

import (
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func TestSummaryInputExactlyMatchesExistingAIEncoding(t *testing.T) {
	segments := []documents.Segment{{Start: 1250, Text: "<公式>&中文\n引文\""}, {Start: 3661000, Text: "second"}}
	input := summaryinput.New("detailed", segments, 7)
	body, err := summaryinput.Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"mode":"detailed","cues":[{"cue":7,"time":"00:00:01","text":"<公式>&中文\n引文\""},{"cue":8,"time":"01:01:01","text":"second"}]}`
	if string(body) != expected || strings.HasSuffix(string(body), "\n") {
		t.Fatal(string(body))
	}
	actual, err := ai.JSONInputSHA256(input)
	shared, sharedErr := summaryinput.Digest(input)
	if err != nil || sharedErr != nil || actual != shared {
		t.Fatal("AI encoder differs from summary input contract", actual, shared, err, sharedErr)
	}
}
func TestSummaryChunksRetainRuneBoundaryAndOversizeCue(t *testing.T) {
	segments := []documents.Segment{{Text: strings.Repeat("课", 3)}, {Text: "ab"}, {Text: "c"}, {Text: strings.Repeat("x", 9)}}
	chunks := summaryinput.Chunks(segments, 5)
	if len(chunks) != 3 || len(chunks[0]) != 2 || len(chunks[1]) != 1 || len(chunks[2]) != 1 || chunks[2][0].Text != segments[3].Text {
		t.Fatal(chunks)
	}
}
