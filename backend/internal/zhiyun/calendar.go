package zhiyun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"
)

const personalMonthEndpoint = "/courseapi/v2/course-live/get-my-course-month"

var chinaTime = time.FixedZone("Asia/Shanghai", 8*60*60)

func monthInChina(at time.Time) string {
	// The official calendar sends an unpadded month, for example "2026-9".
	return at.In(chinaTime).Format("2006-1")
}

type calendarSnapshot struct {
	key     [32]byte
	ready   chan struct{}
	expires time.Time
	courses []Course
	err     error
}

// CalendarCache keeps at most one short-lived, read-only account/month snapshot in memory.
type CalendarCache struct {
	mu      sync.Mutex
	current *calendarSnapshot
}

func (cache *CalendarCache) courses(ctx context.Context, client *Client, account map[string]any, month string) ([]Course, error) {
	key := sha256.Sum256([]byte(strings.Join([]string{
		client.Base, stringValue(account["tenant_id"]), stringValue(account["id"]), month,
	}, "\x00")))
	cache.mu.Lock()
	snapshot := cache.current
	if snapshot != nil && snapshot.key == key && (snapshot.expires.IsZero() || time.Now().Before(snapshot.expires)) {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-snapshot.ready:
			return snapshot.courses, snapshot.err
		}
	}
	snapshot = &calendarSnapshot{key: key, ready: make(chan struct{})}
	cache.current = snapshot
	cache.mu.Unlock()

	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	courses, err := client.personalMonth(lookupCtx, month)
	cancel()
	ttl := 2 * time.Minute
	if err != nil {
		ttl = 15 * time.Second // An unavailable calendar should not be retried once per screenshot row.
	}
	if ctx.Err() != nil {
		ttl = 0
	}
	cache.mu.Lock()
	snapshot.courses, snapshot.err, snapshot.expires = courses, err, time.Now().Add(ttl)
	close(snapshot.ready)
	cache.mu.Unlock()
	return courses, err
}

func monthCourse(item any) (Course, error) {
	row, _ := item.(map[string]any)
	// The monthly view opens a replay with course_id=row.id and sub_id=row.sub_id.
	id, title := stringValue(row["id"]), strings.TrimSpace(stringValue(row["title"]))
	if !digitsPattern.MatchString(id) || title == "" {
		return Course{}, fmt.Errorf("智云个人月历课程信息不完整")
	}
	return Course{
		ID: id, Title: title, Teacher: stringValue(row["lecturer_name"]), Term: stringValue(row["term_name"]),
	}, nil
}

func monthDayCourses(day any) ([]Course, error) {
	bucket, ok := day.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("智云个人月历日期格式异常")
	}
	if bucket["course"] == nil {
		return nil, nil
	}
	rows, ok := bucket["course"].([]any)
	if !ok {
		return nil, fmt.Errorf("智云个人月历课程列表格式异常")
	}
	courses := make([]Course, 0, len(rows))
	for _, item := range rows {
		course, err := monthCourse(item)
		if err != nil {
			return nil, err
		}
		courses = append(courses, course)
	}
	return courses, nil
}

func (client *Client) personalMonth(ctx context.Context, month string) ([]Course, error) {
	data, err := client.request(ctx, personalMonthEndpoint, map[string]string{"month": month})
	if err != nil {
		return nil, err
	}
	days, ok := data["list"].([]any)
	if !ok || stringValue(data["code"]) != "0" {
		return nil, fmt.Errorf("智云个人月历暂时不可用")
	}
	courses := []Course{}
	seen := map[[2]string]bool{}
	for _, day := range days {
		dayCourses, err := monthDayCourses(day)
		if err != nil {
			return nil, err
		}
		for _, course := range dayCourses {
			key := [2]string{course.ID, NormalizeCourseTitle(course.Title)}
			if seen[key] {
				continue
			}
			courses = append(courses, course)
			seen[key] = true
		}
	}
	return courses, nil
}
