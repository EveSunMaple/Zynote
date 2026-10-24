package learning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const defaultTavilyEndpoint = "https://api.tavily.com/search"
const defaultBingEndpoint = "https://www.bing.com/search"

func searchEndpoint(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

type SearchHTTPError struct{ Status int }

func (value *SearchHTTPError) Error() string {
	return fmt.Sprintf("搜索服务失败 HTTP %d", value.Status)
}

type tavilyReply struct {
	Results []struct {
		URL        string `json:"url"`
		RawContent string `json:"raw_content"`
		Content    string `json:"content"`
	} `json:"results"`
}

func httpSource(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func TavilySearch(
	ctx context.Context,
	client *http.Client,
	key string,
	query string,
	depth string,
	maxResults int,
	timeout time.Duration,
) ([]PracticeSource, error) {
	if client == nil {
		client = http.DefaultClient
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"query": query, "max_results": maxResults, "include_raw_content": true, "search_depth": depth,
	})
	if err != nil {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, searchEndpoint("ZYNOTE_TAVILY_URL", defaultTavilyEndpoint), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &SearchHTTPError{Status: response.StatusCode}
	}
	var reply tavilyReply
	if err := json.NewDecoder(io.LimitReader(response.Body, 8*1024*1024)).Decode(&reply); err != nil {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	result := []PracticeSource{}
	for _, item := range reply.Results {
		if !httpSource(item.URL) {
			continue
		}
		text := item.RawContent
		if text == "" {
			text = item.Content
		}
		result = append(result, PracticeSource{URL: item.URL, Text: sliceUTF16(text, 15000)})
	}
	return result, nil
}

var bingItemPattern = regexp.MustCompile(`(?s)<item>([\s\S]*?)</item>`)
var bingLinkPattern = regexp.MustCompile(`(?s)<link>([\s\S]*?)</link>`)
var bingCDATA = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)
var bingTag = regexp.MustCompile(`(?s)<[^>]*>`)

func xmlText(value string) string {
	value = bingCDATA.ReplaceAllString(value, "$1")
	value = bingTag.ReplaceAllString(value, " ")
	for _, pair := range [][2]string{
		{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", `"`},
	} {
		value = strings.ReplaceAll(value, pair[0], pair[1])
	}
	return value
}

func BingSearch(ctx context.Context, client *http.Client, query string) ([]PracticeSource, error) {
	if client == nil {
		client = http.DefaultClient
	}
	requestContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext, http.MethodGet, searchEndpoint("ZYNOTE_BING_URL", defaultBingEndpoint)+"?format=rss&q="+strings.ReplaceAll(url.QueryEscape(query), "+", "%20"), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 150001))
	if err != nil || !regexp.MustCompile(`<rss\b`).Match(raw) {
		return nil, fmt.Errorf("答案搜索暂不可用")
	}
	items := bingItemPattern.FindAllStringSubmatch(string(raw), -1)
	if len(items) > 5 {
		items = items[:5]
	}
	result := []PracticeSource{}
	for _, item := range items {
		link := ""
		if match := bingLinkPattern.FindStringSubmatch(item[1]); match != nil {
			link = xmlText(match[1])
		}
		if !httpSource(link) {
			continue
		}
		result = append(result, PracticeSource{URL: link, Text: xmlText(item[1])})
	}
	return result, nil
}

func SearchAnswers(ctx context.Context, client *http.Client, query, key string) ([]PracticeSource, error) {
	if strings.TrimSpace(key) != "" {
		return TavilySearch(ctx, client, key, query, "basic", 5, 20*time.Second)
	}
	return BingSearch(ctx, client, query)
}
