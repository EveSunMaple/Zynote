package api

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

// This is an opt-in local protocol diagnostic, not a model benchmark. The gold
// is the public transcript with deterministic spelling corruptions injected.
func TestCorrectionProtocolBaselineDiagnostic(t *testing.T) {
	if os.Getenv("ZYNOTE_CORRECTION_DIAGNOSTIC") != "1" {
		t.Skip("explicit local diagnostic only")
	}
	body, err := os.ReadFile("F:/BiliSum-dependencies/zynote-acceptance/full-course-20261031/mit-computation-full.srt")
	if err != nil {
		t.Fatal(err)
	}
	gold, err := documents.ParseSrt(string(body))
	if err != nil {
		t.Fatal(err)
	}
	source := append([]documents.Segment(nil), gold...)
	injected := 0
	for i := range source {
		for _, pair := range [][2]string{{"computer", "compuetr"}, {"Python", "Pythno"}, {"program", "progrma"}} {
			if strings.Contains(source[i].Text, pair[0]) && i%3 == 0 {
				source[i].Text = strings.Replace(source[i].Text, pair[0], pair[1], 1)
				injected++
				break
			}
		}
	}
	started := time.Now()
	batches, err := learning.PlanCorrection(source, 0, 4096)
	planNS := time.Since(started).Nanoseconds()
	if err != nil {
		t.Fatal(err)
	}
	contextFor := learning.CorrectionContextFor("[第1页]\ncomputer Python program\n[第2页]\nDo not change negation, numerical values or formula meaning.", nil)
	var contextNS, validationNS int64
	inputBytes, fullBytes, compactBytes := 0, 0, 0
	counts := []int{}
	lengths := []int{}
	for index, batch := range batches {
		started = time.Now()
		ctx := contextFor(batch, source, learning.CorrectionText)
		contextNS += time.Since(started).Nanoseconds()
		run := &correctionRun{contexts: make([]learning.CorrectionContext, len(batches))}
		run.contexts[index] = ctx
		input, _ := correctionEffectiveRequest(run, batch, index)
		wire, _ := json.Marshal(input)
		inputBytes += len(wire)
		counts = append(counts, len(batch.Rows))
		rows := []map[string]any{}
		processed := []int{}
		patches := []map[string]any{}
		for offset, row := range batch.Rows {
			id := batch.Start + offset
			lengths = append(lengths, len([]rune(row.Text)))
			reason := ""
			if row.Text != gold[id].Text {
				reason = "deterministic injected spelling error"
				patches = append(patches, map[string]any{"id": id, "text": gold[id].Text, "uncertain": false, "reason": reason})
			}
			processed = append(processed, id)
			rows = append(rows, map[string]any{"id": id, "text": gold[id].Text, "uncertain": false, "reason": reason})
		}
		full, _ := json.Marshal(rows)
		compact, _ := json.Marshal(map[string]any{"processing": "complete", "processed": processed, "patches": patches})
		fullBytes += len(full)
		compactBytes += len(compact)
		started = time.Now()
		if _, err := learning.ValidateCorrectionRows(full, batch); err != nil {
			t.Fatal(err)
		}
		validationNS += time.Since(started).Nanoseconds()
	}
	sort.Ints(counts)
	sort.Ints(lengths)
	report := map[string]any{"fixture": "public MIT full course + deterministic spelling injection", "sourceCues": len(source), "injectedErrorCues": injected, "batchCount": len(batches), "batchRowsMin": counts[0], "batchRowsMedian": counts[len(counts)/2], "batchRowsMax": counts[len(counts)-1], "rowCodepointsMedian": lengths[len(lengths)/2], "rowCodepointsMax": lengths[len(lengths)-1], "requestPayloadJSONBytes": inputBytes, "imageBytes": 0, "legacyOutputJSONBytes": fullBytes, "proposedPatchOutputJSONBytes": compactBytes, "responseByteReduction": 1 - float64(compactBytes)/float64(fullBytes), "planningNS": planNS, "contextNS": contextNS, "legacyValidationNS": validationNS, "profile": "controlled text fixture, conservative4096outputtokens, no user profile read", "tokenUsage": nil, "networkCalls": 0, "quality": "oracle protocol fixture only, not real model quality", "requestCountPrediction": "unchanged; preserve conservative planner initially"}
	encoded, _ := json.Marshal(report)
	t.Log(string(encoded))
}

func TestCorrectionFidelityPublicResourceDiagnostic(t *testing.T) {
	if os.Getenv("ZYNOTE_CORRECTION_DIAGNOSTIC") != "1" {
		t.Skip("explicit local diagnostic only")
	}
	body, err := os.ReadFile("F:/BiliSum-dependencies/zynote-acceptance/full-course-20261031/mit-computation-full.srt")
	if err != nil {
		t.Fatal(err)
	}
	source, err := documents.ParseSrt(string(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"前三篇主要对应的是牙体，后两片对应的是牙髓。", "呃，你不要拍照片啊，这个现在这个手机都很好，高清照片，一个照片，这是个AM。", "呃，然后转化成PDF文件提交到这个平台上面去。", "不考这个公式 x^2", "𠮷保持原样"} {
		start := int64(len(source)) * 2000
		source = append(source, documents.Segment{Start: start, End: start + 1000, Text: text})
	}
	rows := make([]learning.CorrectedSegment, len(source))
	maximum := 0
	for i, row := range source {
		maximum = max(maximum, len([]rune(row.Text)))
		rows[i] = learning.CorrectedSegment{Start: row.Start, End: row.End, OriginalText: row.Text, Text: row.Text + "。", Reason: "controlled local punctuation edit"}
	}
	rows[len(rows)-5].Text = "后两篇"
	rows[len(rows)-4].Text = source[len(source)-3].Text
	times := []int64{}
	uncertain := 0
	for repeat := 0; repeat < 5; repeat++ {
		started := time.Now()
		guarded, err := learning.GuardCorrectedSegments(rows, source)
		times = append(times, time.Since(started).Nanoseconds())
		if err != nil {
			t.Fatal(err)
		}
		uncertain = 0
		for _, row := range guarded {
			if row.Uncertain {
				uncertain++
			}
		}
	}
	report, _ := json.Marshal(map[string]any{"cues": len(source), "maxSourceCodepoints": maximum, "passesNS": times, "uncertainCues": uncertain, "networkCalls": 0, "scope": "five local full-prefix guard passes; all837 cues proposed changed; controlled punctuation plus two destructive proposals; not provider or model quality"})
	t.Log(string(report))
}
