package learning

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

func TestCompactCorrectionRejectsDestructiveCueReplacement(t *testing.T) {
	source := "前三篇主要对应的是牙体，后两片对应的是牙髓。"
	photo := "呃，你不要拍照片啊，这个现在这个手机都很好，高清照片，一个照片，这是个AM。"
	pdf := "呃，然后转化成PDF文件提交到这个平台上面去。"
	batch := CorrectionBatch{Start: 27, Rows: []documents.Segment{{Text: source}, {Text: photo}, {Text: pdf}}}
	for name, patch := range map[string]map[string]any{
		"fragment_overwrites_sentence":     {"id": 27, "text": "后两篇", "uncertain": false, "reason": "量词片改为篇"},
		"neighbor_sentence_overwrites_cue": {"id": 28, "text": pdf, "uncertain": false, "reason": "PDF大小写"},
		"relative_id":                      {"id": 0, "text": source, "uncertain": false, "reason": "依据"},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"processing": "complete", "processed": []int{27, 28, 29}, "patches": []any{patch}})
			rows, err := ValidateCompactCorrectionWithBase(raw, batch, false)
			if name == "relative_id" {
				if err == nil {
					t.Fatal("relative ID accepted")
				}
				return
			}
			index := 0
			if name == "neighbor_sentence_overwrites_cue" {
				index = 1
			}
			if err != nil || rows[index].Text != batch.Rows[index].Text || !rows[index].Uncertain || rows[index].Reason == "" {
				t.Fatal("unsafe patch not retained for review", rows, err)
			}
		})
	}
}

func TestCompactMixedIDBasisNeverAutomaticallyRenumbers(t *testing.T) {
	batch := CorrectionBatch{Rows: []documents.Segment{{Text: "线立体提供能量。"}, {Text: "请不要拍照片。"}}}
	// A correct processed receipt does not bind each patch to its source text.
	// Model relabeling 0 as 1 lands in-scope; guard must retain the target cue.
	raw := []byte(`{"processing":"complete","processed":[0,1],"patches":[{"id":1,"text":"线粒体提供能量。","uncertain":false,"reason":"术语纠正"}]}`)
	rows, err := ValidateCompactCorrectionWithBase(raw, batch, false)
	if err != nil || rows[0].Text != batch.Rows[0].Text || rows[1].Text != batch.Rows[1].Text || !rows[1].Uncertain {
		t.Fatal(rows, err)
	}
	if _, err := ValidateCompactCorrectionWithBase([]byte(`{"processing":"complete","processed":[1,2],"patches":[]}`), batch, false); err == nil {
		t.Fatal("one-based processed accepted by historical validator")
	}
	// Similar legitimate-looking rows are inherently ambiguous in historical
	// v2: no source binding exists. Do not pretend the heuristic proves ID intent.
	similar := CorrectionBatch{Rows: []documents.Segment{{Text: "这里线立体提供能量。"}, {Text: "那里线立体提供能量。"}}}
	if _, err := ValidateCompactCorrectionWithBase(raw, similar, false); err != nil {
		t.Fatal("structure cannot infer a different ID basis", err)
	}
}

func TestHistoricalCompactQuarantineDoesNotGuessSimilarCueIntent(t *testing.T) {
	source := "那里线立体提供能量。"
	rows := []CorrectedSegment{{Start: 1, End: 2, OriginalText: source, Text: "这里线粒体提供能量。", Reason: "术语纠正"}, {Start: 3, End: 4, OriginalText: "重复原句", Text: "重复原句"}}
	got := QuarantineCompactChanges(rows)
	if got[0].Text != source || !got[0].Uncertain || !strings.Contains(got[0].Reason, "原文绑定") || got[1] != rows[1] || rows[0].Text == source {
		t.Fatal(got, rows)
	}
}

func TestCorrectionFidelityPreservesValidEditsAndFlagsRisk(t *testing.T) {
	for _, item := range []struct {
		name, source, proposed string
		risky                  bool
	}{
		{"full_sentence_classifier", "前三篇主要对应的是牙体，后两片对应的是牙髓。", "前三篇主要对应的是牙体，后两篇对应的是牙髓。", false},
		{"unicode_term", "𠮷，线立体提供能量。", "𠮷，线粒体提供能量。", false},
		{"two_typoes", "Pythno 中的 compuetr 变量。", "Python 中的 computer 变量。", false},
		{"repeated_source", "线立体，线立体。", "线粒体，线粒体。", false},
		{"negation", "这个公式不考。", "这个公式考。", true},
		{"arabic_number", "剂量为10毫克。", "剂量为100毫克。", true},
		{"chinese_number", "前两篇对应牙体。", "前三篇对应牙体。", true},
		{"formula", "公式 y=x^2。", "公式 y=x+2。", true},
		{"conservative_term_false_positive", "非部检查结果明确。", "肺部检查结果明确。", true},
		{"large_expansion", "短原文。", strings.Repeat("扩写", 500000), true},
		{"bounded_diff", strings.Repeat("甲", 6000), strings.Repeat("乙", 6000), true},
		{"long_single_typo", strings.Repeat("甲", 2999) + "错" + strings.Repeat("乙", 3000), strings.Repeat("甲", 2999) + "正" + strings.Repeat("乙", 3000), false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if risky := CorrectionFidelityReason(item.source, item.proposed, nil) != ""; risky != item.risky {
				t.Fatalf("risk=%v expected=%v", risky, item.risky)
			}
		})
	}
	source := "前三篇主要对应的是牙体，后两片对应的是牙髓。"
	proposed := "前三篇主要对应的是牙体，后两篇对应的是牙髓。"
	if CorrectionFidelityReason(source, proposed, []documents.Segment{{Text: proposed}}) == "" {
		t.Fatal("near-identical known neighbor copy not flagged")
	}
	if CorrectionFidelityReason(source, source, []documents.Segment{{Text: source}}) != "" {
		t.Fatal("naturally repeated unchanged source flagged")
	}
}

func TestCorrectionLegacyAndCheckpointSafetyDoesNotDiscardProgress(t *testing.T) {
	source := []documents.Segment{{Start: 20, End: 27, Text: "前三篇主要对应的是牙体，后两片对应的是牙髓。"}}
	raw := []byte(`[{"id":7,"text":"后两篇","uncertain":false,"reason":"量词纠正"}]`)
	rows, err := ValidateCorrectionRows(raw, CorrectionBatch{Start: 7, Rows: source})
	if err != nil || rows[0].Text != source[0].Text || !rows[0].Uncertain {
		t.Fatal(rows, err)
	}
	checkpoint := &CorrectionCheckpoint{Fingerprint: "historical-paid-identity", Total: 1, Segments: []CorrectedSegment{{Start: 20, End: 27, OriginalText: source[0].Text, Text: "后两篇", Reason: "量词纠正"}}}
	resumed := ResumableCorrection(checkpoint, "historical-paid-identity", source)
	if len(resumed) != 1 || resumed[0].Text != source[0].Text || !resumed[0].Uncertain || resumed[0].Reason == "" {
		t.Fatal("checkpoint progress dropped", resumed)
	}
	if checkpoint.Segments[0].Text != "后两篇" {
		t.Fatal("historical input mutated")
	}
	// A partial checkpoint retains its saved progress too. It must not fall
	// through to a zero-length prefix and silently regenerate paid rows.
	checkpoint.Total = 2
	partialSource := append(append([]documents.Segment{}, source...), documents.Segment{Start: 30, End: 40, Text: "下一句"})
	partial := ResumableCorrection(checkpoint, "historical-paid-identity", partialSource)
	if len(partial) != 1 || partial[0].Text != source[0].Text || !partial[0].Uncertain {
		t.Fatal("partial progress lost", partial)
	}
}
