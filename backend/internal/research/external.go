package research

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const hostArxiv = "arxiv.org"

var errExternalRateLimited = errors.New("检索服务限流，请稍后手动重试")

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
	Client  *http.Client
	PDFPage func(path string, page int) (string, int, error)
}

// NewExternal 返回带防 SSRF 约束的外部检索客户端。
// 直连时在拨号前解析并校验目标地址；经环境变量配置的代理时，
// 只放行该代理地址本身，目标主机在发请求前单独解析校验。
func NewExternal() *External {
	return newExternal(http.ProxyFromEnvironment, net.DefaultResolver.LookupIPAddr, nil)
}

type lookupIPFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

type proxyFunc func(*http.Request) (*url.URL, error)

func newExternal(proxy proxyFunc, lookup lookupIPFunc, tlsConfig *tls.Config) *External {
	guard := &guardedTransport{proxy: proxy, lookup: lookup, proxies: map[string]bool{}}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	guard.direct = &http.Transport{
		TLSClientConfig: tlsConfig,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("外部地址无效：%w", err)
			}
			ips, err := guard.resolvePublic(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	guard.proxied = &http.Transport{
		TLSClientConfig: tlsConfig,
		Proxy:           proxy,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if !guard.proxyAllowed(address) {
				return nil, fmt.Errorf("代理地址不在允许范围")
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	return &External{Client: &http.Client{
		Timeout: 30 * time.Second, Transport: guard,
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

// guardedTransport 按环境代理配置把请求分给直连或代理两个 Transport。
// 代理 Transport 只能拨号到已登记的代理地址，直连 Transport 只能拨号到公网地址。
type guardedTransport struct {
	proxy   proxyFunc
	lookup  lookupIPFunc
	direct  *http.Transport
	proxied *http.Transport
	mu      sync.Mutex
	proxies map[string]bool
}

func (guard *guardedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	proxyURL, err := guard.proxy(request)
	if err != nil {
		return nil, fmt.Errorf("代理配置无效：%w", err)
	}
	if proxyURL == nil {
		return guard.direct.RoundTrip(request)
	}
	if _, err := publicHTTPS(request.URL.String()); err != nil {
		return nil, err
	}
	if _, err := guard.resolvePublic(request.Context(), request.URL.Hostname()); err != nil {
		return nil, err
	}
	guard.mu.Lock()
	guard.proxies[proxyAddress(proxyURL)] = true
	guard.mu.Unlock()
	return guard.proxied.RoundTrip(request)
}

func (guard *guardedTransport) proxyAllowed(address string) bool {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.proxies[address]
}

func (guard *guardedTransport) resolvePublic(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := guard.lookup(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("外部地址无法解析：%w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("外部地址无法解析")
	}
	for _, item := range ips {
		if !publicIP(item.IP) {
			return nil, fmt.Errorf("外部地址不受信任")
		}
	}
	return ips, nil
}

// proxyAddress 与 net/http 拨号代理时使用的 host:port 保持一致。
func proxyAddress(proxyURL *url.URL) string {
	port := proxyURL.Port()
	if port == "" {
		port = map[string]string{"https": "443", "socks5": "1080", "socks5h": "1080"}[proxyURL.Scheme]
	}
	if port == "" {
		port = "80"
	}
	return net.JoinHostPort(proxyURL.Hostname(), port)
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

// maxPDFPages 是论文 PDF 的页数上限；每次只读所选的至多 4 页，整篇页数只用于校验页码。
const maxPDFPages = 2000

func parseFulltextChoices(input any) []FulltextChoice {
	choices := []FulltextChoice{}
	rows, _ := input.([]any)
	for _, raw := range rows {
		value, _ := raw.(map[string]any)
		choice := FulltextChoice{URL: text(value["url"])}
		for _, page := range numberList(value["pages"]) {
			if page >= 1 && page <= maxPDFPages {
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

// 一般网页、检索接口的响应上限与总时限；论文 PDF 另有更大的上限和更长的时限。
const externalResponseLimit = 2 * 1024 * 1024

// 带大量插图的论文 PDF 可达数十 MB；只取指定的几页，但必须先完整下载。
const arxivPDFLimit = 50 * 1024 * 1024

// http.Client 的 Timeout 包括读完响应体；50 MB 在慢速网络下 30 秒读不完。
const arxivPDFTimeout = 3 * time.Minute

var errExternalTooLarge = errors.New("外部响应过大")

func (external *External) getResponse(ctx context.Context, target string) ([]byte, string, error) {
	return external.getResponseLimit(ctx, target, externalResponseLimit, 0)
}

// getResponseLimit 读取外部响应；timeout 为 0 时沿用客户端自身的总时限。
func (external *External) getResponseLimit(ctx context.Context, target string, limit int64, timeout time.Duration) ([]byte, string, error) {
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
	if timeout > 0 {
		// 复制客户端只改总时限，传输层（代理与地址校验）共用。
		extended := *client
		extended.Timeout = timeout
		client = &extended
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, "", fmt.Errorf("外部请求失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, "", errExternalRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("检索服务返回 %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("外部响应读取失败")
	}
	if int64(len(body)) > limit {
		return nil, "", errExternalTooLarge
	}
	return body, response.Header.Get("Content-Type"), nil
}

func (external *External) get(ctx context.Context, target string) ([]byte, error) {
	body, _, err := external.getResponse(ctx, target)
	return body, err
}

func arxivFulltextURL(parsed *url.URL) bool {
	return parsed.Hostname() == hostArxiv &&
		regexp.MustCompile(`^/(?:abs|pdf)/(?:\d{4}\.\d{4,5}|[a-z-]+/\d{7})v\d+$`).MatchString(parsed.Path)
}

func validateArxivChoice(channels []string, choice FulltextChoice, parsed *url.URL, pdfCount int) error {
	if !containsChannel(channels, "arxiv_fulltext") || !containsChannel(channels, "arxiv") {
		return fmt.Errorf("arXiv 正文需同时勾选 arXiv 及正文渠道")
	}
	if len(choice.Pages) == 0 || len(choice.Pages) > 4 || uniqueInts(choice.Pages) != len(choice.Pages) ||
		parsed.RawQuery != "" || pdfCount >= 2 {
		return fmt.Errorf("每次最多两篇固定版本论文，每篇明确选择至多四个物理页")
	}
	return nil
}

func downloadArxivPDF(ctx context.Context, external *External, parsed *url.URL) (string, string, []byte, error) {
	target := "https://arxiv.org/pdf/" + strings.TrimPrefix(strings.TrimPrefix(parsed.Path, "/abs/"), "/pdf/")
	body, _, err := external.getResponseLimit(ctx, target, arxivPDFLimit, arxivPDFTimeout)
	if err != nil {
		if errors.Is(err, errExternalTooLarge) {
			return "", "", nil, fmt.Errorf("论文正文超过大小上限（50 MB）")
		}
		return "", "", nil, err
	}
	if len(body) < 5 || string(body[:5]) != "%PDF-" {
		return "", "", nil, fmt.Errorf("论文接口未返回 PDF")
	}
	file, err := os.CreateTemp("", "zynote-arxiv-*.pdf")
	if err != nil {
		return "", "", nil, fmt.Errorf("论文正文暂存失败")
	}
	path := file.Name()
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", "", nil, fmt.Errorf("论文正文暂存失败")
	}
	return path, target, body, nil
}

func arxivEvidenceRows(external *External, path, target, name string, pages []int, responseSHA string) ([]ExternalEvidence, error) {
	rows := []ExternalEvidence{}
	for _, page := range pages {
		text, total, err := external.PDFPage(path, page)
		if err != nil {
			return nil, err
		}
		if total > maxPDFPages {
			return nil, fmt.Errorf("论文超过 %d 页，暂不支持", maxPDFPages)
		}
		if page > total {
			return nil, fmt.Errorf("所选第 %d 页不存在（全文共 %d 页）", page, total)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("所选论文页未取得文字，当前不会自动OCR或宣称读完")
		}
		truncated := len([]rune(text)) > 12000
		text = trimText(text, 12000)
		value := ExternalEvidence{
			Provider: "arxiv", Title: fmt.Sprintf("%s · 第%d页", name, page), URL: target,
			Text: text, Scope: "pdf_page", ObtainedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Page: page, TotalPages: total, Truncated: truncated, ResponseSHA256: responseSHA,
		}
		encoded, _ := json.Marshal(value)
		value.SHA256 = hashText(string(encoded))
		rows = append(rows, value)
	}
	return rows, nil
}

func readArxivFulltext(ctx context.Context, external *External, channels []string, choice FulltextChoice, parsed *url.URL, pdfCount *int) ([]ExternalEvidence, error) {
	if err := validateArxivChoice(channels, choice, parsed, *pdfCount); err != nil {
		return nil, err
	}
	if external.PDFPage == nil {
		return nil, fmt.Errorf("PDF 阅读组件不可用")
	}
	*pdfCount++
	path, target, body, err := downloadArxivPDF(ctx, external, parsed)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	name := parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
	return arxivEvidenceRows(external, path, target, name, choice.Pages, hashBytes(body))
}

func webArticleText(decoded, contentType string) string {
	text := decoded
	if strings.HasPrefix(contentType, "text/html") || strings.HasPrefix(contentType, "application/xhtml") {
		article := regexp.MustCompile(`(?is)<(?:article|main)\b[^>]*>(.*?)</(?:article|main)>`).FindStringSubmatch(decoded)
		if article != nil {
			text = article[1]
		}
		text = plainText(text)
	}
	return text
}

func readWebFulltext(ctx context.Context, external *External, channels []string, choice FulltextChoice, parsed *url.URL) (ExternalEvidence, error) {
	if !containsChannel(channels, "web_pages") || !containsChannel(channels, "web") || len(choice.Pages) > 0 {
		return ExternalEvidence{}, fmt.Errorf("公开网页正文需同时勾选网页搜索与正文渠道，且不设置PDF页码")
	}
	body, contentType, err := external.getResponse(ctx, parsed.String())
	if err != nil {
		return ExternalEvidence{}, err
	}
	if !regexp.MustCompile(`^(?:text/html|application/xhtml\+xml|text/plain)(?:;|$)`).MatchString(contentType) {
		return ExternalEvidence{}, fmt.Errorf("所选网址不是公开文字网页")
	}
	title := regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`).FindStringSubmatch(string(body))
	name := parsed.Hostname()
	if title != nil {
		name = plainText(title[1])
	}
	text := strings.TrimSpace(webArticleText(string(body), contentType))
	if text == "" || regexp.MustCompile(`(?i)just a moment|verify you are human|captcha|访问验证|安全验证|请登录后查看`).MatchString(name+"\n"+trimText(text, 500)) {
		return ExternalEvidence{}, fmt.Errorf("网页未返回可核验正文，可能要求登录或验证")
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
	return value, nil
}

func (external *External) ReadFulltext(ctx context.Context, channels []string, choices []FulltextChoice) ([]ExternalEvidence, error) {
	if len(choices) == 0 || len(choices) > 4 {
		return nil, fmt.Errorf("正文选择无效")
	}
	seen := map[string]bool{}
	rows := []ExternalEvidence{}
	pdfCount := 0
	for _, choice := range choices {
		if seen[choice.URL] {
			return nil, fmt.Errorf("正文网址重复，请合并所选页码")
		}
		seen[choice.URL] = true
		parsed, err := publicHTTPS(choice.URL)
		if err != nil {
			return nil, err
		}
		if arxivFulltextURL(parsed) {
			value, err := readArxivFulltext(ctx, external, channels, choice, parsed, &pdfCount)
			if err != nil {
				return nil, err
			}
			rows = append(rows, value...)
			continue
		}
		value, err := readWebFulltext(ctx, external, channels, choice, parsed)
		if err != nil {
			return nil, err
		}
		rows = append(rows, value)
	}
	return rows, nil
}

func containsChannel(channels []string, name string) bool {
	for _, channel := range channels {
		if channel == name {
			return true
		}
	}
	return false
}

func uniqueInts(values []int) int {
	seen := map[int]bool{}
	for _, value := range values {
		seen[value] = true
	}
	return len(seen)
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

type crossrefPayload struct {
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

func crossrefAuthorNames(authors []struct {
	Given  string `json:"given"`
	Family string `json:"family"`
	Name   string `json:"name"`
}) []string {
	names := []string{}
	for index, author := range authors {
		if index >= 8 {
			break
		}
		name := author.Name
		if name == "" {
			name = strings.TrimSpace(author.Given + " " + author.Family)
		}
		names = append(names, name)
	}
	return names
}

func (external *External) searchCrossref(ctx context.Context, query string) ([]ExternalEvidence, error) {
	values, err := url.Parse("https://api.crossref.org/works")
	if err != nil {
		return nil, nil
	}
	queryValues := values.Query()
	queryValues.Set("query", query)
	queryValues.Set("rows", "4")
	values.RawQuery = queryValues.Encode()
	body, err := external.get(ctx, values.String())
	if err != nil {
		return nil, err
	}
	var payload crossrefPayload
	if json.Unmarshal(body, &payload) != nil || payload.Status != "ok" || len(payload.Message.Items) == 0 {
		return nil, nil
	}
	rows := []ExternalEvidence{}
	for _, item := range payload.Message.Items {
		if len(item.Title) == 0 || !doiPattern.MatchString(item.DOI) {
			continue
		}
		text, scope := paperEvidenceText("作者："+strings.Join(crossrefAuthorNames(item.Author), ", "), item.Abstract)
		row, err := external.evidence("crossref", item.Title[0], "https://doi.org/"+strings.ToLower(item.DOI), text, scope)
		if err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (external *External) searchSemantic(ctx context.Context, query string) ([]ExternalEvidence, error) {
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
		return nil, nil
	}
	rows := []ExternalEvidence{}
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
		text, scope := paperEvidenceText(fmt.Sprintf("年份：%d 作者：%s", item.Year, strings.Join(names, ", ")), item.Abstract)
		row, err := external.evidence("semantic", item.Title, "https://www.semanticscholar.org/paper/"+item.PaperID, text, scope)
		if err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (external *External) arxivEntryEvidence(entry string) (ExternalEvidence, bool) {
	id := regexp.MustCompile(`(?s)<id>\s*([^<]+)\s*</id>`).FindStringSubmatch(entry)
	title := regexp.MustCompile(`(?s)<title>\s*(.*?)\s*</title>`).FindStringSubmatch(entry)
	summary := regexp.MustCompile(`(?s)<summary>\s*(.*?)\s*</summary>`).FindStringSubmatch(entry)
	if id == nil || title == nil {
		return ExternalEvidence{}, false
	}
	parsed, err := url.Parse(strings.TrimSpace(id[1]))
	if err != nil || (parsed.Hostname() != hostArxiv && parsed.Hostname() != "export.arxiv.org") {
		return ExternalEvidence{}, false
	}
	parsed.Scheme = "https"
	parsed.Host = hostArxiv
	text := "无可用摘要"
	scope := "metadata"
	if summary != nil && strings.TrimSpace(summary[1]) != "" {
		text = strings.TrimSpace(summary[1])
		scope = "abstract"
	}
	row, err := external.evidence("arxiv", title[1], parsed.String(), text, scope)
	if err != nil {
		return ExternalEvidence{}, false
	}
	return row, true
}

func (external *External) searchArxiv(ctx context.Context, query string) ([]ExternalEvidence, error) {
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
	rows := []ExternalEvidence{}
	for _, entry := range regexp.MustCompile(`(?s)<entry>(.*?)</entry>`).FindAllStringSubmatch(string(body), -1) {
		if row, ok := external.arxivEntryEvidence(entry[1]); ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (external *External) searchSearxng(ctx context.Context, query string, endpoint string) ([]ExternalEvidence, error) {
	base, err := publicHTTPS(endpoint)
	if err != nil {
		return nil, err
	}
	if base.RawQuery != "" {
		return nil, fmt.Errorf("搜索实例地址不应带查询参数")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/search"
	base.RawQuery = url.Values{"q": []string{query}, "format": []string{"json"}}.Encode()
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
		return nil, nil
	}
	rows := []ExternalEvidence{}
	for _, item := range payload.Results {
		row, err := external.evidence("web", item.Title, item.URL, item.Content, "search_snippet")
		if err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func duckDuckGoResults(html string) [][]string {
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
	return matches
}

func duckDuckGoTarget(link string) string {
	link = strings.ReplaceAll(link, "&amp;", "&")
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	target := parsed.String()
	if strings.HasSuffix(parsed.Hostname(), "duckduckgo.com") {
		if redirected := parsed.Query().Get("uddg"); redirected != "" {
			target = redirected
		}
	}
	return target
}

func (external *External) searchDuckDuckGo(ctx context.Context, query string) ([]ExternalEvidence, error) {
	values, _ := url.Parse("https://html.duckduckgo.com/html/")
	values.RawQuery = url.Values{"q": []string{query}}.Encode()
	body, err := external.get(ctx, values.String())
	if err != nil {
		return nil, err
	}
	html := string(body)
	if regexp.MustCompile(`(?i)anomaly-modal|bots use DuckDuckGo|challenge-form`).MatchString(html) {
		return nil, fmt.Errorf("搜索服务要求人工验证，本次没有取得网页来源")
	}
	rows := []ExternalEvidence{}
	for _, match := range duckDuckGoResults(html) {
		target := duckDuckGoTarget(match[1])
		if target == "" {
			continue
		}
		row, err := external.evidence("web", match[2], target, "", "search_snippet")
		if err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (external *External) searchWeb(ctx context.Context, query string, web any) ([]ExternalEvidence, error) {
	options, _ := web.(map[string]any)
	if text(options["provider"]) == "searxng" {
		return external.searchSearxng(ctx, query, text(options["endpoint"]))
	}
	return external.searchDuckDuckGo(ctx, query)
}

func dedupeExternalEvidence(rows []ExternalEvidence) []ExternalEvidence {
	seen := map[string]bool{}
	filtered := []ExternalEvidence{}
	for _, row := range rows {
		if seen[row.URL] {
			continue
		}
		seen[row.URL] = true
		filtered = append(filtered, row)
	}
	return interleaveByProvider(filtered, 12)
}

// interleaveByProvider 按渠道轮流取结果直到上限，避免排在前面的渠道占满名额、后面的渠道被整批截掉。
func interleaveByProvider(rows []ExternalEvidence, limit int) []ExternalEvidence {
	order := []string{}
	groups := map[string][]ExternalEvidence{}
	for _, row := range rows {
		if _, ok := groups[row.Provider]; !ok {
			order = append(order, row.Provider)
		}
		groups[row.Provider] = append(groups[row.Provider], row)
	}
	picked := []ExternalEvidence{}
	for round := 0; len(picked) < len(rows) && len(picked) < limit; round++ {
		for _, provider := range order {
			if round < len(groups[provider]) && len(picked) < limit {
				picked = append(picked, groups[provider][round])
			}
		}
	}
	return picked
}

// ExternalSkip 记录本次检索中被跳过的渠道及中文原因。
type ExternalSkip struct {
	Channel string `json:"channel"`
	Message string `json:"message"`
}

var externalChannelLabels = map[string]string{
	"crossref": "Crossref", "semantic": "Semantic Scholar", "arxiv": "arXiv", "web": "网页搜索",
	"openalex": "OpenAlex", "wikipedia": "维基百科",
}

func externalSkipFor(channel string, err error) ExternalSkip {
	label := externalChannelLabels[channel]
	if label == "" {
		label = channel
	}
	reason := err.Error()
	if errors.Is(err, errExternalRateLimited) {
		reason = "限流"
	}
	return ExternalSkip{Channel: channel, Message: label + " " + reason + "，已跳过"}
}

// externalSkipEvents 把逐渠道失败转成任务事件。
func externalSkipEvents(skips []ExternalSkip) []map[string]any {
	events := []map[string]any{}
	for _, skip := range skips {
		events = append(events, map[string]any{"kind": "external-skip", "channel": skip.Channel, "message": skip.Message})
	}
	return events
}

type externalChannelErrors []error

func (errs externalChannelErrors) Error() string {
	messages := []string{}
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return "所选外部渠道均检索失败：" + strings.Join(messages, "；")
}

func (errs externalChannelErrors) Unwrap() []error {
	return errs
}

func (external *External) searchChannel(ctx context.Context, channel, query string, web any) ([]ExternalEvidence, error) {
	switch channel {
	case "crossref":
		return external.searchCrossref(ctx, query)
	case "semantic":
		return external.searchSemantic(ctx, query)
	case "arxiv":
		return external.searchArxiv(ctx, query)
	case "openalex":
		return external.searchOpenAlex(ctx, query)
	case "wikipedia":
		return external.searchWikipedia(ctx, query)
	case "web":
		return external.searchWeb(ctx, query, web)
	}
	return nil, nil
}

// Search 逐渠道独立检索：单个渠道失败记为跳过，只有所有渠道都失败时才返回错误。
func (external *External) Search(ctx context.Context, channels []string, question string, web any) ([]ExternalEvidence, []ExternalSkip, error) {
	return external.SearchQueries(ctx, channels, nil, question, web)
}

// SearchQueries 按渠道使用各自的检索词（如学术渠道用英文关键词），未指定的渠道用 question。
func (external *External) SearchQueries(ctx context.Context, channels []string, queries map[string]string, question string, web any) ([]ExternalEvidence, []ExternalSkip, error) {
	rows := []ExternalEvidence{}
	skips := []ExternalSkip{}
	failures := externalChannelErrors{}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		query := queries[channel]
		if query == "" {
			query = question
		}
		values, err := external.searchChannel(ctx, channel, trimText(strings.TrimSpace(query), 200), web)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, nil, ctxErr
			}
			skip := externalSkipFor(channel, err)
			skips = append(skips, skip)
			failures = append(failures, fmt.Errorf("%s：%w", externalChannelLabels[channel], err))
			continue
		}
		rows = append(rows, values...)
	}
	if len(channels) > 0 && len(failures) == len(channels) {
		return nil, skips, failures
	}
	return dedupeExternalEvidence(rows), skips, nil
}

func (external *External) Verify(rows []ExternalEvidence) error {
	for _, row := range rows {
		if _, err := publicHTTPS(row.URL); err != nil {
			return fmt.Errorf("外部来源快照校验失败")
		}
		copied := row
		copied.SHA256 = ""
		encoded, _ := json.Marshal(copied)
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
