package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStudyParseCatalogTimezoneAndRequiredEnd(t *testing.T) {
	const courseID = "11111111-1111-4111-8111-111111111111"
	const lectureID = "22222222-2222-4222-8222-222222222222"
	const otherID = "33333333-3333-4333-8333-333333333333"
	courses := []map[string]any{{"id": courseID, "lectures": []map[string]any{{"id": lectureID}}}, {"id": otherID, "lectures": []map[string]any{}}}
	for _, test := range []struct {
		name, course, lecture, end, zone string
		valid                            bool
	}{
		{"known", courseID, lectureID, "2026-10-23T10:00:00+08:00", "Asia/Shanghai", true},
		{"unknown-course", lectureID, "", "2026-10-23T10:00:00+08:00", "Asia/Shanghai", false},
		{"wrong-lecture", otherID, lectureID, "2026-10-23T10:00:00+08:00", "Asia/Shanghai", false},
		{"missing-end", courseID, "", "", "Asia/Shanghai", false},
		{"wrong-offset", courseID, "", "2026-10-23T10:00:00Z", "Asia/Shanghai", false},
		{"winter", courseID, "", "2026-01-20T10:00:00-05:00", "America/New_York", true},
		{"summer", courseID, "", "2026-07-20T10:00:00-04:00", "America/New_York", true},
		{"summer-wrong", courseID, "", "2026-07-20T10:00:00-05:00", "America/New_York", false},
		{"spring-gap", courseID, "", "2026-03-08T02:30:00-05:00", "America/New_York", false},
		{"fall-first", courseID, "", "2026-11-01T01:30:00-04:00", "America/New_York", true},
		{"fall-second", courseID, "", "2026-11-01T01:30:00-05:00", "America/New_York", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := map[string]any{"id": otherID, "courseId": test.course, "title": "线性代数", "chapter": "第一章", "endsAt": test.end}
			if test.lecture != "" {
				row["lectureId"] = test.lecture
			}
			body, _ := json.Marshal([]any{row})
			err := validStudyRowsFor(body, courses, test.zone)
			if (err == nil) != test.valid {
				t.Fatal(err)
			}
		})
	}
	row := map[string]any{"id": otherID, "courseId": courseID, "title": "手工条目", "chapter": "第一章", "endsAt": ""}
	if err := validateStudyEntry(row); err != nil {
		t.Fatal("manual empty end compatibility", err)
	}
}

func TestStudyParseTimezoneRejectedBeforeModelCall(t *testing.T) {
	for _, zone := range []string{"Local", "Mars/Olympus", "+08:00", ""} {
		raw, _ := json.Marshal(map[string]string{"text": "课表", "timezone": zone})
		if _, err := parseStudyInput(raw); err == nil {
			t.Fatal(zone)
		}
	}
	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		raw, _ := json.Marshal(map[string]string{"text": "课表", "timezone": zone})
		if _, err := parseStudyInput(raw); err != nil {
			t.Fatal(zone, err)
		}
	}
}

func TestStudyImageVisibleTimeWeekNotes(t *testing.T) {
	rows, err := decodeStudyImageRows([]byte(`[{"title":"代数","weekday":1,"startPeriod":1,"endPeriod":2,"timeText":" 08:00–09:40 ","weekText":" 1-16周（双周） ","endsAt":"invented"}]`), nil)
	if err != nil || len(rows) != 1 || rows[0]["timeText"] != "08:00–09:40" || rows[0]["weekText"] != "1-16周（双周）" || rows[0]["endsAt"] != "" {
		t.Fatal(rows, err)
	}
	for _, key := range []string{"timeText", "weekText"} {
		for _, bad := range []any{nil, 123, []string{"bad"}, strings.Repeat("周", 201)} {
			row := map[string]any{key: bad}
			if err := validateWeeklySlot(row); err == nil {
				t.Fatal(key, bad)
			}
		}
	}
	rows, err = decodeStudyImageRows([]byte(`[{"title":"课程","timeText":123,"weekText":null}]`), nil)
	if err != nil || rows[0]["timeText"] != nil || rows[0]["weekText"] != nil {
		t.Fatal(rows, err)
	}
	for _, key := range []string{"timeText", "weekText", "节次不是钟点", "不推断日历日期"} {
		if !strings.Contains(studyImagePrompt, key) {
			t.Fatal(key)
		}
	}
}
