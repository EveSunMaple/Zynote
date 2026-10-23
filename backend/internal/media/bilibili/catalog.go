package bilibili

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

var videoPathPattern = regexp.MustCompile(`^/video/(BV[\w]+|av\d+)/?$`)
var pageNumberPattern = regexp.MustCompile(`^[1-9]\d*$`)
var mediaHostPattern = regexp.MustCompile(`(^|\.)bilivideo\.com$`)
var mediaAddressHostPattern = regexp.MustCompile(`(^|\.)(bilivideo\.com|bilivideo\.cn|hdslb\.com)$`)

func VideoURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", contract.InputInvalid("请输入完整的 B 站视频网址，例如 https://www.bilibili.com/video/BV…/?p=5")
	}
	host := parsed.Hostname()
	if parsed.Scheme != "https" || (host != "www.bilibili.com" && host != "bilibili.com") ||
		!videoPathPattern.MatchString(parsed.Path) || parsed.User != nil {
		return "", contract.InputInvalid("请输入完整的 B 站视频网址，例如 https://www.bilibili.com/video/BV…/?p=5")
	}
	if page := parsed.Query().Get("p"); page != "" && !pageNumberPattern.MatchString(page) {
		return "", contract.InputInvalid("视频分 P 编号无效")
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func VideoMediaURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.User == nil &&
		(parsed.Port() == "" || parsed.Port() == "443") &&
		mediaHostPattern.MatchString(parsed.Hostname()) &&
		strings.HasSuffix(strings.ToLower(parsed.Path), ".mp4")
}

func MediaAddress(value string) (string, error) {
	target := value
	if strings.HasPrefix(target, "//") {
		target = "https:" + target
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		(parsed.Port() != "" && parsed.Port() != "443") ||
		!mediaAddressHostPattern.MatchString(parsed.Hostname()) {
		return "", contract.InputInvalid("B 站媒体地址不受信任")
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

type MediaStream struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	URLs      []string `json:"urls"`
	Height    int      `json:"height"`
	Codec     string   `json:"codec"`
	Bandwidth float64  `json:"bandwidth"`
	Extension string   `json:"extension"`
	Transport string   `json:"transport,omitempty"`
}

type SubtitleTrack struct {
	Language  string `json:"language"`
	URL       string `json:"url"`
	Automatic bool   `json:"automatic"`
}

type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type Catalog struct {
	URL       string              `json:"url"`
	Title     string              `json:"title"`
	Playlist  bool                `json:"playlist"`
	Entries   []map[string]string `json:"entries,omitempty"`
	Limited   bool                `json:"limited,omitempty"`
	Duration  *float64            `json:"duration,omitempty"`
	Heights   []int               `json:"heights,omitempty"`
	Subtitles []string            `json:"subtitles,omitempty"`
	Formats   []map[string]any    `json:"formats,omitempty"`
}

type Source struct {
	Catalog         Catalog           `json:"catalog"`
	Identity        string            `json:"identity"`
	Streams         []MediaStream     `json:"streams"`
	SourceID        string            `json:"sourceId,omitempty"`
	Platform        string            `json:"platform,omitempty"`
	DownloadHeaders map[string]string `json:"downloadHeaders,omitempty"`
	Description     string            `json:"description,omitempty"`
	SubtitleTracks  []SubtitleTrack   `json:"subtitleTracks,omitempty"`
	Thumbnail       string            `json:"thumbnail,omitempty"`
	Chapters        []Chapter         `json:"chapters,omitempty"`
	Bvid            string            `json:"bvid"`
	Cid             int64             `json:"cid"`
	Page            int               `json:"page"`
}

type catalogAPI interface {
	ReadMedia(requestPath string, params map[string]string) (any, error)
}

type CatalogReader struct {
	api catalogAPI
}

func NewCatalogReader(api catalogAPI) *CatalogReader { return &CatalogReader{api: api} }

var biliBVPattern = regexp.MustCompile(`^BV[A-Za-z0-9]{10}$`)
var biliPagePattern = regexp.MustCompile(`^[1-9]\d{0,4}$`)

func MediaInput(input string) (string, int, error) {
	value := strings.TrimSpace(input)
	if biliBVPattern.MatchString(value) {
		value = "https://www.bilibili.com/video/" + value + "/"
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", 0, contract.InputInvalid("请使用 B 站 BV 视频网址")
	}
	match := regexp.MustCompile(`^/video/(BV[A-Za-z0-9]{10})/?$`).FindStringSubmatch(parsed.Path)
	host := parsed.Hostname()
	if (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		(host != "www.bilibili.com" && host != "bilibili.com") ||
		parsed.User != nil || parsed.Port() != "" || match == nil {
		return "", 0, contract.InputInvalid("请使用 B 站 BV 视频网址")
	}
	pages := parsed.Query()["p"]
	if len(pages) > 1 || (len(pages) == 1 && !biliPagePattern.MatchString(pages[0])) {
		return "", 0, contract.InputInvalid("分集编号无效")
	}
	page := 1
	if len(pages) == 1 {
		page = atoiSafe(pages[0])
	}
	return match[1], page, nil
}

func atoiSafe(value string) int {
	result := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0
		}
		result = result*10 + int(char-'0')
	}
	return result
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intValue(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	}
	return 0
}

func clipText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (reader *CatalogReader) Read(input string, playlist bool, options any) (any, error) {
	selectedBvid, selectedPage, err := MediaInput(input)
	if err != nil {
		return nil, err
	}
	settings, err := mediaOptionsForCatalog(options)
	if err != nil {
		return nil, err
	}
	viewData, err := reader.api.ReadMedia("/x/web-interface/view", map[string]string{"bvid": selectedBvid})
	if err != nil {
		return nil, err
	}
	view, _ := viewData.(map[string]any)
	if view == nil {
		return nil, contract.Unavailable("视频目录格式异常，请稍后重试")
	}
	bvid := stringValue(view["bvid"])
	if !biliBVPattern.MatchString(bvid) {
		return nil, contract.Unavailable("视频目录格式异常，请稍后重试")
	}
	if bvid != selectedBvid {
		return nil, contract.Unavailable("视频目录与所选视频不一致")
	}
	title := stringValue(view["title"])
	description := stringValue(view["desc"])
	pagesRaw, _ := view["pages"].([]any)
	if len(pagesRaw) < 1 || len(pagesRaw) > 10000 {
		return nil, contract.Unavailable("视频目录格式异常，请稍后重试")
	}
	type pageRow struct {
		cid      int64
		page     int
		part     string
		duration float64
	}
	pages := []pageRow{}
	pageNumbers := map[int]bool{}
	cids := map[int64]bool{}
	for _, item := range pagesRaw {
		row, _ := item.(map[string]any)
		if row == nil {
			return nil, contract.Unavailable("视频目录格式异常，请稍后重试")
		}
		page := pageRow{
			cid:      intValue(row["cid"]),
			page:     int(intValue(row["page"])),
			part:     stringValue(row["part"]),
			duration: numberValue(row["duration"]),
		}
		if page.cid <= 0 || page.page <= 0 {
			return nil, contract.Unavailable("视频目录格式异常，请稍后重试")
		}
		pages = append(pages, page)
		pageNumbers[page.page] = true
		cids[page.cid] = true
	}
	if len(pageNumbers) != len(pages) || len(cids) != len(pages) {
		return nil, contract.Unavailable("分集目录重复，请稍后重试")
	}
	var selected *pageRow
	for index := range pages {
		if pages[index].page == selectedPage {
			selected = &pages[index]
			break
		}
	}
	if selected == nil {
		return nil, contract.NotFound("所选分集不存在")
	}
	pageURL := fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", bvid, selected.page)
	if playlist {
		entries := []map[string]string{}
		limit := len(pages)
		if limit > 100 {
			limit = 100
		}
		for _, page := range pages[:limit] {
			entries = append(entries, map[string]string{
				"title": clipText(page.part, 240),
				"url":   fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", bvid, page.page),
			})
		}
		return Catalog{
			URL: pageURL, Title: clipText(title, 240), Playlist: true,
			Limited: len(pages) > 100, Entries: entries,
		}, nil
	}
	playData, err := reader.api.ReadMedia("/x/player/wbi/playurl", map[string]string{
		"bvid": bvid, "cid": fmt.Sprintf("%d", selected.cid), "qn": "127", "fnval": "4048", "fourk": "1",
	})
	if err != nil {
		return nil, err
	}
	play, _ := playData.(map[string]any)
	if play == nil {
		return nil, contract.Unavailable("媒体格式格式异常，请稍后重试")
	}
	streams := []MediaStream{}
	dash, _ := play["dash"].(map[string]any)
	if dash != nil {
		for _, kind := range []string{"video", "audio"} {
			rows, _ := dash[kind].([]any)
			for _, item := range rows {
				row, _ := item.(map[string]any)
				if row == nil {
					continue
				}
				addresses := []string{}
				for _, candidate := range append([]any{row["baseUrl"], row["base_url"]}, backupList(row)...) {
					if text, ok := candidate.(string); ok && text != "" {
						addresses = append(addresses, text)
					}
				}
				if len(addresses) == 0 {
					return nil, contract.Unavailable("媒体格式缺少下载地址")
				}
				validated := []string{}
				seen := map[string]bool{}
				for _, address := range addresses {
					checked, err := MediaAddress(address)
					if err != nil {
						return nil, err
					}
					if !seen[checked] {
						seen[checked] = true
						validated = append(validated, checked)
					}
				}
				mime := stringValue(row["mimeType"])
				if mime == "" {
					mime = stringValue(row["mime_type"])
				}
				extension := "mp4"
				if strings.Contains(mime, "webm") {
					extension = "webm"
				} else if kind == "audio" {
					extension = "m4a"
				}
				streams = append(streams, MediaStream{
					ID:   fmt.Sprintf("%s%d-%d", map[string]string{"video": "v", "audio": "a"}[kind], intValue(row["id"]), intValue(row["codecid"])),
					Kind: kind, URLs: validated,
					Height: int(intValue(row["height"])), Codec: stringValue(row["codecs"]),
					Bandwidth: numberValue(row["bandwidth"]), Extension: extension,
				})
			}
		}
	}
	if len(streams) == 0 {
		if durl, ok := play["durl"].([]any); ok && len(durl) > 0 {
			urls := []string{}
			for _, item := range durl {
				row, _ := item.(map[string]any)
				if row == nil {
					continue
				}
				address, err := MediaAddress(stringValue(row["url"]))
				if err != nil {
					return nil, err
				}
				urls = append(urls, address)
			}
			streams = append(streams, MediaStream{
				ID: fmt.Sprintf("c%d", int64(numberValue(play["quality"]))), Kind: "combined",
				URLs: urls, Extension: "flv",
			})
		}
	}
	if len(streams) == 0 && !settings.SubtitleOnly {
		return nil, contract.Unavailable("未取得可用音视频格式，请检查登录和访问权限")
	}
	ids := map[string]bool{}
	for _, stream := range streams {
		if ids[stream.ID] {
			return nil, contract.Unavailable("媒体格式编号重复")
		}
		ids[stream.ID] = true
	}
	playerData, err := reader.api.ReadMedia("/x/player/v2", map[string]string{
		"bvid": bvid, "cid": fmt.Sprintf("%d", selected.cid),
	})
	if err != nil {
		return nil, err
	}
	player, _ := playerData.(map[string]any)
	subtitles := []any{}
	viewPoints := []any{}
	if player != nil {
		if subtitleRow, ok := player["subtitle"].(map[string]any); ok {
			subtitles, _ = subtitleRow["subtitles"].([]any)
		}
		viewPoints, _ = player["view_points"].([]any)
	}
	heightsSet := map[int]bool{}
	formats := []map[string]any{}
	subtitleLanguages := []string{}
	for _, item := range subtitles {
		row, _ := item.(map[string]any)
		language := stringValue(row["lan"])
		if language != "" && language != "danmaku" {
			subtitleLanguages = append(subtitleLanguages, language)
		}
	}
	for _, stream := range streams {
		if stream.Kind == "video" {
			heightsSet[stream.Height] = true
		}
		videoCodec := stream.Codec
		audioCodec := "none"
		if stream.Kind == "audio" {
			videoCodec = "none"
			audioCodec = stream.Codec
		}
		var height any
		if stream.Height > 0 {
			height = stream.Height
		}
		formats = append(formats, map[string]any{
			"id": stream.ID, "height": height, "extension": stream.Extension,
			"video_codec": videoCodec, "audio_codec": audioCodec,
		})
	}
	heights := []int{}
	for height := range heightsSet {
		heights = append(heights, height)
	}
	sortInts(heights)
	catalog := Catalog{
		URL: pageURL, Title: clipText(firstNonEmpty(selected.part, title), 240), Playlist: false,
		Duration: &selected.duration, Heights: heights, Subtitles: subtitleLanguages, Formats: formats,
	}
	if settings.FormatIDs != "" {
		for _, id := range strings.Split(settings.FormatIDs, "+") {
			if !ids[id] {
				return nil, contract.InputInvalid("所选格式已改变，请重新读取格式")
			}
		}
	}
	source := Source{
		Catalog: catalog, Identity: fmt.Sprintf("%s:%d:%d", bvid, selected.cid, selected.page),
		Streams: streams, SourceID: fmt.Sprintf("%s-P%d", bvid, selected.page), Platform: "bilibili",
		DownloadHeaders: map[string]string{"Referer": "https://www.bilibili.com/", "User-Agent": "Mozilla/5.0"},
		Description:     description, Bvid: bvid, Cid: selected.cid, Page: selected.page,
	}
	if pic := stringValue(view["pic"]); pic != "" {
		thumbnail, err := MediaAddress(strings.ReplaceAll(pic, "http:", "https:"))
		if err == nil {
			source.Thumbnail = thumbnail
		}
	}
	for _, item := range subtitles {
		row, _ := item.(map[string]any)
		address := stringValue(row["subtitle_url"])
		language := stringValue(row["lan"])
		if address == "" || language == "danmaku" {
			continue
		}
		checked, err := MediaAddress(address)
		if err != nil {
			continue
		}
		source.SubtitleTracks = append(source.SubtitleTracks, SubtitleTrack{
			Language: language, URL: checked,
			Automatic: strings.HasPrefix(language, "ai-") || strings.HasPrefix(language, "auto") || intValue(row["ai_type"]) != 0,
		})
	}
	for _, item := range viewPoints {
		row, _ := item.(map[string]any)
		from := numberValue(row["from"])
		to := numberValue(row["to"])
		if to <= from || to > selected.duration+1 {
			continue
		}
		source.Chapters = append(source.Chapters, Chapter{Start: from, End: to, Title: stringValue(row["content"])})
	}
	return source, nil
}

func backupList(row map[string]any) []any {
	for _, key := range []string{"backupUrl", "backup_url"} {
		if values, ok := row[key].([]any); ok {
			return values
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func mediaOptionsForCatalog(options any) (media.NativeMediaOptions, error) {
	if options == nil {
		return media.MediaOptions(map[string]any{})
	}
	return media.MediaOptions(options)
}
