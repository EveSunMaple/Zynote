package documents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Segment struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Text  string `json:"text"`
}

type timestampParts struct {
	Hours   int64
	Minutes int64
	Seconds int64
	Millis  int64
	Raw     string
}

var stampPattern = regexp.MustCompile(`^(\d{2,}):(\d{2}):(\d{2})(?:[,.](\d{3}))?$`)

func milliseconds(raw string) (int64, error) {
	match := stampPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return 0, fmt.Errorf("时间戳不合法：%s", raw)
	}
	minutes, _ := strconv.ParseInt(match[2], 10, 64)
	seconds, _ := strconv.ParseInt(match[3], 10, 64)
	if minutes > 59 || seconds > 59 {
		return 0, fmt.Errorf("时间戳不合法：%s", raw)
	}
	hours, _ := strconv.ParseInt(match[1], 10, 64)
	millis := int64(0)
	if match[4] != "" {
		millis, _ = strconv.ParseInt(match[4], 10, 64)
	}
	return (hours*3600+minutes*60+seconds)*1000 + millis, nil
}

var fencePattern = regexp.MustCompile("(?is)```(?:srt)?\\s*\\n([\\s\\S]*?)```")
var indexPattern = regexp.MustCompile(`^\d+$`)
var timeLinePattern = regexp.MustCompile(`^(\d{2,}:\d{2}:\d{2}[,.]\d{3})\s*-->\s*(\d{2,}:\d{2}:\d{2}[,.]\d{3})$`)

func ParseSrt(raw string) ([]Segment, error) {
	fenced := fencePattern.FindAllStringSubmatch(raw, -1)
	value := raw
	if len(fenced) > 0 {
		parts := make([]string, 0, len(fenced))
		for _, match := range fenced {
			parts = append(parts, match[1])
		}
		value = strings.Join(parts, "\n")
	}
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r", ""))
	if value == "" {
		return nil, fmt.Errorf("回复中没有字幕内容")
	}
	result := []Segment{}
	for _, block := range regexp.MustCompile(`\n\s*\n`).Split(value, -1) {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if !indexPattern.MatchString(lines[0]) {
			return nil, fmt.Errorf("SRT 字幕缺少序号")
		}
		timeLine := ""
		if len(lines) > 1 {
			timeLine = strings.TrimSpace(lines[1])
		}
		match := timeLinePattern.FindStringSubmatch(timeLine)
		if match == nil {
			return nil, fmt.Errorf("SRT 时间戳格式错误")
		}
		start, err := milliseconds(match[1])
		if err != nil {
			return nil, err
		}
		end, err := milliseconds(match[2])
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(strings.Join(lines[2:], "\n"))
		if text == "" || end <= start || (len(result) > 0 && start < result[len(result)-1].End) {
			return nil, fmt.Errorf("字幕为空或时间没有递增")
		}
		index, _ := strconv.Atoi(lines[0])
		if index != len(result)+1 {
			return nil, fmt.Errorf("字幕序号不连续")
		}
		result = append(result, Segment{Start: start, End: end, Text: text})
	}
	return result, nil
}
