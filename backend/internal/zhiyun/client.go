package zhiyun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultBase = "https://onlineroom.cmc.zju.edu.cn"

type Session struct {
	Cookie        string
	Authorization string
}

type Client struct {
	Base    string
	Session Session
	HTTP    *http.Client
}

func New(base string, session Session) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{
		Base: strings.TrimRight(base, "/"), Session: session,
		HTTP: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	}
}

var digitsPattern = regexp.MustCompile(`^\d+$`)

type Course struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Teacher string `json:"teacher"`
	Term    string `json:"term"`
}

type Lesson struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Teacher        string `json:"teacher"`
	VideoAvailable bool   `json:"videoAvailable"`
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatInt(int64(number), 10)
	}
	return ""
}

func numberValue(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func (client *Client) request(ctx context.Context, path string, params map[string]string) (map[string]any, error) {
	target := client.Base + path
	if len(params) > 0 {
		pairs := []string{}
		for key, value := range params {
			pairs = append(pairs, urlEncode(key)+"="+urlEncode(value))
		}
		target += "?" + strings.Join(pairs, "&")
	}
	for attempt := 0; attempt <= 2; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("智云课堂地址无效")
		}
		if client.Session.Cookie != "" {
			request.Header.Set("Cookie", client.Session.Cookie)
		}
		if client.Session.Authorization != "" {
			request.Header.Set("Authorization", client.Session.Authorization)
		}
		request.Header.Set("User-Agent", "Mozilla/5.0")
		response, err := client.HTTP.Do(request)
		if err != nil {
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(500*(attempt+1)) * time.Millisecond)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				}
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
		response.Body.Close()
		if err != nil {
			continue
		}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return nil, fmt.Errorf("请先登录智云课堂，再重试")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("智云课堂暂时无法访问（%d）", response.StatusCode)
		}
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") ||
				strings.HasPrefix(strings.TrimSpace(string(bytes.TrimSpace(body))), "<") {
				return nil, fmt.Errorf("智云课堂返回了登录页，请先登录")
			}
			return nil, fmt.Errorf("智云课堂响应格式异常，请稍后重试")
		}
		code := int(numberValue(value["code"]))
		if code == 5002 || code == 401 || code == 403 {
			return nil, fmt.Errorf("请先登录智云课堂，再重试")
		}
		return value, nil
	}
	return nil, fmt.Errorf("智云课堂连接失败，请稍后重试")
}

func urlEncode(value string) string {
	// Values here are digits, account names, titles and tenant codes.
	escaped := make([]string, 0, len(value))
	for _, char := range []byte(value) {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9',
			char == '-', char == '_', char == '.', char == '~':
			escaped = append(escaped, string(char))
		case char == ' ':
			escaped = append(escaped, "+")
		default:
			escaped = append(escaped, fmt.Sprintf("%%%02X", char))
		}
	}
	return strings.Join(escaped, "")
}

func (client *Client) Search(ctx context.Context, keyword string, page int) (map[string]any, error) {
	user, err := client.request(ctx, "/userapi/v1/infosimple", nil)
	if err != nil {
		return nil, err
	}
	account, _ := user["params"].(map[string]any)
	if stringValue(account["id"]) == "" || stringValue(account["account"]) == "" {
		return nil, fmt.Errorf("请先在左侧智云登录页面完成登录，再重试搜索")
	}
	tenant := stringValue(account["tenant_id"])
	if tenant == "" {
		tenant = "112"
	}
	data, err := client.request(ctx, "/pptnote/v1/searchlist", map[string]string{
		"tenant_id": tenant, "tenant_code": tenant, "user_id": stringValue(account["id"]),
		"user_name": stringValue(account["account"]), "title": keyword, "realname": "", "trans": "",
		"page": strconv.Itoa(page), "per_page": "20",
	})
	if err != nil {
		return nil, err
	}
	if int(numberValue(data["code"])) != 0 {
		return nil, fmt.Errorf("搜索失败，请在智云课堂确认登录后重试")
	}
	result := data
	if total, ok := data["total"].(map[string]any); ok {
		if _, ok := total["list"]; ok {
			result = total
		}
	}
	if value, ok := data["data"].(map[string]any); ok {
		if _, ok := value["list"]; ok {
			result = value
		}
	}
	rows := []any{}
	if list, ok := result["list"].([]any); ok {
		rows = list
	} else if result["list"] != nil {
		return nil, fmt.Errorf("智云课堂搜索返回格式异常，请稍后重试")
	}
	courses := []Course{}
	seen := map[string]bool{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		id := stringValue(row["course_id"])
		if id == "" {
			id = stringValue(row["id"])
		}
		if !digitsPattern.MatchString(id) || stringValue(row["c_type"]) == "recommend" || seen[id] {
			continue
		}
		seen[id] = true
		title := stringValue(row["title"])
		if title == "" {
			title = stringValue(row["course_title"])
		}
		if title == "" {
			title = "未命名课程"
		}
		teacher := stringValue(row["lecturer_name"])
		if teacher == "" {
			teacher = stringValue(row["realname"])
		}
		if teacher == "" {
			teacher = stringValue(row["teacher"])
		}
		term := stringValue(row["term_name"])
		if term == "" {
			term = stringValue(row["term_names"])
		}
		courses = append(courses, Course{ID: id, Title: title, Teacher: teacher, Term: term})
	}
	return map[string]any{
		"courses": courses, "hasMore": float64(page*20) < numberValue(result["total"]),
	}, nil
}

func (client *Client) Lessons(ctx context.Context, courseID string) ([]Lesson, error) {
	data, err := client.request(ctx, "/courseapi/v2/course/catalogue", map[string]string{
		"course_id": courseID, "tenant_code": "112",
	})
	if err != nil {
		return nil, err
	}
	result, _ := data["result"].(map[string]any)
	if data["success"] != true || int(numberValue(result["code"])) != 200 {
		return nil, fmt.Errorf("无法读取课程课次，请确认课程访问权限")
	}
	rows, ok := result["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("智云课次列表格式异常，请刷新后重试")
	}
	lessons := []Lesson{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		id := stringValue(row["sub_id"])
		if id == "" {
			id = stringValue(row["id"])
		}
		if !digitsPattern.MatchString(id) {
			continue
		}
		content := map[string]any{}
		if raw, ok := row["content"].(string); ok {
			if err := json.Unmarshal([]byte(raw), &content); err != nil {
				return nil, fmt.Errorf("智云课次播放信息损坏，请刷新课次列表后重试")
			}
		} else if value, ok := row["content"].(map[string]any); ok {
			content = value
		} else if row["content"] != nil {
			return nil, fmt.Errorf("智云课次播放信息格式异常，请刷新课次列表后重试")
		}
		url := ""
		if playback, ok := content["save_playback"].(map[string]any); ok {
			url = stringValue(playback["contents"])
		}
		if url == "" {
			if playback, ok := content["playback"].(map[string]any); ok {
				url = stringValue(playback["url"])
			}
		}
		title := stringValue(row["title"])
		if title == "" {
			title = stringValue(row["sub_title"])
		}
		if title == "" {
			title = "未命名课次"
		}
		lessons = append(lessons, Lesson{
			ID: id, Title: title, Teacher: stringValue(row["lecturer_name"]), VideoAvailable: url != "",
		})
	}
	return lessons, nil
}
