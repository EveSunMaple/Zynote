package zhiyun

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatchCoursePrioritizesPersonalCalendar(t *testing.T) { // NOSONAR: table-driven fixture test; extracting the server would obscure the scenario.
	const first = `{"id":"11","course_id":"987","sub_id":"511","title":"数据思维（甲）Ⅰ","term_name":"秋","lecturer_name":"甲老师"}`
	const second = `{"id":"12","sub_id":"512","title":"数据思维 (甲) I","lecturer_name":"乙老师"}`
	for _, tc := range []struct {
		name        string
		rows        string
		unavailable bool
		source      string
		matched     string
		count       int
	}{
		{name: "one teaching section has several replays", rows: first + "," + first, source: "personal-calendar", matched: "11", count: 1},
		{name: "two enrolled sections", rows: first + "," + second, source: "personal-calendar", count: 2},
		{name: "no exact personal title", rows: `{"id":"11","title":"数据思维（甲）II"}`, source: "catalog", matched: "99", count: 1},
		{name: "month error falls back", unavailable: true, source: "catalog", matched: "99", count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var searchCalls, monthCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case accountEndpoint:
					fmt.Fprint(w, `{"params":{"id":7,"tenant_id":112,"account":"student"}}`)
				case personalMonthEndpoint:
					monthCalls.Add(1)
					if r.URL.Query().Get("month") != monthInChina(time.Now()) || r.Header.Get("Authorization") != "Bearer fixture" {
						t.Error("bad calendar month or session")
					}
					if tc.unavailable {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					fmt.Fprintf(w, `{"code":0,"list":[{"section":"01","course":[%s]}]}`, tc.rows)
				case "/pptnote/v1/searchlist":
					searchCalls.Add(1)
					fmt.Fprint(w, `{"code":0,"list":[{"id":"99","title":"数据思维（甲）Ⅰ"}],"total":1}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL, Session{Authorization: "Bearer fixture"})
			for range 2 {
				match, err := client.MatchCourse(context.Background(), "数据思维 (甲) I")
				if err != nil || match.CalendarUnavailable != tc.unavailable || match.Source != tc.source ||
					match.MatchedID != tc.matched || len(match.Courses) != tc.count || (match.Status == "matched") != (tc.matched != "") {
					t.Fatalf("unexpected match: %+v %v", match, err)
				}
			}
			if monthCalls.Load() != 1 || (searchCalls.Load() == 0) != (tc.source == "personal-calendar") {
				t.Fatalf("unexpected requests: monthly %d, search %d", monthCalls.Load(), searchCalls.Load())
			}
		})
	}
}

func TestCalendarCacheSharesOnlyOneAccountTenantAndMonth(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprintf(w, `{"code":0,"list":[{"course":[{"id":"1","title":"%s"}]}]}`, r.Header.Get("Cookie"))
	}))
	defer server.Close()
	var cache CalendarCache
	lookup := func(user, tenant, month string) {
		client := New(server.URL, Session{Cookie: "account=" + user})
		rows, err := cache.courses(context.Background(), client, map[string]any{"id": user, "tenant_id": tenant}, month)
		if err != nil || len(rows) != 1 || rows[0].Title != "account="+user {
			t.Errorf("wrong account/month result: %+v %v", rows, err)
		}
	}
	var batch sync.WaitGroup
	for range 11 {
		batch.Go(func() { lookup("1", "112", "2026-9") })
	}
	batch.Wait()
	lookup("2", "112", "2026-9")
	lookup("2", "113", "2026-9")
	lookup("2", "113", "2026-10")
	if requests.Load() != 4 {
		t.Fatalf("expected four distinct calendar requests, got %d", requests.Load())
	}
	if monthInChina(time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)) != "2026-9" {
		t.Fatal("calendar month must use the Chinese timezone")
	}
}

func TestExplicitCatalogIncludesOtherSectionsAfterPersonalMatch(t *testing.T) {
	var accounts, months, searches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case accountEndpoint:
			accounts.Add(1)
			fmt.Fprint(w, `{"params":{"id":7,"tenant_id":112,"account":"student"}}`)
		case personalMonthEndpoint:
			months.Add(1)
			fmt.Fprint(w, `{"code":0,"list":[{"course":[{"id":"11","title":"数据思维"}]}]}`)
		case "/pptnote/v1/searchlist":
			searches.Add(1)
			fmt.Fprint(w, `{"code":0,"list":[{"id":"11","title":"数据思维","term_name":"秋"},{"id":"22","title":"数据思维","term_name":"春"}],"total":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	session := Session{Authorization: "Bearer fixture"}
	own, err := New(server.URL, session).MatchCourse(context.Background(), "数据思维")
	if err != nil || own.CatalogRequested || own.Source != "personal-calendar" || own.MatchedID != "11" {
		t.Fatalf("unexpected personal result: %+v, %v", own, err)
	}
	// A fresh cache ensures the explicit catalog request really bypasses the monthly API.
	catalog, err := New(server.URL, session).MatchCatalogCourse(context.Background(), "数据思维")
	if err != nil || !catalog.CatalogRequested || catalog.CalendarUnavailable || catalog.Source != "catalog" ||
		catalog.Status != "choose" || len(catalog.Courses) != 2 || catalog.Courses[1].ID != "22" || catalog.MatchedID != "" {
		t.Fatalf("unexpected explicit catalog: %+v, %v", catalog, err)
	}
	if accounts.Load() != 2 || months.Load() != 1 || searches.Load() != 1 {
		t.Fatalf("requests: accounts %d, calendars %d, catalogs %d", accounts.Load(), months.Load(), searches.Load())
	}
}
