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
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

func TestCanonicalCorrectionSelectionPreservesLegacyLiabilityAndCustomPrompt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	prompt, err := os.ReadFile(filepath.Join("..", "..", "..", "src", "prompts", "correction.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalCorrectionPrompt(string(prompt)) {
		t.Fatal("packaged prompt identity drift")
	}
	if ok, err := freshCompactCorrection(ctx, root, "lecture", "old", "", string(prompt)); err != nil || !ok {
		t.Fatal("fresh canonical not eligible", err)
	}
	if ok, _ := freshCompactCorrection(ctx, root, "lecture", "old", "", string(prompt)+" 自定义"); ok {
		t.Fatal("custom prompt changed")
	}
	if ok, _ := freshCompactCorrection(ctx, root, "lecture", "old", "old", string(prompt)); ok {
		t.Fatal("historical checkpoint changed")
	}
	ledger, err := generationjob.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ledger.Acquire(ctx, "correction-lecture-old", 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Close()
	if ok, _ := freshCompactCorrection(ctx, root, "lecture", "old", "", string(prompt)); ok {
		t.Fatal("prior attempted job bypassed by new format")
	}
	ledger, err = generationjob.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ledger.Acquire(ctx, "correction-lecture-new", 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Close()
	if _, err := freshCompactCorrection(ctx, root, "lecture", "old", "", string(prompt), "new"); err == nil {
		t.Fatal("two protocol histories silently switched")
	}
	if ok, err := freshCompactCorrection(ctx, root, "lecture", "absent", "new", string(prompt), "new"); err != nil || !ok {
		t.Fatal("compact resume changed recipe", err)
	}
}

func TestCompactCorrectionRequestUsesOneBasedRowIDs(t *testing.T) {
	batch := learning.CorrectionBatch{Start: 7, Rows: []documents.Segment{{Text: "甲"}, {Text: "乙"}}}
	run := &correctionRun{compact: true, contexts: []learning.CorrectionContext{{}}}
	input, _ := correctionRequestFor(run, batch, 0, nil)
	if input.Rows[0].ID != 8 || input.Rows[1].ID != 9 {
		t.Fatalf("compact ids must be one-based: %+v", input.Rows)
	}
	run.compact = false
	input, _ = correctionRequestFor(run, batch, 0, nil)
	if input.Rows[0].ID != 7 || input.Rows[1].ID != 8 {
		t.Fatalf("legacy ids changed: %+v", input.Rows)
	}
}

func TestCompactCorrectionDurableSuccessReusesGenerationWithoutPaidReplay(t *testing.T) {
	ctx := context.Background()
	ledger, err := generationjob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	fence, err := ledger.Acquire(ctx, "compact-fixture", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	batch := learning.CorrectionBatch{Rows: []documents.Segment{{Start: 0, End: 1000, Text: "线立体"}}}
	run := &correctionRun{compact: true, prompt: compactCorrectionPrompt, fingerprint: "compact-source-v2", client: ai.New(ai.Profile{BaseURL: "https://fixture.invalid", Model: "fixture"}), contexts: []learning.CorrectionContext{{}}, requestLedger: ledger, requestFence: fence}
	calls := 0
	paid := func(context.Context, learning.CorrectionBatch, int) ([]learning.CorrectedSegment, error) {
		calls++
		return []learning.CorrectedSegment{{Start: 0, End: 1000, OriginalText: "线立体", Text: "线粒体", Reason: "课件术语"}}, nil
	}
	if _, err := run.withCachedBatch(ctx, batch, 0, paid); err != nil {
		t.Fatal(err)
	}
	run.requestFence, err = ledger.Acquire(ctx, "compact-fixture", 1, 8)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := run.withCachedBatch(ctx, batch, 0, paid)
	if err != nil || calls != 1 || rows[0].Text != "线粒体" {
		t.Fatal("compact cache replayed", calls, err)
	}
	run.compact = false
	run.prompt = "custom legacy"
	if _, err := run.withCachedBatch(ctx, batch, 0, paid); err != nil || calls != 2 {
		t.Fatal("protocol cache identity collided", calls, err)
	}
}

func TestCompactCorrectionActualHTTPAndRepairPreserveGold(t *testing.T) {
	gold := []documents.Segment{{Start: 0, End: 1000, Text: "不考这个公式 x^2"}, {Start: 1000, End: 2000, Text: "线粒体提供能量"}, {Start: 2000, End: 3000, Text: "可能是甲或乙"}}
	source := append([]documents.Segment(nil), gold...)
	source[1].Text = "线立体提供能量"
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "compact"}[compact], func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var request struct {
					Messages []ai.Message `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad wire")
				}
				var content string
				if n == 1 {
					content = `{"processing":"complete","processed":[0,1],"patches":[]}`
				} else if compact {
					content = `{"processing":"complete","processed":[1,2,3],"patches":[{"id":2,"text":"线粒体提供能量","uncertain":false,"reason":"课件术语明确"},{"id":3,"text":"甲","uncertain":true,"reason":"无法确认，不猜改"}]}`
				} else {
					content = `[{"id":0,"text":"不考这个公式 x^2","uncertain":false,"reason":""},{"id":1,"text":"线粒体提供能量","uncertain":false,"reason":"课件术语明确"},{"id":2,"text":"可能是甲或乙","uncertain":true,"reason":"无法确认，不猜改"}]`
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			}))
			defer server.Close()
			run := &correctionRun{compact: compact, inv: &Invoker{}, prompt: "controlled legacy", client: ai.New(ai.Profile{BaseURL: server.URL, Model: "fixture"}), sourceSegments: source, contexts: []learning.CorrectionContext{{Courseware: "线粒体"}}, batches: []learning.CorrectionBatch{{Start: 0, Rows: source}}, active: map[int]string{}, started: time.Now()}
			if compact {
				run.prompt = compactCorrectionPrompt
			}
			rows, err := run.runBatch(context.Background(), run.batches[0], 0)
			if err != nil || calls.Load() != 2 {
				t.Fatal(err, calls.Load())
			}
			for i, row := range rows {
				if row.Text != gold[i].Text || row.Start != source[i].Start || row.End != source[i].End || row.OriginalText != source[i].Text {
					t.Fatal("gold/negation/formula/time invariant", rows)
				}
			}
		})
	}
}
