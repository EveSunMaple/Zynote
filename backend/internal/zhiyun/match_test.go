package zhiyun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMatchCourse(t *testing.T) { // NOSONAR: table-driven fixture test; extracting the server would obscure the scenario.
	const first = `{"course_id":1,"title":"数据思维（甲）Ⅰ","lecturer_name":"甲老师","term_name":"秋"}`
	const similar = `{"course_id":2,"title":"数据思维（甲）II"}`
	const sameName = `{"course_id":3,"title":"数据思维 (甲) I","lecturer_name":"乙老师","term_name":"春"}`
	for _, tc := range []struct {
		name        string
		pages       []string
		total       int
		status      string
		matched     string
		count       int
		truncated   bool
		expectError bool
	}{
		{name: "unique across pages", pages: []string{similar, first + "," + similar}, total: 21, status: "matched", matched: "1", count: 2},
		{name: "two sections", pages: []string{first, sameName}, total: 21, status: "choose", count: 2},
		{name: "other level", pages: []string{similar}, total: 1, status: "choose", count: 1},
		{name: "none", pages: []string{""}, status: "not-found"},
		{name: "partial catalogue", pages: []string{first}, total: 500, status: "choose", count: 1, truncated: true},
		{name: "second page inaccessible", pages: []string{first, "error"}, total: 21, expectError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var accounts, pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/userapi/v1/infosimple":
					accounts.Add(1)
					fmt.Fprint(w, `{"params":{"id":7,"account":"student"}}`)
				case personalMonthEndpoint:
					fmt.Fprint(w, `{"code":0,"list":[]}`)
				case "/pptnote/v1/searchlist":
					page := int(pages.Add(1))
					query := r.URL.Query()
					if query.Get("title") != "数据思维" || query.Get("page") != strconv.Itoa(page) || r.Header.Get("Cookie") != "session=example" {
						t.Error("incorrect match query or session")
					}
					rows := tc.pages[(page-1)%len(tc.pages)]
					if rows == "error" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					fmt.Fprintf(w, `{"code":0,"total":{"list":[%s],"total":%d}}`, rows, tc.total)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			replay := &ReplayRef{CourseID: "1", SubID: "451"}
			client := New(server.URL, Session{Cookie: "session=example", CurrentReplay: replay})
			result, err := client.MatchCourse(context.Background(), "数据思维 (甲) I")
			if tc.expectError {
				if err == nil || !strings.Contains(err.Error(), "课程权限") || result.MatchedID != "" {
					t.Fatalf("partial results treated as complete: %+v, %v", result, err)
				}
				return
			}
			if err != nil || result.Source != "catalog" || result.CalendarUnavailable || result.Status != tc.status || result.MatchedID != tc.matched || result.Truncated != tc.truncated || len(result.Courses) != tc.count {
				t.Fatalf("unexpected match: %+v, %v", result, err)
			}
			if result.CurrentReplay != replay || accounts.Load() != 1 || tc.truncated && pages.Load() != 12 {
				t.Fatalf("unexpected replay or pagination: %+v, accounts %d, pages %d", result.CurrentReplay, accounts.Load(), pages.Load())
			}
			payload, err := json.Marshal(result)
			if err != nil || strings.Contains(string(payload), "session=example") {
				t.Fatal("match result leaked account data")
			}
		})
	}
}

func TestNormalizeCourseTitle(t *testing.T) {
	if NormalizeCourseTitle("大学英语　Ⅲ") != NormalizeCourseTitle("大学英语 III") ||
		NormalizeCourseTitle("数据思维（甲）Ⅰ") == NormalizeCourseTitle("数据思维（乙）I") ||
		NormalizeCourseTitle("数据思维（甲）Ⅰ") == NormalizeCourseTitle("数据思维（甲）II") {
		t.Fatal("unexpected title normalization")
	}
}

func TestMatchCourseWithoutSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a missing session must not query Zhiyun")
	}))
	defer server.Close()
	_, err := New(server.URL, Session{}).MatchCourse(context.Background(), "大学英语")
	if err == nil || !strings.Contains(err.Error(), "登录") {
		t.Fatalf("expected actionable login error: %v", err)
	}
}

func TestMatchCourseHandlesCatalogNoDataResponse(t *testing.T) {
	for _, tc := range []struct {
		reply string
		empty bool
	}{
		{reply: `{"code":-1,"msg":"没有数据"}`, empty: true},
		{reply: `{"code":-1,"msg":"请求失败"}`},
		{reply: `{"code":504,"msg":"没有数据"}`},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case accountEndpoint:
				fmt.Fprint(w, `{"params":{"id":7,"account":"student"}}`)
			case personalMonthEndpoint:
				fmt.Fprint(w, `{"code":0,"list":[]}`)
			case "/pptnote/v1/searchlist":
				fmt.Fprint(w, tc.reply)
			default:
				http.NotFound(w, r)
			}
		}))
		client := New(server.URL, Session{Cookie: "fixture"})
		result, err := client.MatchCourse(context.Background(), "学术写作")
		server.Close()
		if tc.empty {
			if err != nil || result.Status != "not-found" || result.Source != "catalog" || result.Truncated || len(result.Courses) != 0 {
				t.Fatalf("no-data response: %+v, %v", result, err)
			}
		} else if err == nil {
			t.Fatalf("treated another catalog error as empty: %+v", result)
		}
	}
}
