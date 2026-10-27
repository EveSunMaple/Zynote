package bilibili

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

const errVideoCatalogInvalid = "视频目录格式异常，请稍后重试"

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

type MediaStream = media.MediaStream

type SubtitleTrack = media.SubtitleTrack

type Chapter = media.Chapter

type Catalog = media.Catalog

type Source = media.Source

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

func readVideoView(reader *CatalogReader, selectedBvid string, selectedPage int) (string, string, string, string, []pageRow, pageRow, error) {
	viewData, err := reader.api.ReadMedia("/x/web-interface/view", map[string]string{"bvid": selectedBvid})
	if err != nil {
		return "", "", "", "", nil, pageRow{}, err
	}
	view, _ := viewData.(map[string]any)
	if view == nil {
		return "", "", "", "", nil, pageRow{}, contract.Unavailable(errVideoCatalogInvalid)
	}
	bvid := stringValue(view["bvid"])
	if !biliBVPattern.MatchString(bvid) {
		return "", "", "", "", nil, pageRow{}, contract.Unavailable(errVideoCatalogInvalid)
	}
	if bvid != selectedBvid {
		return "", "", "", "", nil, pageRow{}, contract.Unavailable("视频目录与所选视频不一致")
	}
	pagesRaw, _ := view["pages"].([]any)
	if len(pagesRaw) < 1 || len(pagesRaw) > 10000 {
		return "", "", "", "", nil, pageRow{}, contract.Unavailable(errVideoCatalogInvalid)
	}
	pages := []pageRow{}
	pageNumbers := map[int]bool{}
	cids := map[int64]bool{}
	for _, item := range pagesRaw {
		row, _ := item.(map[string]any)
		if row == nil {
			return "", "", "", "", nil, pageRow{}, contract.Unavailable(errVideoCatalogInvalid)
		}
		page := pageRow{
			CID: intValue(row["cid"]), Page: int(intValue(row["page"])),
			Part: stringValue(row["part"]), Duration: numberValue(row["duration"]),
		}
		if page.CID <= 0 || page.Page <= 0 {
			return "", "", "", "", nil, pageRow{}, contract.Unavailable(errVideoCatalogInvalid)
		}
		pages = append(pages, page)
		pageNumbers[page.Page] = true
		cids[page.CID] = true
	}
	if len(pageNumbers) != len(pages) || len(cids) != len(pages) {
		return "", "", "", "", nil, pageRow{}, contract.Unavailable("分集目录重复，请稍后重试")
	}
	for _, page := range pages {
		if page.Page == selectedPage {
			return bvid, stringValue(view["title"]), stringValue(view["desc"]), stringValue(view["pic"]), pages, page, nil
		}
	}
	return "", "", "", "", nil, pageRow{}, contract.NotFound("所选分集不存在")
}

func catalogPlaylist(bvid, title, pageURL string, pages []pageRow) Catalog {
	entries := []map[string]string{}
	limit := len(pages)
	if limit > 100 {
		limit = 100
	}
	for _, page := range pages[:limit] {
		entries = append(entries, map[string]string{
			"title": clipText(page.Part, 240),
			"url":   fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", bvid, page.Page),
		})
	}
	return Catalog{
		URL: pageURL, Title: clipText(title, 240), Playlist: true,
		Limited: len(pages) > 100, Entries: entries,
	}
}

func dashAddresses(row map[string]any) []string {
	addresses := []string{}
	for _, candidate := range append([]any{row["baseUrl"], row["base_url"]}, backupList(row)...) {
		if text, ok := candidate.(string); ok && text != "" {
			addresses = append(addresses, text)
		}
	}
	return addresses
}

func validatedMediaAddresses(addresses []string) []string {
	validated := []string{}
	seen := map[string]bool{}
	for _, address := range addresses {
		checked, err := MediaAddress(address)
		if err != nil {
			continue
		}
		if !seen[checked] {
			seen[checked] = true
			validated = append(validated, checked)
		}
	}
	return validated
}

func dashStream(kind string, row map[string]any) (MediaStream, error) {
	addresses := dashAddresses(row)
	if len(addresses) == 0 {
		return MediaStream{}, contract.Unavailable("媒体格式缺少下载地址")
	}
	validated := validatedMediaAddresses(addresses)
	if len(validated) == 0 {
		return MediaStream{}, contract.Unavailable("媒体格式缺少可信下载地址")
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
	return MediaStream{
		ID:   fmt.Sprintf("%s%d-%d", map[string]string{"video": "v", "audio": "a"}[kind], intValue(row["id"]), intValue(row["codecid"])),
		Kind: kind, URLs: validated,
		Height: int(intValue(row["height"])), Codec: stringValue(row["codecs"]),
		Bandwidth: numberValue(row["bandwidth"]), Extension: extension,
	}, nil
}

func durlStreams(play map[string]any) ([]MediaStream, error) {
	durl, ok := play["durl"].([]any)
	if !ok || len(durl) == 0 {
		return nil, nil
	}
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
	return []MediaStream{{
		ID: fmt.Sprintf("c%d", int64(numberValue(play["quality"]))), Kind: "combined",
		URLs: urls, Extension: "flv",
	}}, nil
}

func readCatalogStreams(reader *CatalogReader, bvid string, page pageRow) ([]MediaStream, error) {
	playData, err := reader.api.ReadMedia("/x/player/wbi/playurl", map[string]string{
		"bvid": bvid, "cid": fmt.Sprintf("%d", page.CID), "qn": "127", "fnval": "4048", "fourk": "1",
	})
	if err != nil {
		return nil, err
	}
	play, _ := playData.(map[string]any)
	if play == nil {
		return nil, contract.Unavailable("媒体格式格式异常，请稍后重试")
	}
	streams := []MediaStream{}
	if dash, _ := play["dash"].(map[string]any); dash != nil {
		for _, kind := range []string{"video", "audio"} {
			rows, _ := dash[kind].([]any)
			for _, item := range rows {
				row, _ := item.(map[string]any)
				if row == nil {
					continue
				}
				stream, err := dashStream(kind, row)
				if err != nil {
					return nil, err
				}
				streams = append(streams, stream)
			}
		}
	}
	if len(streams) == 0 {
		values, err := durlStreams(play)
		if err != nil {
			return nil, err
		}
		streams = values
	}
	ids := map[string]bool{}
	for _, stream := range streams {
		if ids[stream.ID] {
			return nil, contract.Unavailable("媒体格式编号重复")
		}
		ids[stream.ID] = true
	}
	return streams, nil
}

func readCatalogPlayer(reader *CatalogReader, bvid string, page pageRow) ([]any, []any, error) {
	playerData, err := reader.api.ReadMedia("/x/player/v2", map[string]string{
		"bvid": bvid, "cid": fmt.Sprintf("%d", page.CID),
	})
	if err != nil {
		return nil, nil, err
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
	return subtitles, viewPoints, nil
}

func catalogFormats(streams []MediaStream, subtitles []any) ([]int, []map[string]any, []string) {
	subtitleLanguages := []string{}
	for _, item := range subtitles {
		row, _ := item.(map[string]any)
		language := stringValue(row["lan"])
		if language != "" && language != "danmaku" {
			subtitleLanguages = append(subtitleLanguages, language)
		}
	}
	heightsSet := map[int]bool{}
	formats := []map[string]any{}
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
	return heights, formats, subtitleLanguages
}

func applyCatalogSubtitles(source *Source, subtitles []any) {
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
}

func applyCatalogChapters(source *Source, viewPoints []any, duration float64) {
	for _, item := range viewPoints {
		row, _ := item.(map[string]any)
		from := numberValue(row["from"])
		to := numberValue(row["to"])
		if to <= from || to > duration+1 {
			continue
		}
		source.Chapters = append(source.Chapters, Chapter{Start: from, End: to, Title: stringValue(row["content"])})
	}
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
	bvid, title, description, pic, pages, selected, err := readVideoView(reader, selectedBvid, selectedPage)
	if err != nil {
		return nil, err
	}
	pageURL := fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d", bvid, selected.Page)
	if playlist {
		return catalogPlaylist(bvid, title, pageURL, pages), nil
	}
	streams, err := readCatalogStreams(reader, bvid, selected)
	if err != nil {
		return nil, err
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
	subtitles, viewPoints, err := readCatalogPlayer(reader, bvid, selected)
	if err != nil {
		return nil, err
	}
	heights, formats, subtitleLanguages := catalogFormats(streams, subtitles)
	catalog := Catalog{
		URL: pageURL, Title: clipText(firstNonEmpty(selected.Part, title), 240), Playlist: false,
		Duration: &selected.Duration, Heights: heights, Subtitles: subtitleLanguages, Formats: formats,
	}
	if settings.FormatIDs != "" {
		for _, id := range strings.Split(settings.FormatIDs, "+") {
			if !ids[id] {
				return nil, contract.InputInvalid("所选格式已改变，请重新读取格式")
			}
		}
	}
	source := Source{
		Catalog: catalog, Identity: fmt.Sprintf("%s:%d:%d", bvid, selected.CID, selected.Page),
		Streams: streams, SourceID: fmt.Sprintf("%s-P%d", bvid, selected.Page), Platform: "bilibili",
		DownloadHeaders: map[string]string{"Referer": "https://www.bilibili.com/", "User-Agent": "Mozilla/5.0"},
		Description:     description, Bvid: bvid, Cid: selected.CID, Page: selected.Page,
	}
	if pic != "" {
		thumbnail, err := MediaAddress(strings.ReplaceAll(pic, "http:", "https:"))
		if err == nil {
			source.Thumbnail = thumbnail
		}
	}
	applyCatalogSubtitles(&source, subtitles)
	applyCatalogChapters(&source, viewPoints, selected.Duration)
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
