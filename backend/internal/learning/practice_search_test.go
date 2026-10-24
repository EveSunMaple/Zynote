package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTavilySearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization mismatch: %q", request.Header.Get("Authorization"))
		}
		var body struct {
			Query       string `json:"query"`
			SearchDepth string `json:"search_depth"`
			MaxResults  int    `json:"max_results"`
			RawContent  bool   `json:"include_raw_content"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Query != "题目" ||
			body.SearchDepth != "basic" || body.MaxResults != 5 || !body.RawContent {
			t.Errorf("body mismatch: %+v %v", body, err)
		}
		fmt.Fprint(writer, `{"results":[{"url":"https://example.com/a","raw_content":"来源正文"},{"url":"ftp://example.com/b","content":"忽略"}]}`)
	}))
	defer server.Close()
	t.Setenv("ZYNOTE_TAVILY_URL", server.URL)
	sources, err := SearchAnswers(context.Background(), nil, "题目", "test-key")
	if err != nil || len(sources) != 1 || sources[0].URL != "https://example.com/a" || sources[0].Text != "来源正文" {
		t.Fatalf("tavily mismatch: %+v %v", sources, err)
	}
}

func TestBingSearchFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.URL.RawQuery, "format=rss") {
			t.Errorf("query mismatch: %s", request.URL.RawQuery)
		}
		items := ""
		for index := 0; index < 6; index++ {
			items += fmt.Sprintf("<item><title>%d</title><link>https://example.com/%d</link><description>正文 %d</description></item>", index, index, index)
		}
		fmt.Fprintf(writer, `<rss><channel>%s</channel></rss>`, items)
	}))
	defer server.Close()
	t.Setenv("ZYNOTE_BING_URL", server.URL+"/search")
	sources, err := SearchAnswers(context.Background(), nil, "题目", "")
	if err != nil || len(sources) != 5 || sources[0].URL != "https://example.com/0" ||
		!strings.Contains(sources[4].Text, "正文 4") {
		t.Fatalf("bing mismatch: %+v %v", sources, err)
	}
}
