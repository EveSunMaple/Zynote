package research

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type ExternalEvidence struct {
	Provider       string `json:"provider"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	Text           string `json:"text"`
	Scope          string `json:"scope"`
	ObtainedAt     string `json:"obtainedAt"`
	SHA256         string `json:"sha256"`
	Page           int    `json:"page,omitempty"`
	TotalPages     int    `json:"totalPages,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
	ResponseSHA256 string `json:"responseSha256,omitempty"`
}

type External struct {
	Client *http.Client
}

func NewExternal() *External {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("外部地址无效")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("外部地址无法解析")
			}
			for _, item := range ips {
				if !publicIP(item.IP) {
					return nil, fmt.Errorf("外部地址不受信任")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &External{Client: &http.Client{
		Timeout: 30 * time.Second, Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("外部跳转次数过多")
			}
			if _, err := publicHTTPS(request.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}}
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

func publicHTTPS(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("外部地址必须是 HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("外部地址不受信任")
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return nil, fmt.Errorf("外部地址不受信任")
	}
	return parsed, nil
}

func parseFulltextChoices(input any) []FulltextChoice {
	choices := []FulltextChoice{}
	rows, _ := input.([]any)
	for _, raw := range rows {
		value, _ := raw.(map[string]any)
		choice := FulltextChoice{URL: text(value["url"])}
		for _, page := range numberList(value["pages"]) {
			if page >= 1 && page <= 64 {
				choice.Pages = append(choice.Pages, page)
			}
		}
		if choice.URL != "" && len(choice.Pages) <= 4 {
			choices = append(choices, choice)
		}
		if len(choices) >= 4 {
			break
		}
	}
	return choices
}

type FulltextChoice struct {
	URL   string `json:"url"`
	Pages []int  `json:"pages,omitempty"`
}

func numberList(input any) []int {
	rows, _ := input.([]any)
	values := []int{}
	for _, row := range rows {
		values = append(values, int(number(row)))
	}
	return values
}

func (external *External) getResponse(ctx context.Context, target string) ([]byte, string, error) {
	parsed, err := publicHTTPS(target)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("外部请求失败")
	}
	request.Header.Set("Accept", "application/json, application/atom+xml, text/html;q=0.8")
	request.Header.Set("User-Agent", "Zynote-NativeResearch/1.0")
	client := external.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("外部请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode == 429 {
		return nil, "", fmt.Errorf("检索服务限流，请稍后手动重试")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("检索服务返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(body) > 2*1024*1024 {
		return nil, "", fmt.Errorf("外部响应过大")
	}
	return body, response.Header.Get("Content-Type"), nil
}

func (external *External) get(ctx context.Context, target string) ([]byte, error) {
	body, _, err := external.getResponse(ctx, target)
	return body, err
}

func (external *External) ReadPages(ctx context.Context, choices []FulltextChoice) ([]ExternalEvidence, error) {
	if len(choices) == 0 || len(choices) > 4 {
		return nil, fmt.Errorf("正文选择无效")
	}
	seen := map[string]bool{}
	rows := []ExternalEvidence{}
	for _, choice := range choices {
		if seen[choice.URL] {
			return nil, fmt.Errorf("正文网址重复，请合并所选页码")
		}
		seen[choice.URL] = true
		parsed, err := publicHTTPS(choice.URL)
		if err != nil {
			return nil, err
		}
		if len(choice.Pages) > 0 {
			return nil, fmt.Errorf("公开网页正文不设置PDF页码")
		}
		body, contentType, err := external.getResponse(ctx, parsed.String())
		if err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`^(?:text/html|application/xhtml\+xml|text/plain)(?:;|$)`).MatchString(contentType) {
			return nil, fmt.Errorf("所选网址不是公开文字网页")
		}
		title := regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`).FindStringSubmatch(string(body))
		name := parsed.Hostname()
		decoded := string(body)
		if title != nil {
			name = plainText(title[1])
		}
		text := decoded
		if strings.HasPrefix(contentType, "text/html") || strings.HasPrefix(contentType, "application/xhtml") {
			article := regexp.MustCompile(`(?is)<(?:article|main)\b[^>]*>(.*?)</(?:article|main)>`).FindStringSubmatch(decoded)
			if article != nil {
				text = article[1]
			}
			text = plainText(text)
		}
		text = strings.TrimSpace(text)
		if text == "" || regexp.MustCompile(`(?i)just a moment|verify you are human|captcha|访问验证|安全验证|请登录后查看`).MatchString(name+"\n"+trimText(text, 500)) {
			return nil, fmt.Errorf("网页未返回可核验正文，可能要求登录或验证")
		}
		truncated := len([]rune(text)) > 12000
		text = trimText(text, 12000)
		value := ExternalEvidence{
			Provider: "web", Title: name, URL: parsed.String(), Text: text, Scope: "web_excerpt",
			ObtainedAt: time.Now().UTC().Format(time.RFC3339Nano), Truncated: truncated,
			ResponseSHA256: hashBytes(body),
		}
		encoded, _ := json.Marshal(value)
		value.SHA256 = hashText(string(encoded))
		rows = append(rows, value)
	}
	return rows, nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func plainText(value string) string {
	value = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`).ReplaceAllString(value, " ")
	value = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`).ReplaceAllString(value, " ")
	value = regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(value, " ")
	value = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func (external *External) evidence(provider, title, link, text, scope string) (ExternalEvidence, error) {
	if _, err := publicHTTPS(link); err != nil {
		return ExternalEvidence{}, err
	}
	title = trimText(plainText(title), 300)
	text = trimText(plainText(text), 6000)
	if title == "" {
		return ExternalEvidence{}, fmt.Errorf("外部检索返回空标题")
	}
	value := ExternalEvidence{
		Provider: provider, Title: title, URL: link, Text: text, Scope: scope,
		ObtainedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, _ := json.Marshal(value)
	value.SHA256 = hashText(string(encoded))
	return value, nil
}

func (external *External) Search(ctx context.Context, channels []string, question string, web any) ([]ExternalEvidence, error) {
	query := trimText(strings.TrimSpace(question), 200)
	rows := []ExternalEvidence{}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch channel {
		case "crossref":
			values, err := url.Parse("https://api.crossref.org/works")
			if err != nil {
				continue
			}
			queryValues := values.Query()
			queryValues.Set("query", query)
			queryValues.Set("rows", "4")
			values.RawQuery = queryValues.Encode()
			body, err := external.get(ctx, values.String())
			if err != nil {
				return nil, err
			}
			var payload struct {
				Status  string `json:"status"`
				Message struct {
					Items []struct {
						DOI      string   `json:"DOI"`
						Title    []string `json:"title"`
						Abstract string   `json:"abstract"`
						Author   []struct {
							Given  string `json:"given"`
							Family string `json:"family"`
							Name   string `json:"name"`
						} `json:"author"`
					} `json:"items"`
				} `json:"message"`
			}
			if json.Unmarshal(body, &payload) != nil || payload.Status != "ok" || len(payload.Message.Items) == 0 {
				continue
			}
			for _, item := range payload.Message.Items {
				if len(item.Title) == 0 || !regexp.MustCompile(`^10\.[0-9]{4,9}/\S+$`).MatchString(item.DOI) {
					continue
				}
				authors := []string{}
				for index, author := range item.Author {
					if index >= 8 {
						break
					}
					name := author.Name
					if name == "" {
						name = strings.TrimSpace(author.Given + " " + author.Family)
					}
					authors = append(authors, name)
				}
				text := "作者：" + strings.Join(authors, ", ") + "\n" + item.Abstract
				if item.Abstract == "" {
					text = "作者：" + strings.Join(authors, ", ") + "\n仅取得题录，无可用摘要。"
				}
				scope := "metadata"
				if item.Abstract != "" {
					scope = "abstract"
				}
				row, err := external.evidence("crossref", item.Title[0], "https://doi.org/"+strings.ToLower(item.DOI), text, scope)
				if err == nil {
					rows = append(rows, row)
				}
			}
		case "semantic":
			values, _ := url.Parse("https://api.semanticscholar.org/graph/v1/paper/search")
			queryValues := values.Query()
			queryValues.Set("query", query)
			queryValues.Set("limit", "4")
			queryValues.Set("fields", "title,abstract,authors,year,externalIds")
			values.RawQuery = queryValues.Encode()
			body, err := external.get(ctx, values.String())
			if err != nil {
				return nil, err
			}
			var payload struct {
				Data []struct {
					PaperID  string `json:"paperId"`
					Title    string `json:"title"`
					Abstract string `json:"abstract"`
					Year     int    `json:"year"`
					Authors  []struct {
						Name string `json:"name"`
					} `json:"authors"`
				} `json:"data"`
			}
			if json.Unmarshal(body, &payload) != nil {
				continue
			}
			for _, item := range payload.Data {
				if len(item.PaperID) != 40 {
					continue
				}
				names := []string{}
				for index, author := range item.Authors {
					if index >= 8 {
						break
					}
					names = append(names, author.Name)
				}
				text := fmt.Sprintf("年份：%d 作者：%s\n%s", item.Year, strings.Join(names, ", "), item.Abstract)
				if item.Abstract == "" {
					text = fmt.Sprintf("年份：%d 作者：%s\n仅取得题录，无可用摘要。", item.Year, strings.Join(names, ", "))
				}
				scope := "metadata"
				if item.Abstract != "" {
					scope = "abstract"
				}
				row, err := external.evidence("semantic", item.Title, "https://www.semanticscholar.org/paper/"+item.PaperID, text, scope)
				if err == nil {
					rows = append(rows, row)
				}
			}
		case "arxiv":
			values, _ := url.Parse("https://export.arxiv.org/api/query")
			queryValues := values.Query()
			exact := regexp.MustCompile(`(?i)(?:arxiv\.org/(?:abs|pdf)/)?(\d{4}\.\d{4,5}(?:v\d+)?)\b`).FindStringSubmatch(query)
			if exact != nil {
				queryValues.Set("id_list", exact[1])
			} else {
				queryValues.Set("search_query", "all:"+query)
			}
			queryValues.Set("start", "0")
			queryValues.Set("max_results", "4")
			values.RawQuery = queryValues.Encode()
			body, err := external.get(ctx, values.String())
			if err != nil {
				return nil, err
			}
			entries := regexp.MustCompile(`(?s)<entry>(.*?)</entry>`).FindAllStringSubmatch(string(body), -1)
			for _, entry := range entries {
				id := regexp.MustCompile(`(?s)<id>\s*([^<]+)\s*</id>`).FindStringSubmatch(entry[1])
				title := regexp.MustCompile(`(?s)<title>\s*(.*?)\s*</title>`).FindStringSubmatch(entry[1])
				summary := regexp.MustCompile(`(?s)<summary>\s*(.*?)\s*</summary>`).FindStringSubmatch(entry[1])
				if id == nil || title == nil {
					continue
				}
				parsed, err := url.Parse(strings.TrimSpace(id[1]))
				if err != nil || (parsed.Hostname() != "arxiv.org" && parsed.Hostname() != "export.arxiv.org") {
					continue
				}
				parsed.Scheme = "https"
				parsed.Host = "arxiv.org"
				text := "无可用摘要"
				scope := "metadata"
				if summary != nil && strings.TrimSpace(summary[1]) != "" {
					text = strings.TrimSpace(summary[1])
					scope = "abstract"
				}
				row, err := external.evidence("arxiv", title[1], parsed.String(), text, scope)
				if err == nil {
					rows = append(rows, row)
				}
			}
		case "web":
			options, _ := web.(map[string]any)
			provider := text(options["provider"])
			if provider == "searxng" {
				endpoint := text(options["endpoint"])
				base, err := publicHTTPS(endpoint)
				if err != nil {
					return nil, err
				}
				if base.RawQuery != "" {
					return nil, fmt.Errorf("搜索实例地址不应带查询参数")
				}
				base.Path = strings.TrimSuffix(base.Path, "/") + "/search"
				queryValues := url.Values{"q": []string{query}, "format": []string{"json"}}
				base.RawQuery = queryValues.Encode()
				body, err := external.get(ctx, base.String())
				if err != nil {
					return nil, err
				}
				var payload struct {
					Results []struct {
						Title   string `json:"title"`
						URL     string `json:"url"`
						Content string `json:"content"`
					} `json:"results"`
				}
				if json.Unmarshal(body, &payload) != nil {
					continue
				}
				for _, item := range payload.Results {
					row, err := external.evidence("web", item.Title, item.URL, item.Content, "search_snippet")
					if err == nil {
						rows = append(rows, row)
					}
				}
			} else {
				values, _ := url.Parse("https://html.duckduckgo.com/html/")
				queryValues := url.Values{"q": []string{query}}
				values.RawQuery = queryValues.Encode()
				body, err := external.get(ctx, values.String())
				if err != nil {
					return nil, err
				}
				html := string(body)
				if regexp.MustCompile(`(?i)anomaly-modal|bots use DuckDuckGo|challenge-form`).MatchString(html) {
					return nil, fmt.Errorf("搜索服务要求人工验证，本次没有取得网页来源")
				}
				anchors := regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`).FindAllStringSubmatch(html, -1)
				matches := [][]string{}
				for _, anchor := range anchors {
					if !regexp.MustCompile(`(?i)class=["'][^"']*\bresult__a\b`).MatchString(anchor[1]) {
						continue
					}
					href := regexp.MustCompile(`(?i)href=["']([^"']+)["']`).FindStringSubmatch(anchor[1])
					if href == nil {
						continue
					}
					matches = append(matches, []string{anchor[0], href[1], anchor[2]})
					if len(matches) >= 4 {
						break
					}
				}
				for _, match := range matches {
					link := strings.ReplaceAll(match[1], "&amp;", "&")
					parsed, err := url.Parse(link)
					if err != nil {
						continue
					}
					target := parsed.String()
					if strings.HasSuffix(parsed.Hostname(), "duckduckgo.com") {
						if redirected := parsed.Query().Get("uddg"); redirected != "" {
							target = redirected
						}
					}
					row, err := external.evidence("web", match[2], target, "", "search_snippet")
					if err == nil {
						rows = append(rows, row)
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	filtered := []ExternalEvidence{}
	for _, row := range rows {
		if seen[row.URL] {
			continue
		}
		seen[row.URL] = true
		filtered = append(filtered, row)
	}
	if len(filtered) > 12 {
		filtered = filtered[:12]
	}
	return filtered, nil
}

func (external *External) Verify(rows []ExternalEvidence) error {
	for _, row := range rows {
		if _, err := publicHTTPS(row.URL); err != nil {
			return fmt.Errorf("外部来源快照校验失败")
		}
		copy := row
		copy.SHA256 = ""
		encoded, _ := json.Marshal(copy)
		if hashText(string(encoded)) != row.SHA256 {
			return fmt.Errorf("外部来源快照校验失败")
		}
	}
	return nil
}

func externalEvidenceText(source ExternalEvidence) string {
	return "读取范围：" + source.Scope + "；仅为 " + source.ObtainedAt + " 获取的快照，不表示已读全文。\n" + source.Text
}

func externalHash(value map[string]any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
