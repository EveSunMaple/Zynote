package learning

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

func TestHistoricalCompactIDsAndQuarantine(t *testing.T) {
	batch := CorrectionBatch{Start: 27, Rows: []documents.Segment{
		{Text: "前三篇主要对应的是牙体，后两片对应的是牙髓。"},
		{Text: "呃，你不要拍照片啊，这个现在这个手机都很好，高清照片，一个照片，这是个AM。"},
		{Text: "呃，然后转化成PDF文件提交到这个平台上面去。"},
	}}
	// Historical compact rows used zero-based IDs; a one-based patch can land
	// in scope and must be retained for review rather than silently applied.
	raw, _ := json.Marshal(map[string]any{
		"processing": "complete",
		"processed":  []int{27, 28, 29},
		"patches": []any{map[string]any{
			"id": 28, "text": batch.Rows[2].Text, "uncertain": false, "reason": "PDF 大小写",
		}},
	})
	rows, err := ValidateCompactCorrectionWithBase(raw, batch, false)
	if err != nil || rows[1].Text != batch.Rows[1].Text || !rows[1].Uncertain || rows[1].Reason == "PDF 大小写" {
		t.Fatalf("unsafe historical patch not retained: %+v %v", rows, err)
	}
	if _, err := ValidateCompactCorrection(raw, batch); err == nil {
		t.Fatal("one-based validator accepted zero-based processed receipt")
	}

	segments := []CorrectedSegment{
		{Start: 1, End: 2, OriginalText: "那里线立体提供能量。", Text: "这里线粒体提供能量。", Reason: "术语纠正"},
		{Start: 3, End: 4, OriginalText: "重复原句", Text: "重复原句"},
	}
	quarantined := QuarantineCompactChanges(segments)
	if quarantined[0].Text != "那里线立体提供能量。" || !quarantined[0].Uncertain ||
		!strings.Contains(quarantined[0].Reason, "原文绑定") || quarantined[1] != segments[1] ||
		segments[0].Text == "那里线立体提供能量。" {
		t.Fatalf("historical compact quarantine changed source: %+v %+v", quarantined, segments)
	}
}

func TestGuardCorrectionRowsRetainsDestructiveReplacement(t *testing.T) {
	source := "前三篇主要对应的是牙体，后两片对应的是牙髓。"
	batch := CorrectionBatch{Rows: []documents.Segment{{Text: source}}}
	rows := GuardCorrectionRows([]CorrectedRow{{Text: "后两篇"}}, batch, batch.Rows)
	if rows[0].Text != source || !rows[0].Uncertain || rows[0].Reason == "" {
		t.Fatalf("destructive replacement kept: %+v", rows)
	}
}
