package learning

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

func segment(start, end int64, text string) documents.Segment {
	return documents.Segment{Start: start, End: end, Text: text}
}

func TestPlanCorrectionBatches(t *testing.T) {
	segments := []documents.Segment{
		segment(0, 1000, "第一句话"),
		segment(1000, 2000, "第二句话"),
		segment(2000, 3000, "第三句话"),
	}
	batches, err := PlanCorrection(segments, 0, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 || batches[0].Rows[0].Text != "第一句话" || batches[2].Start != 2 {
		t.Fatalf("batch planning mismatch: %+v", batches)
	}
	resumed, err := PlanCorrection(segments, 2, 8192)
	if err != nil || len(resumed) != 1 || resumed[0].Rows[0].Text != "第三句话" {
		t.Fatalf("resume planning mismatch: %+v %v", resumed, err)
	}
}

func TestPlanCorrectionRejectsOversizeRow(t *testing.T) {
	_, err := PlanCorrection([]documents.Segment{segment(0, 1, strings.Repeat("课", 6001))}, 0, 8192)
	if err == nil || !strings.Contains(err.Error(), "6000") {
		t.Fatalf("oversize mismatch: %v", err)
	}
}

func TestValidateCorrectionRows(t *testing.T) {
	batch := CorrectionBatch{Start: 2, Rows: []documents.Segment{
		segment(2000, 3000, "原文一"), segment(3000, 4000, "原文二"),
	}}
	rows, err := ValidateCorrectionRows(
		[]byte(`[{"id":2,"text":"校订一","uncertain":false,"reason":""},{"id":3,"text":"校订二","uncertain":true,"reason":"术语"}]`),
		batch,
	)
	if err != nil || len(rows) != 2 || rows[1].Uncertain != true || rows[1].Reason != "术语" {
		t.Fatalf("validation mismatch: %+v %v", rows, err)
	}
	for _, value := range []string{
		`[{"id":2,"text":"校订一","uncertain":false,"reason":""}]`,
		`[{"id":3,"text":"校订一","uncertain":false,"reason":""},{"id":2,"text":"校订二","uncertain":false,"reason":""}]`,
		`[{"id":2,"text":"   ","uncertain":false,"reason":""},{"id":3,"text":"校订二","uncertain":false,"reason":""}]`,
	} {
		if _, err := ValidateCorrectionRows([]byte(value), batch); err == nil {
			t.Fatalf("invalid rows accepted: %s", value)
		}
	}
}

func TestCorrectionContextSelectsRelevantPageAndImage(t *testing.T) {
	ppt := "[第 1 页]\n机器学习 特征工程 模型\n\n[第 2 页]\n数据清洗 缺失值"
	images := []CoursewareImage{
		{Label: "课件第 1 页嵌入图片 1", URL: "data:image/png;base64,aaa"},
		{Label: "课件第 2 页嵌入图片 1", URL: "data:image/png;base64,bbb"},
	}
	source := []documents.Segment{
		segment(0, 1000, "开场提示"),
		segment(1000, 2000, "数据清洗"),
	}
	batch := CorrectionBatch{Start: 1, Rows: source[1:]}
	context := CorrectionContextFor(ppt, images)(batch, source, CorrectionQuality)
	if !strings.Contains(context.Courseware, "数据清洗") || len(context.Images) != 1 ||
		context.Images[0].Label != "课件第 2 页嵌入图片 1" {
		t.Fatalf("context mismatch: %+v", context)
	}
	if len(context.Omissions) == 0 {
		t.Fatalf("limit omission missing: %+v", context)
	}
	textOnly := CorrectionContextFor(ppt, images)(batch, source, CorrectionText)
	if len(textOnly.Images) != 0 || !strings.Contains(strings.Join(textOnly.Omissions, "；"), "快速文字") {
		t.Fatalf("text mode mismatch: %+v", textOnly)
	}
}

func TestCorrectionContextFallsBackToProgress(t *testing.T) {
	ppt := "[第 1 页]\n苹果 香蕉\n\n[第 2 页]\n橘子 葡萄"
	source := []documents.Segment{segment(0, 1000, "完全无关的内容")}
	context := CorrectionContextFor(ppt, nil)(
		CorrectionBatch{Start: 0, Rows: source}, source, CorrectionQuality,
	)
	if !strings.Contains(context.Courseware, "苹果") {
		t.Fatalf("fallback page mismatch: %+v", context)
	}
	if !strings.Contains(strings.Join(context.Omissions, "；"), "未找到课件文字匹配") {
		t.Fatalf("fallback omission mismatch: %+v", context.Omissions)
	}
}

func TestResumableCorrectionPrefix(t *testing.T) {
	source := []documents.Segment{
		segment(0, 1000, "原文一"), segment(1000, 2000, "原文二"),
	}
	checkpoint := &CorrectionCheckpoint{
		Fingerprint: "fp", Total: 2,
		Segments: []CorrectedSegment{
			{Start: 0, End: 1000, Text: "校订一", OriginalText: "原文一", Uncertain: false, Reason: ""},
		},
	}
	if resumable := ResumableCorrection(checkpoint, "fp", source); len(resumable) != 1 {
		t.Fatalf("resumable mismatch: %+v", resumable)
	}
	if resumable := ResumableCorrection(checkpoint, "other", source); resumable != nil {
		t.Fatalf("fingerprint guard mismatch: %+v", resumable)
	}
	checkpoint.Total = 3
	if resumable := ResumableCorrection(checkpoint, "fp", source); resumable != nil {
		t.Fatalf("total guard mismatch: %+v", resumable)
	}
	checkpoint.Total = 2
	checkpoint.Segments[0].OriginalText = "篡改"
	if resumable := ResumableCorrection(checkpoint, "fp", source); resumable != nil {
		t.Fatalf("prefix guard mismatch: %+v", resumable)
	}
}

func TestParseCorrectedSegmentsRequiresFields(t *testing.T) {
	valid := `[{"start":0,"end":1000,"text":"校订","originalText":"原文","uncertain":false,"reason":""}]`
	segments, err := ParseCorrectedSegments(valid)
	if err != nil || len(segments) != 1 || segments[0].OriginalText != "原文" {
		t.Fatalf("parse mismatch: %+v %v", segments, err)
	}
	if _, err := ParseCorrectedSegments(`[{"start":0,"end":1000,"text":"校订"}]`); err == nil {
		t.Fatal("missing fields must fail")
	}
}

func TestRunCorrectionBatchesCommitsInOrder(t *testing.T) {
	batches := []CorrectionBatch{{Start: 0}, {Start: 1}, {Start: 2}, {Start: 3}}
	var committed []int
	var mutex sync.Mutex
	var running int64
	var peak int64
	err := RunCorrectionBatches(
		context.Background(), batches,
		func(ctx context.Context, batch CorrectionBatch, index int) (int, error) {
			current := atomic.AddInt64(&running, 1)
			for {
				seen := atomic.LoadInt64(&peak)
				if current <= seen || atomic.CompareAndSwapInt64(&peak, seen, current) {
					break
				}
			}
			if index%2 == 0 {
				time.Sleep(30 * time.Millisecond)
			} else {
				time.Sleep(5 * time.Millisecond)
			}
			atomic.AddInt64(&running, -1)
			return index, nil
		},
		func(value int, batch CorrectionBatch) error {
			mutex.Lock()
			committed = append(committed, value)
			mutex.Unlock()
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 4 {
		t.Fatalf("commit count mismatch: %v", committed)
	}
	for index, value := range committed {
		if value != index {
			t.Fatalf("commit order mismatch: %v", committed)
		}
	}
	if peak > 2 {
		t.Fatalf("concurrency exceeded: %d", peak)
	}
}

func TestRunCorrectionBatchesAbortsSiblings(t *testing.T) {
	batches := []CorrectionBatch{{Start: 0}, {Start: 1}}
	failure := errors.New("批量失败")
	err := RunCorrectionBatches(
		context.Background(), batches,
		func(ctx context.Context, batch CorrectionBatch, index int) (int, error) {
			if index == 1 {
				return 0, failure
			}
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(time.Second):
				return index, nil
			}
		},
		func(value int, batch CorrectionBatch) error { return nil },
	)
	if !errors.Is(err, failure) {
		t.Fatalf("failure mismatch: %v", err)
	}
}

func TestCorrectionContextParityWithTypeScript(t *testing.T) {
	segments := []documents.Segment{
		segment(1000, 2000, "第一句话 机器学习"),
		segment(2000, 3000, "第二句话 数据清洗"),
		segment(3000, 4000, "第三句话 特征工程"),
		segment(4000, 5000, "第四句话 模型训练"),
	}
	ppt := "[第 1 页]\n机器学习 特征工程 模型\n\n[第 2 页]\n数据清洗 缺失值\n\n[第 3 页]\n开场 提示"
	images := []CoursewareImage{
		{Label: "课件第 1 页嵌入图片 1", URL: "data:image/png;base64,aaa"},
		{Label: "课件第 2 页嵌入图片 1", URL: "data:image/png;base64,bbb"},
	}
	batches, err := PlanCorrection(segments, 0, 250)
	if err != nil || len(batches) != 4 {
		t.Fatalf("batches mismatch: %+v %v", batches, err)
	}
	contextFor := CorrectionContextFor(ppt, images)
	quality := contextFor(batches[0], segments, CorrectionQuality)
	if quality.Courseware != "[第 1 页]\n机器学习 特征工程 模型\n[第 2 页]\n数据清洗 缺失值" ||
		len(quality.Images) != 2 || len(quality.LectureContext) != 3 ||
		len(quality.Omissions) != 1 || !strings.Contains(quality.Omissions[0], "最多 8000 字") {
		t.Fatalf("quality context mismatch: %+v", quality)
	}
	balanced := contextFor(batches[1], segments, CorrectionBalanced)
	if len(balanced.Images) != 1 || balanced.Images[0].Label != "课件第 1 页嵌入图片 1" ||
		len(balanced.Omissions) != 2 || !strings.Contains(balanced.Omissions[0], "最多 4000 字") ||
		!strings.Contains(balanced.Omissions[1], "均衡模式") {
		t.Fatalf("balanced context mismatch: %+v", balanced)
	}
	ids := []int{}
	for _, row := range balanced.LectureContext {
		ids = append(ids, row.ID)
	}
	if len(ids) != 3 || ids[0] != 0 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("adjacent mismatch: %+v", balanced.LectureContext)
	}
}

func TestCorrectionFingerprintParityWithTypeScript(t *testing.T) {
	segments := []documents.Segment{
		segment(1000, 2000, "第一句"),
		segment(2000, 3000, "第二句"),
	}
	limitations := []string{"限制一", "限制二"}
	quality := CorrectionFingerprint(
		"conn-abc", segments, "校订提示词", []byte("ppt-bytes"), "课件文字", limitations, CorrectionQuality,
	)
	if quality != "e0f75bd7bf57c85c4010284698de9f537f624daa5fef880e3a5080419736ed0e" {
		t.Fatalf("quality fingerprint mismatch: %s", quality)
	}
	balanced := CorrectionFingerprint(
		"conn-abc", segments, "校订提示词", []byte("ppt-bytes"), "课件文字", limitations, CorrectionBalanced,
	)
	if balanced != "57c8cf55bbf227896fc161c496b3767b97d01088e7e765b7687b527aae7369f3" {
		t.Fatalf("balanced fingerprint mismatch: %s", balanced)
	}
}
