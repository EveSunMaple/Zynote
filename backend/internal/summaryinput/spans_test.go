package summaryinput

import (
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"testing"
)

func TestCorePartitionUsesCodepointsAndParentTimes(t *testing.T) {
	segments := []documents.Segment{{Start: 1000, End: 9000, Text: "甲😀乙𠮷"}}
	chunks, err := PlanSpans("brief", segments, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[0].Input.Cues[0].Text != "甲😀" || chunks[1].Input.Cues[0].Text != "乙𠮷" {
		t.Fatalf("UTF16 split: %+v", chunks)
	}
	if chunks[1].Input.Cues[0].CoreStart != 2 || chunks[1].Input.Cues[0].CoreEnd != 4 || chunks[1].Input.Cues[0].StartMs != 1000 || chunks[1].Input.Cues[0].EndMs != 9000 {
		t.Fatal("invented subcue alignment", chunks)
	}
	chunks[0].CoreSpans[0].EndCodepoint = 3
	if err := ValidateCorePartition(segments, chunks); err == nil {
		t.Fatal("overlap accepted")
	}
}

func TestEvidenceSpanCannotIncreaseCoreCoverage(t *testing.T) {
	segments := []documents.Segment{{Text: "甲😀乙𠮷"}}
	chunks := []SpanChunk{{CoreSpans: []contract.CoreSpan{{ParentCueID: 0, StartCodepoint: 0, EndCodepoint: 2}}, Input: SpanInput{Cues: []SpanCue{{Cue: 0, Text: "甲😀乙𠮷"}}}}}
	if err := ValidateCorePartition(segments, chunks); err == nil {
		t.Fatal("wire/halo text counted as core coverage")
	}
}
