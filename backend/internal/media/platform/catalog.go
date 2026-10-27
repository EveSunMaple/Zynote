package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

const (
	hostYouTube     = "youtube.com"
	hostYoutuBe     = "youtu.be"
	hostDailymotion = "dailymotion.com"
	hostVimeoCdn    = "vimeocdn.com"
	hostDmcdn       = "dmcdn.net"
	headerUserAgent = "User-Agent"
)

type Selection struct {
	Platform string
	URL      string
	ID       string
	Hash     string
}

var (
	bvInputPattern     = regexp.MustCompile(`^BV[A-Za-z0-9]{10}$`)
	vimeoPathPattern   = regexp.MustCompile(`^/(?:video/)?([1-9]\d{0,14})(?:/([A-Za-z0-9]{1,64}))?/?$`)
	dailymotionPattern = regexp.MustCompile(`^/video/([A-Za-z0-9]{1,32})/?$`)
	shortDailymotion   = regexp.MustCompile(`^/([A-Za-z0-9]{1,32})/?$`)
	youtubeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	youtubeListPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,100}$`)
	youtubeShorts      = regexp.MustCompile(`^/(?:shorts|embed)/([A-Za-z0-9_-]{11})/?$`)
	languagePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	vimeoTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

func parseInputURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("视频网址无效")
	}
	return parsed, nil
}

func vimeoInput(parsed *url.URL) (Selection, error) {
	match := vimeoPathPattern.FindStringSubmatch(parsed.Path)
	hashes := parsed.Query()["h"]
	if match == nil || len(hashes) > 1 {
		return Selection{}, fmt.Errorf("请使用具体的 Vimeo 视频网址")
	}
	for _, hash := range hashes {
		if !vimeoTokenPattern.MatchString(hash) {
			return Selection{}, fmt.Errorf("请使用具体的 Vimeo 视频网址")
		}
	}
	token := match[2]
	if token == "" && len(hashes) == 1 {
		token = hashes[0]
	}
	if match[2] != "" && len(hashes) == 1 && match[2] != hashes[0] {
		return Selection{}, fmt.Errorf("视频访问标识有歧义")
	}
	value := "https://vimeo.com/" + match[1]
	if token != "" {
		value += "/" + token
	}
	return Selection{Platform: "vimeo", URL: value, ID: match[1], Hash: token}, nil
}

func dailymotionInput(parsed *url.URL) (Selection, error) {
	pattern := dailymotionPattern
	if parsed.Hostname() == "dai.ly" {
		pattern = shortDailymotion
	}
	match := pattern.FindStringSubmatch(parsed.Path)
	if match == nil {
		return Selection{}, fmt.Errorf("请使用具体的 Dailymotion 视频网址")
	}
	return Selection{
		Platform: "dailymotion", URL: "https://www.dailymotion.com/video/" + match[1], ID: match[1],
	}, nil
}

func Input(raw string) (Selection, error) {
	trimmed := strings.TrimSpace(raw)
	if bvInputPattern.MatchString(trimmed) {
		return Selection{Platform: "bilibili", URL: trimmed}, nil
	}
	parsed, err := parseInputURL(trimmed)
	if err != nil {
		return Selection{}, err
	}
	host := parsed.Hostname()
	switch {
	case host == "bilibili.com" || host == "www.bilibili.com":
		return Selection{Platform: "bilibili", URL: parsed.String()}, nil
	case host == hostYouTube || host == "www.youtube.com" || host == "m.youtube.com" ||
		host == "music.youtube.com" || host == hostYoutuBe:
		return youtubeInput(parsed)
	case host == "vimeo.com" || host == "www.vimeo.com" || host == "player.vimeo.com":
		return vimeoInput(parsed)
	case host == "www.dailymotion.com" || host == hostDailymotion || host == "dai.ly":
		return dailymotionInput(parsed)
	default:
		return Selection{}, fmt.Errorf("此站点的原生解析仍在迁移；支持 B站、YouTube、Vimeo 和 Dailymotion")
	}
}

func validYoutubeURL(parsed *url.URL, host string) bool {
	return parsed.Scheme == "https" && parsed.Port() == "" && parsed.User == nil && parsed.Fragment == "" &&
		(host == hostYouTube || host == "m.youtube.com" || host == hostYoutuBe || host == "music.youtube.com")
}

func youtubeVideoID(host string, parsed *url.URL, videos []string) string {
	switch {
	case host == hostYoutuBe:
		return strings.TrimPrefix(parsed.Path, "/")
	case parsed.Path == "/watch":
		if len(videos) == 1 {
			return videos[0]
		}
	default:
		if match := youtubeShorts.FindStringSubmatch(parsed.Path); match != nil {
			return match[1]
		}
	}
	return ""
}

func validateYoutubeSelection(id, list string) error {
	if (id != "" && !youtubeIDPattern.MatchString(id)) ||
		(list != "" && !youtubeListPattern.MatchString(list)) ||
		(id == "" && list == "") {
		return fmt.Errorf("请使用具体视频或播放列表网址")
	}
	return nil
}

func youtubeURL(id, list string) string {
	if id == "" {
		return "https://www.youtube.com/playlist?list=" + list
	}
	value := "https://www.youtube.com/watch?v=" + id
	if list != "" {
		value += "&list=" + list
	}
	return value
}

func youtubeInput(parsed *url.URL) (Selection, error) {
	host := strings.TrimPrefix(parsed.Hostname(), "www.")
	if !validYoutubeURL(parsed, host) {
		return Selection{}, fmt.Errorf("YouTube 网址无效")
	}
	lists := parsed.Query()["list"]
	videos := parsed.Query()["v"]
	if len(lists) > 1 || len(videos) > 1 {
		return Selection{}, fmt.Errorf("视频或播放列表标识有歧义")
	}
	id := youtubeVideoID(host, parsed, videos)
	list := ""
	if len(lists) == 1 {
		list = lists[0]
	}
	if err := validateYoutubeSelection(id, list); err != nil {
		return Selection{}, err
	}
	return Selection{Platform: "youtube", URL: youtubeURL(id, list), ID: id, Hash: list}, nil
}

type Fetcher = media.AuthFetcher

type NativeCaller interface {
	Call(method string, params any, timeoutMS int) (json.RawMessage, error)
}

func Read(ctx context.Context, selection Selection, playlist bool, options media.NativeMediaOptions, fetcher Fetcher, youtube NativeCaller) (any, error) {
	if selection.Platform == "youtube" {
		return readYoutube(ctx, selection, playlist, options, youtube, fetcher)
	}
	if playlist {
		return nil, fmt.Errorf("此平台当前请逐条选择视频，不把单个视频误当作完整播放列表")
	}
	endpoint := ""
	switch selection.Platform {
	case "vimeo":
		endpoint = "https://player.vimeo.com/video/" + selection.ID + "/config"
		if selection.Hash != "" {
			endpoint += "?h=" + url.QueryEscape(selection.Hash)
		}
	case "dailymotion":
		endpoint = "https://www.dailymotion.com/player/metadata/video/" + selection.ID
	default:
		return nil, fmt.Errorf("此站点的原生解析仍在迁移；支持 B站、YouTube、Vimeo 和 Dailymotion")
	}
	raw, err := requestJSON(ctx, fetcher, endpoint)
	if err != nil {
		return nil, err
	}
	if selection.Platform == "vimeo" {
		return vimeoSource(selection, raw, options)
	}
	return dailymotionSource(selection, raw, options)
}

func readYoutubeVideo(ctx context.Context, id string, options media.NativeMediaOptions, caller NativeCaller, fetcher Fetcher) (media.Source, error) {
	raw, err := caller.Call("youtube.video", map[string]any{"id": id}, 120000)
	if err != nil {
		return media.Source{}, err
	}
	var source media.Source
	if err := json.Unmarshal(raw, &source); err != nil {
		return media.Source{}, fmt.Errorf("YouTube 目录读取失败：响应格式异常")
	}
	if source.Identity != "youtube:"+id {
		return media.Source{}, fmt.Errorf("YouTube 目录与所选视频身份不一致")
	}
	addYoutubeTranslations(ctx, &source, id, options, fetcher)
	if err := validateYoutubeSource(source, options); err != nil {
		return media.Source{}, err
	}
	return source, nil
}

func validateYoutubeSource(source media.Source, options media.NativeMediaOptions) error {
	if len(source.Streams) == 0 && !options.SubtitleOnly {
		return fmt.Errorf("YouTube 未返回可读取格式，请检查访问权限或站点验证要求")
	}
	if options.Subtitles && len(source.SubtitleTracks) == 0 {
		return fmt.Errorf("此视频没有提供可用字幕；可下载后使用本地转写")
	}
	if err := validateFormats(options, source.Streams); err != nil {
		return fmt.Errorf("YouTube 格式已改变，请重新读取")
	}
	return nil
}

func readYoutube(ctx context.Context, selection Selection, playlist bool, options media.NativeMediaOptions, caller NativeCaller, fetcher Fetcher) (any, error) {
	if caller == nil {
		return nil, fmt.Errorf("YouTube 原生执行器不可用")
	}
	if playlist {
		if selection.Hash == "" {
			return nil, fmt.Errorf("请提供带播放列表标识的网址，单个视频不冒充整套课程")
		}
		return readYoutubePlaylist(ctx, selection.Hash, fetcher)
	}
	if selection.ID == "" {
		return nil, fmt.Errorf("请先选择播放列表中的具体视频")
	}
	return readYoutubeVideo(ctx, selection.ID, options, caller, fetcher)
}

func addYoutubeTranslations(ctx context.Context, source *media.Source, id string, options media.NativeMediaOptions, fetcher Fetcher) {
	if !options.TranslatedSubtitles || fetcher == nil {
		return
	}
	response, err := fetcher("https://www.youtube.com/watch?v="+id+"&hl=en", map[string]string{
		headerUserAgent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36",
		"Accept-Language": "en",
	}, ctx)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(body) > 8*1024*1024 {
		return
	}
	html := string(body)
	marker := strings.Index(html, "ytInitialPlayerResponse")
	if marker < 0 {
		return
	}
	equal := strings.Index(html[marker:], "=")
	if equal < 0 {
		return
	}
	start := strings.Index(html[marker+equal:], "{")
	if start < 0 {
		return
	}
	var page map[string]any
	if json.NewDecoder(strings.NewReader(html[marker+equal+start:])).Decode(&page) != nil {
		return
	}
	var renderer map[string]any
	walkJSON(page, func(node map[string]any) {
		if renderer != nil {
			return
		}
		if value, ok := node["playerCaptionsTracklistRenderer"].(map[string]any); ok {
			renderer = value
		}
	})
	if renderer == nil {
		return
	}
	original := ""
	for _, raw := range arrayOf(renderer["captionTracks"]) {
		track := objectOf(raw)
		if track["isTranslatable"] == true {
			original = stringOf(track["baseUrl"])
			break
		}
	}
	if original == "" {
		return
	}
	seen := map[string]bool{}
	for _, track := range source.SubtitleTracks {
		seen[strings.ToLower(track.Language)] = true
	}
	for _, raw := range arrayOf(renderer["translationLanguages"]) {
		language := stringOf(objectOf(raw)["languageCode"])
		if !regexp.MustCompile(`^[A-Za-z-]{2,20}$`).MatchString(language) {
			continue
		}
		name := "ai-" + language
		if seen[strings.ToLower(name)] || !media.MatchesSubtitleLanguage(name, options.SubtitleLanguages) {
			continue
		}
		target, err := url.Parse(original)
		if err != nil || target.Scheme != "https" ||
			!(target.Hostname() == hostYouTube || strings.HasSuffix(target.Hostname(), ".youtube.com")) {
			continue
		}
		query := target.Query()
		query.Set("fmt", "json3")
		query.Set("tlang", language)
		target.RawQuery = query.Encode()
		source.SubtitleTracks = append(source.SubtitleTracks, media.SubtitleTrack{
			Language: name, URL: target.String(), Automatic: true, Format: "youtube-json3",
		})
		seen[strings.ToLower(name)] = true
		source.Catalog.Subtitles = append(source.Catalog.Subtitles, name)
	}
}

func requestJSON(ctx context.Context, fetcher Fetcher, endpoint string) (map[string]any, error) {
	response, err := fetcher(endpoint, map[string]string{headerUserAgent: "Mozilla/5.0"}, ctx)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("此视频目录无法取得，请检查是否公开及站点访问权限")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4*1024*1024 {
		return nil, fmt.Errorf("此视频目录无法取得，请检查是否公开及站点访问权限")
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("此视频目录无法取得，请检查是否公开及站点访问权限")
	}
	return value, nil
}

func objectOf(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func arrayOf(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return []any{}
}

func stringOf(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func positiveNumber(value any) (float64, bool) {
	number, ok := numberValue(value)
	return number, ok && number > 0 && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func mediaAddress(raw string, domains []string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" ||
		parsed.Fragment != "" {
		return "", fmt.Errorf("站点媒体地址未通过核验")
	}
	for _, domain := range domains {
		if parsed.Hostname() == domain || strings.HasSuffix(parsed.Hostname(), "."+domain) {
			return parsed.String(), nil
		}
	}
	return "", fmt.Errorf("站点媒体地址未通过核验")
}

func truncateTitle(value string) string {
	runes := []rune(value)
	if len(runes) > 240 {
		runes = runes[:240]
	}
	return string(runes)
}

func validateFormats(options media.NativeMediaOptions, streams []media.MediaStream) error {
	if options.FormatIDs == "" {
		return nil
	}
	known := map[string]bool{}
	for _, stream := range streams {
		known[stream.ID] = true
	}
	for _, id := range strings.Split(options.FormatIDs, "+") {
		if !known[id] {
			return fmt.Errorf("所选格式已改变，请重新读取格式")
		}
	}
	return nil
}

func vimeoID(value any) string {
	switch typed := value.(type) {
	case float64:
		if typed >= 0 && math.Trunc(typed) == typed {
			return strconv.FormatInt(int64(typed), 10)
		}
	case string:
		return typed
	}
	return ""
}

func vimeoProgressiveStreams(files map[string]any) ([]media.MediaStream, error) {
	streams := []media.MediaStream{}
	progressive := arrayOf(files["progressive"])
	if len(progressive) > 100 {
		progressive = progressive[:100]
	}
	for index, item := range progressive {
		row := objectOf(item)
		height, heightOK := numberValue(row["height"])
		if !heightOK || height < 0 || math.Trunc(height) != height {
			continue
		}
		address, err := mediaAddress(stringOf(row["url"]), []string{hostVimeoCdn, "akamaized.net"})
		if err != nil {
			return nil, err
		}
		bitrate, _ := numberValue(row["bitrate"])
		extension := "mp4"
		if strings.Contains(stringOf(row["mime"]), "webm") {
			extension = "webm"
		}
		streams = append(streams, media.MediaStream{
			ID: fmt.Sprintf("vimeo-%d-%d", int(height), index), Kind: "combined", URLs: []string{address},
			Height: int(height), Codec: "avc1", Bandwidth: bitrate * 1000, Extension: extension,
		})
	}
	return streams, nil
}

func vimeoHLSStream(files map[string]any) []media.MediaStream {
	hls := objectOf(files["hls"])
	if len(hls) == 0 {
		return nil
	}
	cdn := objectOf(objectOf(hls["cdns"])[stringOf(hls["default_cdn"])])
	address, err := mediaAddress(stringOf(cdn["url"]), []string{hostVimeoCdn, "akamaized.net"})
	if err != nil {
		return nil
	}
	return []media.MediaStream{{
		ID: "hls-auto", Kind: "combined", URLs: []string{address}, Extension: "mp4", Transport: "hls",
	}}
}

func vimeoThumbnail(video map[string]any) string {
	thumbs := objectOf(video["thumbs"])
	if len(thumbs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(thumbs))
	for key := range thumbs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		leftNumber, _ := strconv.Atoi(keys[left])
		rightNumber, _ := strconv.Atoi(keys[right])
		return leftNumber > rightNumber
	})
	address, err := mediaAddress(stringOf(thumbs[keys[0]]), []string{hostVimeoCdn})
	if err != nil {
		return ""
	}
	return address
}

func vimeoSubtitleTracks(request map[string]any) []media.SubtitleTrack {
	tracks := []media.SubtitleTrack{}
	captions := arrayOf(request["text_tracks"])
	if len(captions) > 100 {
		captions = captions[:100]
	}
	for _, item := range captions {
		row := objectOf(item)
		language := stringOf(row["lang"])
		if !languagePattern.MatchString(language) {
			continue
		}
		base, err := url.Parse("https://player.vimeo.com/")
		if err != nil {
			continue
		}
		target, err := base.Parse(stringOf(row["url"]))
		if err != nil {
			continue
		}
		address, err := mediaAddress(target.String(), []string{"vimeo.com", hostVimeoCdn})
		if err != nil {
			continue
		}
		duplicate := false
		for _, track := range tracks {
			if track.Language == language {
				duplicate = true
				break
			}
		}
		if !duplicate {
			tracks = append(tracks, media.SubtitleTrack{Language: language, URL: address, Automatic: false, Format: "webvtt"})
		}
	}
	return tracks
}

func vimeoSource(selection Selection, raw map[string]any, options media.NativeMediaOptions) (media.Source, error) {
	video := objectOf(raw["video"])
	id := vimeoID(video["id"])
	title := stringOf(video["title"])
	duration, durationOK := positiveNumber(video["duration"])
	if id != selection.ID || title == "" || !durationOK || len([]rune(title)) > 2000 {
		return media.Source{}, fmt.Errorf("Vimeo 目录与所选视频身份不一致")
	}
	request := objectOf(raw["request"])
	files := objectOf(request["files"])
	streams, err := vimeoProgressiveStreams(files)
	if err != nil {
		return media.Source{}, err
	}
	if len(streams) == 0 {
		streams = vimeoHLSStream(files)
	}
	return finishSource(selection, "vimeo", title, duration, "", vimeoThumbnail(video), streams, vimeoSubtitleTracks(request), options)
}

func dailymotionStreams(qualities map[string]any) ([]media.MediaStream, error) {
	streams := []media.MediaStream{}
	for quality, value := range qualities {
		formats := arrayOf(value)
		for index, item := range formats {
			row := objectOf(item)
			kind := stringOf(row["type"])
			if kind != "video/mp4" && kind != "video/webm" {
				continue
			}
			address, err := mediaAddress(stringOf(row["url"]), []string{hostDailymotion, hostDmcdn})
			if err != nil {
				return nil, err
			}
			height := 0
			if digitsOnly(quality) {
				height, _ = strconv.Atoi(quality)
			}
			codec, extension := "avc1", "mp4"
			if kind == "video/webm" {
				codec, extension = "vp9", "webm"
			}
			streams = append(streams, media.MediaStream{
				ID: fmt.Sprintf("dm-%s-%d", sanitizeID(quality), index), Kind: "combined", URLs: []string{address},
				Height: height, Codec: codec, Extension: extension,
			})
		}
	}
	return streams, nil
}

func dailymotionHLSStream(qualities map[string]any) []media.MediaStream {
	hlsURL := ""
	for _, value := range qualities {
		for _, item := range arrayOf(value) {
			row := objectOf(item)
			kind := stringOf(row["type"])
			if kind == "application/x-mpegURL" || kind == "application/vnd.apple.mpegurl" {
				hlsURL = stringOf(row["url"])
				break
			}
		}
	}
	if hlsURL == "" {
		return nil
	}
	address, err := mediaAddress(hlsURL, []string{hostDailymotion, hostDmcdn})
	if err != nil {
		return nil
	}
	return []media.MediaStream{{
		ID: "hls-auto", Kind: "combined", URLs: []string{address}, Extension: "mp4", Transport: "hls",
	}}
}

func dailymotionThumbnail(raw map[string]any) string {
	for _, value := range objectOf(raw["posters"]) {
		if address, err := mediaAddress(stringOf(value), []string{hostDmcdn, hostDailymotion}); err == nil {
			return address
		}
	}
	return ""
}

func dailymotionSubtitleTracks(raw map[string]any) []media.SubtitleTrack {
	tracks := []media.SubtitleTrack{}
	captions := objectOf(objectOf(raw["subtitles"])["data"])
	for language, value := range captions {
		if !languagePattern.MatchString(language) {
			continue
		}
		urls := arrayOf(objectOf(value)["urls"])
		if len(urls) == 0 {
			continue
		}
		if address, err := mediaAddress(stringOf(urls[0]), []string{hostDailymotion, hostDmcdn}); err == nil {
			tracks = append(tracks, media.SubtitleTrack{Language: language, URL: address, Automatic: false, Format: "webvtt"})
		}
	}
	return tracks
}

func dailymotionSource(selection Selection, raw map[string]any, options media.NativeMediaOptions) (media.Source, error) {
	id := stringOf(raw["id"])
	title := stringOf(raw["title"])
	duration, durationOK := positiveNumber(raw["duration"])
	if id != selection.ID || title == "" || !durationOK || raw["error"] != nil || len([]rune(title)) > 2000 {
		return media.Source{}, fmt.Errorf("Dailymotion 未确认所选视频可读取")
	}
	description := stringOf(raw["description"])
	if len([]rune(description)) > 1000000 {
		description = ""
	}
	qualities := objectOf(raw["qualities"])
	streams, err := dailymotionStreams(qualities)
	if err != nil {
		return media.Source{}, err
	}
	if len(streams) == 0 {
		streams = dailymotionHLSStream(qualities)
	}
	return finishSource(selection, "dailymotion", title, duration, description, dailymotionThumbnail(raw), streams, dailymotionSubtitleTracks(raw), options)
}

func finishSource(selection Selection, platformName, title string, duration float64, description, thumbnail string, streams []media.MediaStream, tracks []media.SubtitleTrack, options media.NativeMediaOptions) (media.Source, error) {
	if len(streams) == 0 && !options.SubtitleOnly {
		return media.Source{}, fmt.Errorf("此视频未提供当前支持的可下载格式，未创建假下载成果")
	}
	if options.Subtitles && len(tracks) == 0 {
		return media.Source{}, fmt.Errorf("此视频没有提供可用字幕；可下载后使用本地转写")
	}
	if err := validateFormats(options, streams); err != nil {
		return media.Source{}, err
	}
	heights := []int{}
	seenHeights := map[int]bool{}
	formats := []map[string]any{}
	languages := []string{}
	for _, stream := range streams {
		if stream.Kind != "audio" && !seenHeights[stream.Height] {
			seenHeights[stream.Height] = true
			heights = append(heights, stream.Height)
		}
		var heightValue any
		if stream.Height > 0 {
			heightValue = stream.Height
		}
		videoCodec := stream.Codec
		audioCodec := "combined"
		if stream.Kind == "audio" {
			videoCodec = "none"
			audioCodec = stream.Codec
		}
		formats = append(formats, map[string]any{
			"id": stream.ID, "height": heightValue, "extension": stream.Extension,
			"video_codec": videoCodec, "audio_codec": audioCodec,
		})
	}
	sort.Ints(heights)
	for _, track := range tracks {
		languages = append(languages, track.Language)
	}
	referer := "https://vimeo.com/"
	if selection.Platform == "dailymotion" {
		referer = "https://www.dailymotion.com/"
	}
	source := media.Source{
		Platform: platformName, SourceID: platformName + "-" + selection.ID,
		Identity: platformName + ":" + selection.ID,
		Streams:  streams, SubtitleTracks: tracks, Description: description, Thumbnail: thumbnail,
		DownloadHeaders: map[string]string{"Referer": referer, headerUserAgent: "Mozilla/5.0"},
		Catalog: media.Catalog{
			URL: selection.URL, Title: truncateTitle(title), Duration: &duration,
			Playlist: false, Heights: heights, Subtitles: languages, Formats: formats,
		},
	}
	return source, nil
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func sanitizeID(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
