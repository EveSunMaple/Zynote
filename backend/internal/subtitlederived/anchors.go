package subtitlederived

import (
	"fmt"
	"math"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

type CueAnchor struct {
	SourceHash string `json:"sourceHash"`
	CueIndex   int    `json:"cueIndex"`
	StartMs    int64  `json:"startMs"`
	EndMs      int64  `json:"endMs"`
	Text       string `json:"text"`
}

// Rebuild anchors from verified source bytes; never trust prepared/model times.
func anchorPoints(srt string, points []Point) ([]Point, string, error) {
	segments, err := documents.ParseSrt(srt)
	if err != nil {
		return nil, "", err
	}
	hash := digest(srt)
	anchored := append([]Point(nil), points...)
	lines := []string{"\n\n### 字幕引用定位", "", "以下编号为此字幕快照内从 1 开始的顺序；时间单位为毫秒。", ""}
	for index := range anchored {
		point := &anchored[index]
		point.Anchor = nil
		if math.IsNaN(point.Cue) || math.IsInf(point.Cue, 0) || point.Cue != math.Trunc(point.Cue) || point.Cue < 0 || point.Cue >= float64(len(segments)) {
			return nil, "", fmt.Errorf("摘要引用字幕编号无效，已接收结果保留")
		}
		cue := int(point.Cue)
		segment := segments[cue]
		quote := learning.NormalizePracticeText(point.Quote)
		if quote == "" || !strings.Contains(learning.NormalizePracticeText(segment.Text), quote) {
			return nil, "", fmt.Errorf("摘要引文与字幕不一致，已接收结果保留")
		}
		point.Anchor = &CueAnchor{SourceHash: hash, CueIndex: cue, StartMs: segment.Start, EndMs: segment.End, Text: segment.Text}
		seconds := segment.Start / 1000
		point.Timestamp = fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds/60)%60, seconds%60)
		lines = append(lines, fmt.Sprintf("- 字幕 #%d · %d–%d ms · 原文：%s", cue+1, segment.Start, segment.End, strings.ReplaceAll(segment.Text, "\n", " ")))
	}
	return anchored, strings.Join(lines, "\n"), nil
}
