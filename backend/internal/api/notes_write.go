package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

const msgNotesInvalid = "笔记数据无效"

var notesTimestamp = regexp.MustCompile(`^\d{2,}:\d{2}:\d{2}$`)
var notesNegative = regexp.MustCompile(`不考|了解即可|不作要求|跳过|不用看|不是重点|不重要|不要求`)
var notesPositive = regexp.MustCompile(`重点|要考|会考|可能考|很重要|一定要掌握|必考|去年考过|记一下|要会算|要求掌握|必须掌握`)
var notesLevels = map[string]bool{"必考": true, "很可能考": true, "重要": true, "要求掌握": true}

func normalizeNotesText(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsPunct(char) || unicode.IsSpace(char) || unicode.Is(unicode.Zs, char) || unicode.Is(unicode.Zl, char) || unicode.Is(unicode.Zp, char) {
			continue
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func notesTimestampMillis(value string) (int64, bool) {
	if !notesTimestamp.MatchString(value) {
		return 0, false
	}
	var hours, minutes, seconds int64
	if _, err := fmt.Sscanf(value, "%d:%d:%d", &hours, &minutes, &seconds); err != nil {
		return 0, false
	}
	return (hours*3600 + minutes*60 + seconds) * 1000, true
}

type notesSegment struct {
	start     float64
	end       float64
	text      string
	uncertain bool
}

func notesSegments(detail map[string]any) []notesSegment {
	raw, _ := detail["segments"].([]any)
	result := []notesSegment{}
	for _, item := range raw {
		segment, _ := item.(map[string]any)
		if segment == nil || truthy(segment["uncertain"]) {
			continue
		}
		result = append(result, notesSegment{
			start: float64(numberValue(segment["start"])), end: float64(numberValue(segment["end"])),
			text: textValue(segment["text"]), uncertain: false,
		})
	}
	return result
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	case json.Number:
		number, _ := typed.Float64()
		return number
	default:
		return 0
	}
}

func verifyNotesPoint(point map[string]any, group string, segments []notesSegment) {
	all := ""
	for _, segment := range segments {
		all += normalizeNotesText(segment.text)
	}
	quote := normalizeNotesText(textValue(point["quote"]))
	stamp, stampOK := notesTimestampMillis(textValue(point["timestamp"]))
	nearby := ""
	if stampOK {
		for _, segment := range segments {
			if absFloat(segment.start-float64(stamp)) < 20000 ||
				(segment.start <= float64(stamp) && segment.end >= float64(stamp)) {
				nearby += normalizeNotesText(segment.text)
			}
		}
	}
	negative := notesNegative.MatchString(quote)
	positive := notesPositive.MatchString(quote)
	trigger := false
	switch group {
	case "key":
		trigger = positive && !negative
	case "not":
		trigger = negative
	default:
		trigger = true
	}
	point["verified"] = stampOK && len([]rune(quote)) >= 3 && strings.Contains(all, quote) &&
		strings.Contains(nearby, quote) && trigger && !strings.HasPrefix(quote, "学生")
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func validateNotes(notes map[string]any) error {
	for _, group := range []string{"keyPoints", "notTested", "uncertain"} {
		items, ok := notes[group].([]any)
		if group == "uncertain" && notes[group] == nil {
			continue
		}
		if !ok {
			return contract.InputInvalid(msgNotesInvalid)
		}
		for _, item := range items {
			point, _ := item.(map[string]any)
			if point == nil || len([]rune(textValue(point["quote"]))) < 1 ||
				!notesTimestamp.MatchString(textValue(point["timestamp"])) {
				return contract.InputInvalid(msgNotesInvalid)
			}
			if group == "keyPoints" {
				level := textValue(point["level"])
				if !notesLevels[level] || textValue(point["topic"]) == "" || textValue(point["detail"]) == "" {
					return contract.InputInvalid(msgNotesInvalid)
				}
			}
		}
	}
	return nil
}

func notesMarkdown(lectureTitle string, notes map[string]any) string {
	out := []string{"# " + lectureTitle + " · 听课笔记", ""}
	groups := []struct {
		title string
		key   string
	}{
		{"教师明确重点", "keyPoints"}, {"明确不考", "notTested"}, {"表态含糊", "uncertain"},
	}
	for _, group := range groups {
		out = append(out, "## "+group.title, "")
		items, _ := notes[group.key].([]any)
		for _, item := range items {
			point, _ := item.(map[string]any)
			if point == nil {
				continue
			}
			heading := "### "
			if level, ok := point["level"].(string); ok && level != "" {
				heading += "[" + level + "] "
			}
			heading += textValue(point["topic"])
			status := textValue(point["timestamp"]) + " · "
			if truthy(point["verified"]) {
				status += "原话已核验"
			} else {
				status += "⚠ 疑似编造或时间不符"
			}
			out = append(out, heading, status, "", "> "+textValue(point["quote"]), "", textValue(point["detail"]))
			if ppt, ok := point["pptRef"].(string); ok && ppt != "" {
				out = append(out, "课件："+ppt)
			} else {
				out = append(out, "")
			}
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func (inv *Invoker) handleNotesSave(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID    string         `json:"id"`
		Notes map[string]any `json:"notes"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) || input.Notes == nil {
		return nil, contract.InputInvalid(msgNotesInvalid)
	}
	if err := validateNotes(input.Notes); err != nil {
		return nil, err
	}
	detailAny, err := inv.lectureDetailOf(state, input.ID)
	if err != nil {
		return nil, err
	}
	detail := detailAny.(map[string]any)
	segments := notesSegments(detail)
	for _, item := range input.Notes["keyPoints"].([]any) {
		point, _ := item.(map[string]any)
		verifyNotesPoint(point, "key", segments)
	}
	if items, ok := input.Notes["notTested"].([]any); ok {
		for _, item := range items {
			point, _ := item.(map[string]any)
			verifyNotesPoint(point, "not", segments)
		}
	}
	if items, ok := input.Notes["uncertain"].([]any); ok {
		for _, item := range items {
			point, _ := item.(map[string]any)
			verifyNotesPoint(point, "uncertain", segments)
		}
	} else {
		input.Notes["uncertain"] = []any{}
	}
	return inv.persistNotes(state, input.ID, input.Notes)
}

func (inv *Invoker) persistNotes(state *contract.State, lectureID string, notes map[string]any) (any, error) {
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	lecture, err := write.Lecture(lectureID)
	if err != nil {
		write.Close()
		return nil, err
	}
	if err := write.SaveNotes(lectureID, notes); err != nil {
		write.Close()
		return nil, err
	}
	write.Close()
	course, err := inv.courseName(state, textValue(lecture["course_id"]))
	if err != nil {
		return nil, err
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Notes", course)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, library.FolderName(textValue(lecture["title"]))+"-"+lectureID+"-notes.md")
	if err := os.WriteFile(path, []byte(notesMarkdown(textValue(lecture["title"]), notes)), 0o644); err != nil {
		return nil, contract.Unavailable("笔记保存失败")
	}
	return notes, nil
}

func (inv *Invoker) courseName(state *contract.State, courseID string) (string, error) {
	db, err := openReadOnlyDB(state)
	if err != nil {
		return "", err
	}
	defer db.Close()
	row, err := storage.QueryOne(db, "SELECT name FROM courses WHERE id=?", courseID)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	return textValue(row["name"]), nil
}
