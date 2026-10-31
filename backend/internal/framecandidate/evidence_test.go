package framecandidate

import (
	"os"
	"strings"
	"testing"
)

func TestCandidateEvidenceImmutableBindingAndRecovery(t *testing.T) {
	root, id := t.TempDir(), strings.Repeat("a", 32)
	binding := Binding{SourceHash: strings.Repeat("b", 64), Sampler: "ppt-stable-gray-v1", Policy: "stable-initial", Region: [4]int{1, 2, 160, 90}, StepMs: 1000, GrayWidth: 160, GrayHeight: 90, DurationMs: 2000}
	samples := []Sample{{TimeMs: 500, SampleTimeMs: 0, Reason: "stable-initial", GrayHash: GrayHash([]byte{40}), Metrics: &GrayMetrics{MeanLight: 40}}}
	alternateReason := samples[0]
	alternateReason.Reason = "coverage"
	alternateReason.Metrics = nil
	if sampleID(binding, alternateReason) != sampleID(binding, samples[0]) {
		t.Fatal("candidate identity includes mutable measurement/reason")
	}
	reference, err := Publish(root, id, binding, samples)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := Publish(root, id, binding, samples)
	if err != nil || *repeated != *reference {
		t.Fatal("retry changed evidence")
	}
	manifest, err := Read(root, id, *reference, binding)
	if err != nil || manifest.Stage != "selected" || manifest.Samples[0].TimeMs != 500 {
		t.Fatalf("invalid selection %+v %v", manifest, err)
	}
	for _, change := range []Binding{func() Binding { b := binding; b.SourceHash = strings.Repeat("c", 64); return b }(), func() Binding { b := binding; b.Region[0]++; return b }(), func() Binding { b := binding; b.Sampler = "other-version"; return b }()} {
		if _, err := Read(root, id, *reference, change); err == nil {
			t.Fatal("mismatched source/crop/sampler accepted")
		}
	}
	changed := append([]Sample(nil), samples...)
	changed[0].TimeMs = 600
	if _, err := Publish(root, id, binding, changed); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	other := binding
	other.Region[0]++
	if _, err := Publish(root, id, other, samples); err != nil {
		t.Fatal("explicit new crop could not retain separate evidence")
	}
	path, _ := evidencePath(root, id, binding)
	body, _ := os.ReadFile(path)
	body = append(body, ' ')
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root, id, *reference, binding); err == nil {
		t.Fatal("tampered evidence accepted")
	}
}

func TestNonphysicalCandidateMetricsRejected(t *testing.T) {
	binding := Binding{SourceHash: strings.Repeat("a", 64), Sampler: "visual-balanced-gray-v1", Policy: "balanced", DurationMs: 1000}
	for _, metrics := range []*GrayMetrics{{MeanLight: 256}, {MeanChange: 256}, {NormalizedChange: 1.1}, {MeanChange: 1, NormalizedChange: 0}} {
		if _, err := Publish(t.TempDir(), strings.Repeat("b", 32), binding, []Sample{{TimeMs: 0, SampleTimeMs: 0, Reason: "balanced-change", Metrics: metrics}}); err == nil {
			t.Fatal("nonphysical metrics accepted")
		}
	}
}
