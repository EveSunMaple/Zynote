package zhiyun

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

type ReplayRef struct {
	CourseID string `json:"courseId"`
	SubID    string `json:"subId"`
}

type CourseMatch struct {
	Status              string     `json:"status"`
	Source              string     `json:"source"`
	CalendarUnavailable bool       `json:"calendarUnavailable,omitempty"`
	CatalogRequested    bool       `json:"catalogRequested,omitempty"`
	Courses             []Course   `json:"courses"`
	MatchedID           string     `json:"matchedId,omitempty"`
	Truncated           bool       `json:"truncated"`
	CurrentReplay       *ReplayRef `json:"currentReplay,omitempty"`
}

// NormalizeCourseTitle preserves course levels such as 甲/乙 and I/II, but tolerates OCR typography.
func NormalizeCourseTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(title)), ""))
}

var hanWords = regexp.MustCompile(`\p{Han}+`)

func courseSearchKeyword(title string) string {
	// Search by a common name fragment: a literal full-title query can omit alternate bracket or numeral spellings.
	var longest string
	for _, word := range hanWords.FindAllString(norm.NFKC.String(title), -1) {
		if utf8.RuneCountInString(word) > utf8.RuneCountInString(longest) {
			longest = word
		}
	}
	if utf8.RuneCountInString(longest) >= 2 {
		return longest
	}
	return strings.TrimSpace(norm.NFKC.String(title))
}

func (client *Client) MatchCourse(ctx context.Context, title string) (CourseMatch, error) {
	return client.matchCourse(ctx, title, false)
}

func (client *Client) MatchCatalogCourse(ctx context.Context, title string) (CourseMatch, error) {
	return client.matchCourse(ctx, title, true)
}

func (client *Client) personalCalendarCourses(ctx context.Context, account map[string]any, expected string) ([]Course, error) {
	personal, err := client.Calendar.courses(ctx, client, account, monthInChina(time.Now()))
	if err != nil {
		return nil, err
	}
	matches := []Course{}
	for _, course := range personal {
		if NormalizeCourseTitle(course.Title) == expected {
			matches = append(matches, course)
		}
	}
	return matches, nil
}

func (client *Client) matchCatalog(
	ctx context.Context,
	account map[string]any,
	title, expected string,
	result CourseMatch,
) (CourseMatch, error) {
	keyword := courseSearchKeyword(title)
	seen := make(map[string]bool)
	var exact []Course
	const maxPages = 12
	for page := 1; page <= maxPages; page++ {
		courses, more, err := client.searchPage(ctx, account, keyword, page)
		if err != nil {
			return CourseMatch{}, err
		}
		for _, course := range courses {
			if seen[course.ID] {
				continue
			}
			seen[course.ID] = true
			if NormalizeCourseTitle(course.Title) == expected {
				exact = append(exact, course)
			} else {
				result.Courses = append(result.Courses, course)
			}
		}
		result.Truncated = more
		if !more {
			break
		}
	}
	// Show exact titles first even when several teaching sections need a manual choice.
	if len(exact) > 0 {
		result.Courses = append(exact, result.Courses...)
	}
	// Only a complete search can distinguish a unique course from another section on a later page.
	if result.Truncated {
		return result, nil
	}
	if len(exact) == 1 {
		result.Status, result.MatchedID = "matched", exact[0].ID
	} else if len(result.Courses) == 0 {
		result.Status = "not-found"
	}
	return result, nil
}

func (client *Client) matchCourse(ctx context.Context, title string, catalogOnly bool) (CourseMatch, error) {
	result := CourseMatch{Courses: []Course{}, Status: "choose", Source: "catalog", CatalogRequested: catalogOnly}
	if client.Session.Cookie == "" && client.Session.Authorization == "" {
		return CourseMatch{}, fmt.Errorf("请先在设置中的智云课堂完成登录，再匹配课程")
	}
	account, err := client.account(ctx)
	if err != nil {
		return CourseMatch{}, err
	}
	if replay := client.Session.CurrentReplay; replay != nil && digitsPattern.MatchString(replay.CourseID) && digitsPattern.MatchString(replay.SubID) {
		result.CurrentReplay = replay
	}
	expected := NormalizeCourseTitle(title)
	if !catalogOnly {
		matched, err := client.personalCalendarCourses(ctx, account, expected)
		if err != nil && ctx.Err() != nil {
			return CourseMatch{}, ctx.Err()
		}
		if err != nil {
			result.CalendarUnavailable = true
		}
		if len(matched) > 0 {
			result.Courses, result.Source = matched, "personal-calendar"
			if len(matched) == 1 {
				result.Status, result.MatchedID = "matched", matched[0].ID
			}
			return result, nil
		}
	}
	return client.matchCatalog(ctx, account, title, expected, result)
}
