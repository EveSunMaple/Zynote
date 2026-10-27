package zhiyun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	Base     string
	Session  Session
	HTTP     *http.Client
	Progress func(string)
}

type Resource struct {
	URL      string
	Filename string
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

func buildTarget(base, path string, params map[string]string) string {
	target := base + path
	if len(params) == 0 {
		return target
	}
	pairs := []string{}
	for key, value := range params {
		pairs = append(pairs, urlEncode(key)+"="+urlEncode(value))
	}
	return target + "?" + strings.Join(pairs, "&")
}

func applySessionHeaders(request *http.Request, session Session) {
	if session.Cookie != "" {
		request.Header.Set("Cookie", session.Cookie)
	}
	if session.Authorization != "" {
		request.Header.Set("Authorization", session.Authorization)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0")
}

func waitRequestRetry(ctx context.Context, attempt int) error {
	if attempt >= 2 {
		return nil
	}
	timer := time.NewTimer(time.Duration(500*(attempt+1)) * time.Millisecond)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	}
}

func readZhiyunResponse(response *http.Response) (map[string]any, bool, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	response.Body.Close()
	if err != nil {
		return nil, true, nil
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, false, fmt.Errorf("请先登录智云课堂，再重试")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, false, fmt.Errorf("智云课堂暂时无法访问（%d）", response.StatusCode)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") ||
			strings.HasPrefix(strings.TrimSpace(string(bytes.TrimSpace(body))), "<") {
			return nil, false, fmt.Errorf("智云课堂返回了登录页，请先登录")
		}
		return nil, false, fmt.Errorf("智云课堂响应格式异常，请稍后重试")
	}
	code := int(numberValue(value["code"]))
	if code == 5002 || code == 401 || code == 403 {
		return nil, false, fmt.Errorf("请先登录智云课堂，再重试")
	}
	return value, false, nil
}

func (client *Client) request(ctx context.Context, path string, params map[string]string) (map[string]any, error) {
	target := buildTarget(client.Base, path, params)
	for attempt := 0; attempt <= 2; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("智云课堂地址无效")
		}
		applySessionHeaders(request, client.Session)
		response, err := client.HTTP.Do(request)
		if err != nil {
			if err := waitRequestRetry(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		value, retry, err := readZhiyunResponse(response)
		if retry {
			continue
		}
		if err != nil {
			return nil, err
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

func searchParams(account map[string]any, keyword string, page int) map[string]string {
	tenant := stringValue(account["tenant_id"])
	if tenant == "" {
		tenant = "112"
	}
	return map[string]string{
		"tenant_id": tenant, "tenant_code": tenant, "user_id": stringValue(account["id"]),
		"user_name": stringValue(account["account"]), "title": keyword, "realname": "", "trans": "",
		"page": strconv.Itoa(page), "per_page": "20",
	}
}

func searchRows(data map[string]any) (map[string]any, []any, error) {
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
	if list, ok := result["list"].([]any); ok {
		return result, list, nil
	}
	if result["list"] != nil {
		return nil, nil, fmt.Errorf("智云课堂搜索返回格式异常，请稍后重试")
	}
	return result, []any{}, nil
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func courseFromRow(row map[string]any) (Course, bool) {
	id := firstValue(stringValue(row["course_id"]), stringValue(row["id"]))
	if !digitsPattern.MatchString(id) || stringValue(row["c_type"]) == "recommend" {
		return Course{}, false
	}
	title := firstValue(stringValue(row["title"]), stringValue(row["course_title"]), "未命名课程")
	teacher := firstValue(stringValue(row["lecturer_name"]), stringValue(row["realname"]), stringValue(row["teacher"]))
	term := firstValue(stringValue(row["term_name"]), stringValue(row["term_names"]))
	return Course{ID: id, Title: title, Teacher: teacher, Term: term}, true
}

func searchCourses(rows []any) []Course {
	courses := []Course{}
	seen := map[string]bool{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		course, ok := courseFromRow(row)
		if !ok || seen[course.ID] {
			continue
		}
		seen[course.ID] = true
		courses = append(courses, course)
	}
	return courses
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
	data, err := client.request(ctx, "/pptnote/v1/searchlist", searchParams(account, keyword, page))
	if err != nil {
		return nil, err
	}
	if int(numberValue(data["code"])) != 0 {
		return nil, fmt.Errorf("搜索失败，请在智云课堂确认登录后重试")
	}
	result, rows, err := searchRows(data)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"courses": searchCourses(rows), "hasMore": float64(page*20) < numberValue(result["total"]),
	}, nil
}

func lessonID(row map[string]any) string {
	return firstValue(stringValue(row["sub_id"]), stringValue(row["id"]))
}

func lessonContent(row map[string]any) (map[string]any, error) {
	switch raw := row["content"].(type) {
	case string:
		content := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &content); err != nil {
			return nil, fmt.Errorf("智云课次播放信息损坏，请刷新课次列表后重试")
		}
		return content, nil
	case map[string]any:
		return raw, nil
	case nil:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("智云课次播放信息格式异常，请刷新课次列表后重试")
	}
}

func lessonPlaybackURL(content map[string]any) string {
	url := ""
	if playback, ok := content["save_playback"].(map[string]any); ok {
		url = stringValue(playback["contents"])
	}
	if url != "" {
		return url
	}
	if playback, ok := content["playback"].(map[string]any); ok {
		return stringValue(playback["url"])
	}
	return ""
}

func lessonFromRow(row map[string]any) (Lesson, bool, error) {
	id := lessonID(row)
	if !digitsPattern.MatchString(id) {
		return Lesson{}, false, nil
	}
	content, err := lessonContent(row)
	if err != nil {
		return Lesson{}, false, err
	}
	title := firstValue(stringValue(row["title"]), stringValue(row["sub_title"]), "未命名课次")
	return Lesson{
		ID: id, Title: title, Teacher: stringValue(row["lecturer_name"]),
		VideoAvailable: lessonPlaybackURL(content) != "",
	}, true, nil
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
		lesson, ok, err := lessonFromRow(row)
		if err != nil {
			return nil, err
		}
		if ok {
			lessons = append(lessons, lesson)
		}
	}
	return lessons, nil
}

func mediaURL(base, value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("智云课堂返回的资源地址不受支持")
	}
	if !parsed.IsAbs() {
		root, err := url.Parse(base)
		if err != nil {
			return "", fmt.Errorf("智云课堂返回的资源地址不受支持")
		}
		parsed = root.ResolveReference(parsed)
	}
	host := parsed.Hostname()
	if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		!(host == "cmc.zju.edu.cn" || strings.HasSuffix(host, ".cmc.zju.edu.cn")) ||
		parsed.User != nil {
		return "", fmt.Errorf("智云课堂返回的资源地址不受支持")
	}
	return parsed.String(), nil
}

func (client *Client) lessonVideoURLs(ctx context.Context, courseID string) (map[string]string, error) {
	data, err := client.request(ctx, "/courseapi/v2/course/catalogue", map[string]string{
		"course_id": courseID, "tenant_code": "112",
	})
	if err != nil {
		return nil, err
	}
	result, _ := data["result"].(map[string]any)
	rows, _ := result["data"].([]any)
	urls := map[string]string{}
	for _, item := range rows {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		id := lessonID(row)
		content, err := lessonContent(row)
		if err != nil {
			continue
		}
		url := lessonPlaybackURL(content)
		if id == "" || url == "" {
			continue
		}
		if checked, err := mediaURL(client.Base, url); err == nil {
			urls[id] = checked
		}
	}
	return urls, nil
}

func (client *Client) LessonVideoURL(ctx context.Context, courseID, subID string) (string, error) {
	urls, err := client.lessonVideoURLs(ctx, courseID)
	if err != nil {
		return "", err
	}
	return urls[subID], nil
}

func extensionAllowed(filename, kind string) bool {
	lower := strings.ToLower(filename)
	if kind == "ppt" {
		return strings.HasSuffix(lower, ".ppt") || strings.HasSuffix(lower, ".pptx")
	}
	return strings.HasSuffix(lower, ".docx") || strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".srt")
}

func findLesson(lessons []Lesson, subID string) *Lesson {
	for index := range lessons {
		if lessons[index].ID == subID {
			return &lessons[index]
		}
	}
	return nil
}

func resourceLabel(kind string) (label, exportType string) {
	if kind == "ppt" {
		return "PPT", "1"
	}
	return "语音识别原文", "2"
}

func resourceFilename(lesson *Lesson, kind string) string {
	filename := stringValue(lesson.Title)
	if kind == "ppt" {
		return filename + ".pptx"
	}
	return filename + ".docx"
}

func resourceFromStatus(data map[string]any, lesson *Lesson, kind, base string) (Resource, bool, error) {
	if int(numberValue(data["code"])) != 0 {
		return Resource{}, false, nil
	}
	result, _ := data["data"].(map[string]any)
	if result == nil || stringValue(result["path_name"]) == "" {
		return Resource{}, false, nil
	}
	filename := firstValue(stringValue(result["file_name"]), resourceFilename(lesson, kind))
	if !extensionAllowed(filename, kind) {
		return Resource{}, false, fmt.Errorf("官网返回的文件格式暂不支持")
	}
	checked, err := mediaURL(base, stringValue(result["path_name"]))
	if err != nil {
		return Resource{}, false, err
	}
	return Resource{URL: checked, Filename: filename}, true, nil
}

func (client *Client) startCoursewareExport(ctx context.Context, subID, exportType, label string) error {
	result, err := client.request(ctx, "/courseapi/v2/export/export-async", map[string]string{
		"sub_id": subID, "export_type": "courseware", "type": exportType,
	})
	if err != nil {
		return err
	}
	if int(numberValue(result["code"])) != 0 {
		return fmt.Errorf("智云课堂未能生成%s", label)
	}
	return nil
}

func waitResource(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client *Client) Resource(ctx context.Context, courseID, subID, kind string) (Resource, error) {
	lessons, err := client.Lessons(ctx, courseID)
	if err != nil {
		return Resource{}, err
	}
	lesson := findLesson(lessons, subID)
	if lesson == nil {
		return Resource{}, fmt.Errorf("没有找到所选课次，请重新选择")
	}
	label, exportType := resourceLabel(kind)
	created := false
	for attempt := 0; attempt < 60; attempt++ {
		if err := ctx.Err(); err != nil {
			return Resource{}, err
		}
		data, err := client.request(ctx, "/courseapi/v2/export/get-courseware-status", map[string]string{
			"type": exportType, "sub_id": subID, "get_source_url": "1",
		})
		if err != nil {
			return Resource{}, err
		}
		resource, found, err := resourceFromStatus(data, lesson, kind, client.Base)
		if err != nil {
			return Resource{}, err
		}
		if found {
			return resource, nil
		}
		code := int(numberValue(data["code"]))
		if code != 1 && code != 200 {
			return Resource{}, fmt.Errorf("本课次暂无可导出的%s", label)
		}
		if !created {
			if err := client.startCoursewareExport(ctx, subID, exportType, label); err != nil {
				return Resource{}, err
			}
			created = true
		}
		if client.Progress != nil {
			client.Progress(fmt.Sprintf("智云课堂正在生成%s…", label))
		}
		if err := waitResource(ctx); err != nil {
			return Resource{}, err
		}
	}
	return Resource{}, fmt.Errorf("%s生成超时，请稍后再试", label)
}

func openResourceDownload(ctx context.Context, client *Client, checked string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checked, nil)
	if err != nil {
		return nil, fmt.Errorf("资源下载失败")
	}
	applySessionHeaders(request, client.Session)
	downloader := &http.Client{Transport: client.HTTP.Transport}
	response, err := downloader.Do(request)
	if err != nil {
		return nil, fmt.Errorf("资源下载失败")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("资源下载失败（%d）", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/json") {
		response.Body.Close()
		return nil, fmt.Errorf("资源返回了登录页，请先登录智云课堂")
	}
	return response, nil
}

func writeResourceBody(response *http.Response, partial string, progress func(string)) error { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	file, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("资源下载失败")
	}
	total := response.ContentLength
	written := int64(0)
	buffer := make([]byte, 256*1024)
	last := time.Time{}
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			if _, writeErr := file.Write(buffer[:read]); writeErr != nil {
				file.Close()
				_ = os.Remove(partial)
				return fmt.Errorf("资源下载失败")
			}
			written += int64(read)
			if progress != nil && time.Since(last) > 500*time.Millisecond {
				last = time.Now()
				message := fmt.Sprintf("已下载 %.1f MB", float64(written)/1048576)
				if total > 0 {
					message += fmt.Sprintf(" / %.1f MB", float64(total)/1048576)
				}
				progress(message)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			file.Close()
			_ = os.Remove(partial)
			return fmt.Errorf("资源下载失败")
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("资源下载失败")
	}
	if written == 0 || (total > 0 && written != total) {
		_ = os.Remove(partial)
		return fmt.Errorf("下载文件不完整，请重试")
	}
	return nil
}

func (client *Client) Download(ctx context.Context, value, destination string) error {
	checked, err := mediaURL(client.Base, value)
	if err != nil {
		return err
	}
	partial := destination + ".part-" + strings.ReplaceAll(NewSessionID(), "-", "")
	response, err := openResourceDownload(ctx, client, checked)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := writeResourceBody(response, partial, client.Progress); err != nil {
		return err
	}
	if err := os.Rename(partial, destination); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("资源下载失败")
	}
	if client.Progress != nil {
		client.Progress("导出完成")
	}
	return nil
}

func NewSessionID() string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	value := hex.EncodeToString(sum[:16])
	return value[0:8] + "-" + value[8:12] + "-4" + value[13:16] + "-8" + value[17:20] + "-" + value[20:32]
}
