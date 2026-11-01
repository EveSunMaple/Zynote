package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// 摘要倒排索引还原时的最大词位，防止异常数据撑大内存。
const openAlexAbstractWords = 4000

var doiPattern = regexp.MustCompile(`^10\.[0-9]{4,9}/\S+$`)

// paperEvidenceText 拼接题录信息与摘要，并给出对应的读取范围。
func paperEvidenceText(meta, abstract string) (string, string) {
	abstract = strings.TrimSpace(abstract)
	if abstract == "" {
		return meta + "\n仅取得题录，无可用摘要。", "metadata"
	}
	return meta + "\n" + abstract, "abstract"
}

// externalURL 在固定接口地址上附加查询参数。
func externalURL(base string, params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return base + "?" + values.Encode()
}

// getJSON 读取外部 JSON；响应无法解析时视为空结果，与其他渠道一致。
func (external *External) getJSON(ctx context.Context, target string, payload any) (bool, error) {
	body, err := external.get(ctx, target)
	if err != nil {
		return false, err
	}
	return json.Unmarshal(body, payload) == nil, nil
}

type openAlexWork struct {
	ID              string `json:"id"`
	DOI             string `json:"doi"`
	DisplayName     string `json:"display_name"`
	PublicationYear int    `json:"publication_year"`
	Authorships     []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	PrimaryLocation *struct {
		LandingPageURL string `json:"landing_page_url"`
		Source         *struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
	} `json:"primary_location"`
	AbstractInvertedIndex map[string][]int `json:"abstract_inverted_index"`
}

// openAlexAbstract 把 OpenAlex 的摘要倒排索引（词 → 词位列表）还原成正文。
func openAlexAbstract(index map[string][]int) string {
	words := make([]string, 0)
	for word, positions := range index {
		for _, position := range positions {
			if position < 0 || position >= openAlexAbstractWords {
				continue
			}
			for len(words) <= position {
				words = append(words, "")
			}
			words[position] = word
		}
	}
	return strings.Join(strings.Fields(strings.Join(words, " ")), " ")
}

func (work openAlexWork) authors() string {
	names := []string{}
	for _, authorship := range work.Authorships {
		if len(names) >= 8 {
			break
		}
		if name := strings.TrimSpace(authorship.Author.DisplayName); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func (work openAlexWork) venue() string {
	if work.PrimaryLocation == nil || work.PrimaryLocation.Source == nil {
		return ""
	}
	return strings.TrimSpace(work.PrimaryLocation.Source.DisplayName)
}

// link 优先用 DOI，其次是出版方落地页，最后回退到 OpenAlex 条目页。
func (work openAlexWork) link() string {
	doi := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(work.DOI)), "https://doi.org/")
	if doiPattern.MatchString(doi) {
		return "https://doi.org/" + doi
	}
	if work.PrimaryLocation != nil {
		if _, err := publicHTTPS(work.PrimaryLocation.LandingPageURL); err == nil {
			return work.PrimaryLocation.LandingPageURL
		}
	}
	return work.ID
}

func (work openAlexWork) meta() string {
	meta := fmt.Sprintf("年份：%d 作者：%s", work.PublicationYear, work.authors())
	if venue := work.venue(); venue != "" {
		meta += " 来源：" + venue
	}
	return meta
}

func (external *External) searchOpenAlex(ctx context.Context, query string) ([]ExternalEvidence, error) {
	target := externalURL("https://api.openalex.org/works", map[string]string{
		"search": query, "per-page": "8",
		"select": "id,doi,display_name,publication_year,authorships,primary_location,abstract_inverted_index",
	})
	var payload struct {
		Results []openAlexWork `json:"results"`
	}
	if ok, err := external.getJSON(ctx, target, &payload); !ok {
		return nil, err
	}
	rows := []ExternalEvidence{}
	for _, work := range payload.Results {
		text, scope := paperEvidenceText(work.meta(), openAlexAbstract(work.AbstractInvertedIndex))
		if row, err := external.evidence("openalex", work.DisplayName, work.link(), text, scope); err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// wikipediaHost 按检索词语言选择中文或英文维基百科。
func wikipediaHost(query string) (string, bool) {
	for _, char := range query {
		if unicode.Is(unicode.Han, char) {
			return "zh.wikipedia.org", true
		}
	}
	return "en.wikipedia.org", false
}

type wikipediaPage struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	Extract string `json:"extract"`
	FullURL string `json:"fullurl"`
	Missing bool   `json:"missing"`
}

// wikipediaQuestionWords 是中文问句里的疑问词、虚词和标点。维基站内搜索要求词项都命中，
// 整句提问常匹配到无关条目，去掉这些词后只留主题词。长短语排在前面，优先整体替换。
var wikipediaQuestionWords = strings.NewReplacer(
	"请问", " ", "解释一下", " ", "介绍一下", " ", "什么是", " ", "是什么", " ", "有什么", " ",
	"有哪几种", " ", "有哪些", " ", "哪几种", " ", "哪些", " ", "哪种", " ", "为什么", " ", "为何", " ",
	"怎么样", " ", "怎么", " ", "怎样", " ", "如何", " ", "什么", " ", "一下", " ", "解释", " ", "介绍", " ",
	"请", " ", "吗", " ", "呢", " ", "吧", " ", "的", " ",
	"？", " ", "?", " ", "，", " ", ",", " ", "。", " ", "！", " ", "!", " ", "、", " ", "：", " ", "；", " ",
)

// wikipediaSearchTerms 把中文问句精简为主题词；精简后为空时沿用原词。
func wikipediaSearchTerms(query string, chinese bool) string {
	if !chinese {
		return query
	}
	terms := strings.Join(strings.Fields(wikipediaQuestionWords.Replace(query)), " ")
	if terms == "" {
		return query
	}
	return terms
}

// wikipediaURL 用 generator=search 一次取回检索结果与条目导语纯文本。
func wikipediaURL(query string) string {
	host, chinese := wikipediaHost(query)
	query = wikipediaSearchTerms(query, chinese)
	params := map[string]string{
		"action": "query", "format": "json", "formatversion": "2",
		"generator": "search", "gsrsearch": query, "gsrlimit": "5",
		"prop": "extracts|info", "inprop": "url",
		"exintro": "1", "explaintext": "1", "exlimit": "5",
	}
	if chinese {
		// 中文维基按简体输出导语。
		params["variant"] = "zh-cn"
	}
	return externalURL("https://"+host+"/w/api.php", params)
}

func (external *External) searchWikipedia(ctx context.Context, query string) ([]ExternalEvidence, error) {
	var payload struct {
		Query struct {
			Pages []wikipediaPage `json:"pages"`
		} `json:"query"`
	}
	if ok, err := external.getJSON(ctx, wikipediaURL(query), &payload); !ok {
		return nil, err
	}
	pages := payload.Query.Pages
	sort.SliceStable(pages, func(left, right int) bool { return pages[left].Index < pages[right].Index })
	rows := []ExternalEvidence{}
	for _, page := range pages {
		if page.Missing || strings.TrimSpace(page.Extract) == "" {
			continue
		}
		if row, err := external.evidence("wikipedia", page.Title, page.FullURL, page.Extract, "encyclopedia_intro"); err == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
