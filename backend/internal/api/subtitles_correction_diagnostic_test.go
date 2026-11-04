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
