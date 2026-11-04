package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func TestHistoricalCompactRunBatchQuarantinesChangedRows(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"content": `{"processing":"complete","processed":[0,1],"patches":[{"id":1,"text":"这里线粒体提供能量。","uncertain":false,"reason":"术语纠正"}]}`,
		}}}})
	}))
	defer server.Close()
	source := []documents.Segment{{Start: 1, End: 2, Text: "这里线立体提供能量。"}, {Start: 3, End: 4, Text: "那里线立体提供能量。"}}
	run := &correctionRun{
		compact: true, compactVersion: compactProtocolHistorical, inv: &Invoker{}, prompt: compactCorrectionPrompt,
		client: ai.New(ai.Profile{BaseURL: server.URL, Model: "fixture"}), sourceSegments: source,
		contexts: []learning.CorrectionContext{{}}, active: map[int]string{}, started: time.Now(),
	}
	rows, err := run.runBatch(context.Background(), learning.CorrectionBatch{Rows: source}, 0)
	if err != nil || calls.Load() != 1 || rows[1].Text != source[1].Text || !rows[1].Uncertain || rows[1].Reason == "术语纠正" {
		t.Fatal(rows, err, calls.Load())
	}
}

func TestHistoricalCompactCheckpointQuarantinedOnResume(t *testing.T) {
	for _, completed := range []int{1, 2} {
		t.Run(map[int]string{1: "partial", 2: "complete"}[completed], func(t *testing.T) {
			state := &contract.State{DataRoot: t.TempDir()}
			write, err := storage.OpenWrite(filepath.Join(state.DataRoot, assistantDBFile))
			if err != nil {
				t.Fatal(err)
			}
			defer write.Close()
			if err := storage.BootstrapInto(write); err != nil {
				t.Fatal(err)
			}
			course, err := write.CreateCourse("fixture")
			if err != nil {
				t.Fatal(err)
			}
			lecture, err := write.CreateLecture(storage.ToStringValue(course["id"]), "fixture", "", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			prompt, err := os.ReadFile("../../../src/prompts/correction.txt")
			if err != nil {
				t.Fatal(err)
			}
			source := []documents.Segment{{Start: 1, End: 2, Text: "那里线立体提供能量。"}, {Start: 3, End: 4, Text: "下一句原文。"}}
			run := &correctionRun{
				ctx: context.Background(), inv: &Invoker{}, write: write, state: state, lecture: lecture,
				lectureID: storage.ToStringValue(lecture["id"]), prompt: string(prompt),
				mode: learning.CorrectionText, sourceSegments: source, started: time.Now(),
			}
			fp := learning.CorrectionFingerprint(run.profile.Fingerprint, source, compactCorrectionPrompt, nil, "", nil, run.mode)
			historical := []learning.CorrectedSegment{
				{Start: 1, End: 2, OriginalText: source[0].Text, Text: "这里线粒体提供能量。", Reason: "术语纠正"},
				{Start: 3, End: 4, OriginalText: source[1].Text, Text: source[1].Text},
			}
			if err := write.SaveCorrectionBatch(run.lectureID, fp, 2, historical[:completed], completed, compactProtocolHistorical); err != nil {
				t.Fatal(err)
			}
			if err := run.preparePlan(); err != nil {
				t.Fatal(err)
			}
			if !run.compact || run.compactVersion != compactProtocolHistorical || run.fingerprint != fp ||
				len(run.rows) != completed || run.rows[0].Text != source[0].Text || !run.rows[0].Uncertain {
				t.Fatal("historical resume changed identity or applied unsafe change", run.rows, run.fingerprint, run.compactVersion)
			}
			if completed == 2 {
				if len(run.batches) != 0 {
					t.Fatal("completed historical checkpoint would replay HTTP")
				}
				if _, err := run.finish(); err != nil {
					t.Fatal(err)
				}
				// Publication is independently guarded even if an unsafe row reappears.
				run.rows = historical
				if _, err := run.finish(); err != nil {
					t.Fatal(err)
				}
			} else if len(run.batches) == 0 || run.batches[0].Start != 1 {
				t.Fatal("paid prefix would replay", run.batches)
			}
		})
	}
}
