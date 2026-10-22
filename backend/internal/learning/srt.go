package learning

import (
	"fmt"
	"math"
	"strings"
)

func srtTime(ms float64) string {
	rounded := int64(math.Max(0, math.Round(ms)))
	seconds := rounded / 1000
	millis := rounded % 1000
	return fmt.Sprintf(
		"%02d:%02d:%02d,%03d",
		seconds/3600,
		(seconds/60)%60,
		seconds%60,
		millis,
	)
}

func segmentOf(value map[string]any) (start, end float64, text string, ok bool) {
	start, startOK := value["start"].(float64)
	end, endOK := value["end"].(float64)
	text, textOK := value["text"].(string)
	return start, end, text, startOK && endOK && textOK
}

func ToSrtMaps(segments []map[string]any) string {
	lines := make([]string, 0, len(segments))
	for index, value := range segments {
		start, end, text, ok := segmentOf(value)
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d\n%s --> %s\n%s", index+1, srtTime(start), srtTime(end), text))
	}
	if len(lines) == 0 {
		return "\n"
	}
	return strings.Join(lines, "\n\n") + "\n"
}
