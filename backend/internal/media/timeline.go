package media

import (
	"fmt"
	"math"
	"sort"
)

type MediaInterval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func CheckedMediaIntervals(ranges []MediaInterval, duration float64) ([]MediaInterval, error) {
	if !finite(duration) || duration <= 0 || len(ranges) == 0 || len(ranges) > 201 {
		return nil, fmt.Errorf("媒体保留时间范围无效")
	}
	end := 0.0
	result := []MediaInterval{}
	for _, value := range ranges {
		if !finite(value.Start) || !finite(value.End) || value.Start < end ||
			value.End <= value.Start || value.End > duration+0.001 {
			return nil, fmt.Errorf("媒体保留时间范围越界或重叠")
		}
		end = value.End
		result = append(result, value)
	}
	return result, nil
}

type RemovalResult struct {
	Removed  []MediaInterval
	Kept     []MediaInterval
	Duration float64
}

func clippedRemovals(duration float64, removed []MediaInterval, window MediaInterval) ([]MediaInterval, error) {
	sorted := []MediaInterval{}
	for _, value := range removed {
		if _, err := CheckedMediaIntervals([]MediaInterval{value}, duration); err != nil {
			return nil, err
		}
		start := math.Max(window.Start, value.Start)
		end := math.Min(window.End, value.End)
		if end > start {
			sorted = append(sorted, MediaInterval{Start: start, End: end})
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	return sorted, nil
}

func mergeRemovals(sorted []MediaInterval) []MediaInterval {
	merged := []MediaInterval{}
	for _, value := range sorted {
		if len(merged) > 0 && value.Start <= merged[len(merged)-1].End {
			if value.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = value.End
			}
			continue
		}
		merged = append(merged, value)
	}
	return merged
}

func keptIntervals(window MediaInterval, merged []MediaInterval) []MediaInterval {
	kept := []MediaInterval{}
	cursor := window.Start
	for _, value := range merged {
		if value.Start > cursor {
			kept = append(kept, MediaInterval{Start: cursor, End: value.Start})
		}
		cursor = value.End
	}
	if cursor < window.End {
		kept = append(kept, MediaInterval{Start: cursor, End: window.End})
	}
	return kept
}

func RemovalTimeline(duration float64, removed []MediaInterval, clip *MediaInterval) (RemovalResult, error) {
	window := MediaInterval{Start: 0, End: duration}
	if clip != nil {
		window = *clip
	}
	if _, err := CheckedMediaIntervals([]MediaInterval{window}, duration); err != nil {
		return RemovalResult{}, err
	}
	sorted, err := clippedRemovals(duration, removed, window)
	if err != nil {
		return RemovalResult{}, err
	}
	merged := mergeRemovals(sorted)
	kept := keptIntervals(window, merged)
	if len(kept) == 0 {
		return RemovalResult{}, fmt.Errorf("所选片段移除后没有剩余内容，请调整分类或时间范围")
	}
	if _, err := CheckedMediaIntervals(kept, duration); err != nil {
		return RemovalResult{}, err
	}
	total := 0.0
	for _, value := range kept {
		total += value.End - value.Start
	}
	return RemovalResult{Removed: merged, Kept: kept, Duration: total}, nil
}

type MuxChapter struct {
	Start float64
	End   float64
	Title string
}

func RetimeChapters(chapters []MuxChapter, kept []MediaInterval) []MuxChapter {
	output := []MuxChapter{}
	offset := 0.0
	for _, interval := range kept {
		for _, chapter := range chapters {
			start := math.Max(interval.Start, chapter.Start)
			end := math.Min(interval.End, chapter.End)
			if end > start {
				output = append(output, MuxChapter{
					Start: offset + start - interval.Start,
					End:   offset + end - interval.Start,
					Title: chapter.Title,
				})
			}
		}
		offset += interval.End - interval.Start
	}
	sort.SliceStable(output, func(i, j int) bool { return output[i].Start < output[j].Start })
	return output
}
