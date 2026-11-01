package research

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const openAlexSample = `{"results":[
 {"id":"https://openalex.org/W1","doi":"https://doi.org/10.1080/ABC.2024.1","display_name":"On the Remainder in Taylor's Theorem",
  "publication_year":2024,"authorships":[{"author":{"display_name":"Ann Lee"}},{"author":{"display_name":"Bo Chen"}}],
  "primary_location":{"landing_page_url":"https://example.org/paper","source":{"display_name":"College Math Journal"}},
  "abstract_inverted_index":{"remainder":[1],"The":[0],"is":[2],"bounded":[3],"the":[5],"by":[4],"derivative.":[6]}},
 {"id":"https://openalex.org/W2","doi":null,"display_name":"Lagrange form","publication_year":1990,"authorships":[],
  "primary_location":{"landing_page_url":"http://insecure.example.org/x","source":null},"abstract_inverted_index":null},
 {"id":"https://openalex.org/W3","display_name":"","publication_year":2000}
]}`

func TestOpenAlexAbstractRestoresWordOrder(t *testing.T) {
	got := openAlexAbstract(map[string][]int{"b": {1, 3}, "a": {0}, "c": {2}, "far": {openAlexAbstractWords}, "neg": {-1}})
	if got != "a b c b" {
		t.Fatalf("abstract mismatch: %q", got)
	}
	if openAlexAbstract(nil) != "" {
		t.Fatal("empty index must restore to empty text")
	}
}

func TestSearchOpenAlexParsesWorks(t *testing.T) {
	var query url.Values
	external := &External{Client: &http.Client{Transport: externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		query = request.URL.Query()
		return statusByHost(map[string]string{"api.openalex.org": openAlexSample}).Client.Transport.RoundTrip(request)
	})}}
	rows, err := external.searchOpenAlex(context.Background(), "Taylor remainder")
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("search") != "Taylor remainder" || query.Get("per-page") != "8" {
		t.Fatalf("request query mismatch: %v", query)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %+v", rows)
	}
	first, second := rows[0], rows[1]
	if first.Provider != "openalex" || first.URL != "https://doi.org/10.1080/abc.2024.1" || first.Scope != "abstract" {
		t.Fatalf("first row mismatch: %+v", first)
	}
	for _, part := range []string{"年份：2024", "Ann Lee, Bo Chen", "来源：College Math Journal", "The remainder is bounded by the derivative."} {
		if !strings.Contains(first.Text, part) {
			t.Fatalf("first text missing %q: %q", part, first.Text)
		}
	}
	if second.URL != "https://openalex.org/W2" || second.Scope != "metadata" || !strings.Contains(second.Text, "无可用摘要") {
		t.Fatalf("second row mismatch: %+v", second)
	}
	if err := external.Verify(rows); err != nil {
		t.Fatal(err)
	}
}

const wikipediaSample = `{"batchcomplete":true,"query":{"pages":[
 {"pageid":2,"index":2,"title":"泰勒级数","extract":"泰勒级数用无限项连加式表示函数。","fullurl":"https://zh.wikipedia.org/wiki/%E6%B3%B0%E5%8B%92%E7%BA%A7%E6%95%B0"},
 {"pageid":1,"index":1,"title":"泰勒公式","extract":"泰勒公式还给出了余项。","fullurl":"https://zh.wikipedia.org/wiki/%E6%B3%B0%E5%8B%92%E5%85%AC%E5%BC%8F"},
 {"pageid":3,"index":3,"title":"空条目","extract":"","fullurl":"https://zh.wikipedia.org/wiki/x"}
]}}`

func TestSearchWikipediaPicksLanguageAndOrder(t *testing.T) {
	var requested *url.URL
	external := &External{Client: &http.Client{Transport: externalRoundTrip(func(request *http.Request) (*http.Response, error) {
		requested = request.URL
		return statusByHost(map[string]string{"zh.wikipedia.org": wikipediaSample}).Client.Transport.RoundTrip(request)
	})}}
	rows, err := external.searchWikipedia(context.Background(), "泰勒公式 余项")
	if err != nil {
		t.Fatal(err)
	}
	if requested.Hostname() != "zh.wikipedia.org" || requested.Query().Get("variant") != "zh-cn" ||
		requested.Query().Get("gsrsearch") != "泰勒公式 余项" {
		t.Fatalf("request mismatch: %s", requested)
	}
	if len(rows) != 2 || rows[0].Title != "泰勒公式" || rows[1].Title != "泰勒级数" {
		t.Fatalf("rows mismatch: %+v", rows)
	}
	if rows[0].Provider != "wikipedia" || rows[0].Scope != "encyclopedia_intro" || rows[0].Text != "泰勒公式还给出了余项。" {
		t.Fatalf("row fields mismatch: %+v", rows[0])
	}
	if err := external.Verify(rows); err != nil {
		t.Fatal(err)
	}
}

func TestWikipediaSearchTermsStripChineseQuestionWords(t *testing.T) {
	cases := map[string]string{
		"泰勒公式的余项有哪几种形式？": "泰勒公式 余项 形式",
		"请解释傅里叶变换":       "傅里叶变换",
		"什么？":            "什么？",
	}
	for input, want := range cases {
		if got := wikipediaSearchTerms(input, true); got != want {
			t.Fatalf("terms for %q = %q, want %q", input, got, want)
		}
	}
	if got := wikipediaSearchTerms("What is Taylor's theorem?", false); got != "What is Taylor's theorem?" {
		t.Fatalf("english query must stay unchanged, got %q", got)
	}
}

func TestWikipediaURLUsesEnglishForLatinQuery(t *testing.T) {
	parsed, err := url.Parse(wikipediaURL("Taylor theorem remainder"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "en.wikipedia.org" || parsed.Query().Has("variant") {
		t.Fatalf("english url mismatch: %s", parsed)
	}
}

func TestReferenceChannelsEmptyResults(t *testing.T) {
	external := statusByHost(map[string]string{
		"api.openalex.org": `{"results":[]}`,
		"en.wikipedia.org": `{"batchcomplete":true}`,
	})
	rows, skips, err := external.Search(context.Background(), []string{"openalex", "wikipedia"}, "no such topic", nil)
	if err != nil || len(rows) != 0 || len(skips) != 0 {
		t.Fatalf("empty results mismatch: rows=%+v skips=%+v err=%v", rows, skips, err)
	}
}

func TestReferenceChannelsHTTPErrorSkipped(t *testing.T) {
	external := statusByHost(map[string]string{
		"api.openalex.org": "500",
		"zh.wikipedia.org": "429",
		"api.crossref.org": `{"status":"ok","message":{"items":[{"DOI":"10.1000/xyz123","title":["Taylor remainder"]}]}}`,
	})
	rows, skips, err := external.Search(context.Background(), []string{"openalex", "wikipedia", "crossref"}, "泰勒公式", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(skips) != 2 {
		t.Fatalf("partial search mismatch: rows=%+v skips=%+v", rows, skips)
	}
	if skips[0].Message != "OpenAlex 检索服务返回 500，已跳过" || skips[1].Message != "维基百科 限流，已跳过" {
		t.Fatalf("skip messages mismatch: %+v", skips)
	}
}

func TestDedupeInterleavesProviders(t *testing.T) {
	rows := []ExternalEvidence{}
	for index := 0; index < 10; index++ {
		rows = append(rows, ExternalEvidence{Provider: "openalex", URL: "https://a.example/" + string(rune('a'+index))})
	}
	rows = append(rows, ExternalEvidence{Provider: "wikipedia", URL: "https://w.example/1"}, ExternalEvidence{Provider: "wikipedia", URL: "https://w.example/1"},
		ExternalEvidence{Provider: "web", URL: "https://b.example/1"}, ExternalEvidence{Provider: "web", URL: "https://b.example/2"},
		ExternalEvidence{Provider: "web", URL: "https://b.example/3"})
	got := dedupeExternalEvidence(rows)
	counts := map[string]int{}
	for _, row := range got {
		counts[row.Provider]++
	}
	if len(got) != 12 || counts["wikipedia"] != 1 || counts["web"] != 3 || counts["openalex"] != 8 {
		t.Fatalf("interleave mismatch: %v", counts)
	}
	if got[0].Provider != "openalex" || got[1].Provider != "wikipedia" || got[2].Provider != "web" {
		t.Fatalf("round-robin order mismatch: %+v", got[:3])
	}
}
