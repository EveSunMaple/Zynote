package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if ok, err := freshCompactCorrection(ctx, root, "lecture", "old", "", string(prompt)); err != nil || ok {
		t.Fatal("fresh canonical must use legacy", err)
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
	if _, err := freshCompactCorrection(ctx, root, "lecture", "old", "checkpoint-only-compact", string(prompt), "checkpoint-only-compact"); err == nil {
		t.Fatal("compact checkpoint bypassed historical legacy job")
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

func TestHistoricalCorrectionCacheGuardDoesNotCallProvider(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "compact"}[compact], func(t *testing.T) {
			ctx := context.Background()
			ledger, err := generationjob.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer ledger.Close()
			fence, err := ledger.Acquire(ctx, "historical", 0, 8)
			if err != nil {
				t.Fatal(err)
			}
			source := []documents.Segment{{Start: 12000, End: 15000, Text: "前三篇主要对应的是牙体，后两片对应的是牙髓。"}}
			batch := learning.CorrectionBatch{Start: 27, Rows: source}
			run := &correctionRun{compact: compact, prompt: "historical-prompt", fingerprint: "unchanged-paid-fingerprint", client: ai.New(ai.Profile{BaseURL: "https://fixture.invalid", Model: "fixture"}), contexts: []learning.CorrectionContext{{}}, sourceSegments: source, requestLedger: ledger, requestFence: fence}
			input, images := correctionEffectiveRequest(run, batch, 0)
			digest, err := run.client.JSONRequestDigest(ctx, run.prompt, input, ai.JSONOptions{Images: images})
			if err != nil {
				t.Fatal(err)
			}
			version := 1
			if compact {
				version = 2
			}
			identityBody, _ := json.Marshal(struct {
				RecipeVersion int
				Fingerprint   string
				Batch         learning.CorrectionBatch
				WireDigest    string
				RepairPolicy  any
			}{version, run.fingerprint, batch, digest, ai.JSONRecipePolicy()})
			sum := sha256.Sum256(identityBody)
			historical := []learning.CorrectedSegment{{Start: 12000, End: 15000, OriginalText: source[0].Text, Text: "后两篇", Reason: "量词纠正"}}
			body, _ := json.Marshal(historical)
			if err := ledger.SaveSuccess(ctx, fence, "correction:27:1", hex.EncodeToString(sum[:]), body); err != nil {
				t.Fatal(err)
			}
			calls := 0
			rows, err := run.withCachedBatch(ctx, batch, 0, func(context.Context, learning.CorrectionBatch, int) ([]learning.CorrectedSegment, error) {
				calls++
				return nil, nil
			})
			if err != nil || calls != 0 || len(rows) != 1 || rows[0].Text != source[0].Text || !rows[0].Uncertain || rows[0].Reason == "" {
				t.Fatal("cache unsafe/replayed", rows, calls, err)
			}
			if run.fingerprint != "unchanged-paid-fingerprint" {
				t.Fatal("identity changed")
			}
		})
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
	if err != nil || calls != 1 || rows[0].Text != "线立体" || !rows[0].Uncertain {
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
				// Correction keeps Images-only options for historical identity.
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
					content = `{"processing":"complete","processed":[0,1,2],"patches":[{"id":1,"text":"线粒体提供能量","uncertain":false,"reason":"课件术语明确"},{"id":2,"text":"甲","uncertain":true,"reason":"无法确认，不猜改"}]}`
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
				if compact && source[i].Text != gold[i].Text {
					if row.Text != source[i].Text || !row.Uncertain {
						t.Fatal("historical compact change not quarantined", rows)
					}
					continue
				}
				if row.Text != gold[i].Text || row.Start != source[i].Start || row.End != source[i].End || row.OriginalText != source[i].Text {
					t.Fatal("gold/negation/formula/time invariant", rows)
				}
			}
		})
	}
}

func TestHistoricalCompactCorrectionDoesNotIntroduceUnversionedThinkingFlag(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("bad request")
		}
		if _, found := body["enable_thinking"]; found {
			t.Error("unversioned thinking flag changed historical wire")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": `{"processing":"complete","processed":[0],"patches":[]}`}}}})
	}))
	defer server.Close()
	batch := learning.CorrectionBatch{Rows: []documents.Segment{{Text: "保留原文"}}}
	run := &correctionRun{compact: true, inv: &Invoker{}, prompt: compactCorrectionPrompt, client: ai.New(ai.Profile{BaseURL: server.URL + "/dashscope/v1", Model: "fixture"}), sourceSegments: batch.Rows, contexts: []learning.CorrectionContext{{}}, active: map[int]string{}, started: time.Now()}
	input, images := correctionEffectiveRequest(run, batch, 0)
	ctx := context.Background()
	historical, err := run.client.JSONRequestDigest(ctx, run.prompt, input, ai.JSONOptions{Images: images})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := run.client.JSONRequestDigest(ctx, run.prompt, input, ai.JSONOptions{Images: images, DisableThinking: true})
	if err != nil || historical == changed {
		t.Fatal("thinking distinction lost", err)
	}
	if _, err := run.runBatch(ctx, batch, 0); err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestCorrectionUnsafeHTTPResultGuardedWithoutRepairAtNonzeroStart(t *testing.T) {
	source := []documents.Segment{{Start: 12000, End: 15000, Text: "前三篇主要对应的是牙体，后两片对应的是牙髓。"}, {Start: 27000, End: 30000, Text: "呃，你不要拍照片啊，这个现在这个手机都很好，高清照片，一个照片，这是个AM。"}}
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "compact"}[compact], func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				patches := []any{map[string]any{"id": 27, "text": "后两篇", "uncertain": false, "reason": "量词纠正"}, map[string]any{"id": 28, "text": "呃，然后转化成PDF文件提交到这个平台上面去。", "uncertain": false, "reason": "PDF大小写"}}
				var output any = patches
				if compact {
					output = map[string]any{"processing": "complete", "processed": []int{27, 28}, "patches": patches}
				}
				content, _ := json.Marshal(output)
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}})
			}))
			defer server.Close()
			batch := learning.CorrectionBatch{Start: 27, Rows: source}
			run := &correctionRun{compact: compact, inv: &Invoker{}, prompt: "fixture", client: ai.New(ai.Profile{BaseURL: server.URL, Model: "fixture"}), sourceSegments: source, contexts: []learning.CorrectionContext{{}}, active: map[int]string{}, started: time.Now()}
			rows, err := run.runBatch(context.Background(), batch, 0)
			if err != nil || calls.Load() != 1 {
				t.Fatal("guard caused paid repair", err, calls.Load())
			}
			for i, row := range rows {
				if row.Text != source[i].Text || row.OriginalText != source[i].Text || row.Start != source[i].Start || row.End != source[i].End || !row.Uncertain || row.Reason == "" {
					t.Fatal("unsafe publication", rows)
				}
			}
		})
	}
}
