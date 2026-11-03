package summaryinput

import (
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"strings"
	"testing"
)

func TestExperimentalCandidatePartitionAndIndependentHalo(t *testing.T) {
	segments := []documents.Segment{{Start: 0, End: 1000, Text: "甲𠮷。乙。丙。丁。"}}
	base, err := PlanSpans("brief", segments, 8)
	if err != nil {
		t.Fatal(err)
	}
	input := BoundaryCandidates(base[0].Input, 8)
	if len(input.Candidates) == 0 {
		t.Fatal("missing candidate")
	}
	choices := []int{input.Candidates[0], -1}
	without, err := ExperimentalPlan("brief", segments, 8, 0, choices)
	if err != nil {
		t.Fatal(err)
	}
	with, err := ExperimentalPlan("brief", segments, 8, 2, choices)
	if err != nil {
		t.Fatal(err)
	}
	if len(with) != len(without) {
		t.Fatal("halo altered core partition")
	}
	for i := range with {
		if len(with[i].Input.Halo) == 0 {
			t.Fatal("halo absent")
		}
		with[i].Input.Halo = nil
	}
	if !EqualPlans(with, without) {
		t.Fatal("halo changed ownership")
	}
	if _, err := ExperimentalPlan("brief", segments, 8, 0, []int{1, -1}); err == nil {
		t.Fatal("forged boundary accepted")
	}
	for _, raw := range []string{`{}`, `{"boundary":999}`, `{"boundary":1}`, `null`} {
		if _, state := BoundaryChoice([]byte(raw), input); state != "fallback_invalid" {
			t.Fatal("bad proposal not explicit fallback")
		}
	}
	long := SpanInput{Cues: []SpanCue{{Text: strings.Repeat("。", 20000)}}}
	if len(BoundaryCandidates(long, 20000).Candidates) > 512 {
		t.Fatal("candidate count unbounded")
	}
}
