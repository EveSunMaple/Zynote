package media

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type NativeMediaOptions struct {
	Height              int      `json:"height"`
	AudioQuality        string   `json:"audio_quality"`
	AudioOnly           bool     `json:"audio_only"`
	SubtitleOnly        bool     `json:"subtitle_only"`
	Subtitles           bool     `json:"subtitles"`
	AutoSubtitles       bool     `json:"auto_subtitles"`
	TranslatedSubtitles bool     `json:"translated_subtitles"`
	SubtitleLanguages   string   `json:"subtitle_languages"`
	SubtitleFormat      string   `json:"subtitle_format"`
	EmbedSubtitles      bool     `json:"embed_subtitles"`
	KeepSubtitles       bool     `json:"keep_subtitles"`
	Container           string   `json:"container"`
	AudioFormat         string   `json:"audio_format"`
	AudioBitrate        int      `json:"audio_bitrate"`
	VideoCodec          string   `json:"video_codec"`
	FormatIDs           string   `json:"format_ids"`
	FormatSort          string   `json:"format_sort"`
	MergeAudio          bool     `json:"merge_audio"`
	Thumbnail           bool     `json:"thumbnail"`
	EmbedThumbnail      bool     `json:"embed_thumbnail"`
	CropArtwork         bool     `json:"crop_artwork"`
	Metadata            bool     `json:"metadata"`
	Chapters            bool     `json:"chapters"`
	SplitChapters       bool     `json:"split_chapters"`
	ClipStart           *float64 `json:"clip_start"`
	ClipEnd             *float64 `json:"clip_end"`
	Sponsorblock        string   `json:"sponsorblock"`
	RateKbps            int      `json:"rate_kbps"`
	ConcurrentFragments int      `json:"concurrent_fragments"`
	Retries             int      `json:"retries"`
	ForceIPv4           bool     `json:"force_ipv4"`
	Proxy               string   `json:"proxy"`
	CookieProfile       string   `json:"cookie_profile"`
	UseCookies          bool     `json:"use_cookies"`
	RestrictFilenames   bool     `json:"restrict_filenames"`
	SkipExisting        bool     `json:"skip_existing"`
	FilenameTemplate    string   `json:"filename_template"`
	CustomArgs          string   `json:"custom_args"`
	Downloader          string   `json:"downloader"`
	FolderLayout        string   `json:"folder_layout"`
	Privacy             bool     `json:"privacy"`
}

var MediaHeights = []int{0, 144, 240, 360, 480, 720, 1080, 1440, 2160, 4320}

var MediaEnums = map[string][]string{
	"folder_layout":   {"source", "type", "date", "type-date"},
	"audio_quality":   {"best", "320", "256", "192", "128", "96", "64"},
	"subtitle_format": {"original", "srt", "vtt", "ass", "lrc"},
	"container":       {"mkv", "mp4", "webm"},
	"audio_format":    {"best", "m4a", "mp3", "opus", "flac", "wav", "aac", "vorbis"},
	"video_codec":     {"auto", "h264", "h265", "vp9", "av1"},
	"downloader":      {"native", "aria2c"},
}

var MediaSponsors = []string{
	"sponsor", "intro", "outro", "selfpromo", "preview", "filler", "interaction", "music_offtopic",
}

var controlChars = regexp.MustCompile(`[\x00-\x1f]`)
var subtitleLanguagesPattern = regexp.MustCompile(`^[A-Za-z0-9_.*,+\-]+$`)
var formatIDsPattern = regexp.MustCompile(`^(?:[A-Za-z0-9_.\-]+(?:\+[A-Za-z0-9_.\-]+){0,7})?$`)
var formatSortPattern = regexp.MustCompile(`^[A-Za-z0-9_+.:,~\-]*$`)
var cookieProfilePattern = regexp.MustCompile(`^(?:[a-f0-9]{32})?$`)
var filenameForbidden = regexp.MustCompile(`[/\\<>:"|?*]`)
var intArgsSwitches = map[string]bool{
	"--prefer-free-formats": true, "--force-keyframes-at-cuts": true,
	"--write-description": true, "--write-link": true, "--no-mtime": true,
}
var intArgsRanges = map[string][2]int{
	"--sleep-requests":     {0, 60},
	"--sleep-interval":     {0, 120},
	"--max-sleep-interval": {0, 120},
	"--socket-timeout":     {1, 180},
	"--buffer-size":        {1024, 10485760},
	"--http-chunk-size":    {0, 104857600},
}

func MediaTuning(value string) (map[string]any, error) {
	result := map[string]any{}
	trimmed := strings.TrimSpace(value)
	tokens := []string{}
	if trimmed != "" {
		tokens = strings.Fields(trimmed)
	}
	for index := 0; index < len(tokens); index++ {
		key := tokens[index]
		if intArgsSwitches[key] {
			result[key] = true
			continue
		}
		bounds, known := intArgsRanges[key]
		next := ""
		if index+1 < len(tokens) {
			index++
			next = tokens[index]
		}
		digits, _ := regexp.MatchString(`^\d+$`, next)
		if !known || next == "" || !digits {
			return nil, fmt.Errorf("此模板参数未接入或不是整数：%s", key)
		}
		number, _ := strconv.Atoi(next)
		if number < bounds[0] || number > bounds[1] {
			return nil, fmt.Errorf("模板参数超出范围：%s", key)
		}
		result[key] = number
	}
	return result, nil
}

func defaultOptions() NativeMediaOptions {
	return NativeMediaOptions{
		Height: 0, AudioQuality: "best", AudioOnly: false, SubtitleOnly: false,
		Subtitles: false, AutoSubtitles: false, TranslatedSubtitles: false,
		SubtitleLanguages: "zh.*,ai-zh.*,en.*,ai-en.*", SubtitleFormat: "srt",
		EmbedSubtitles: false, KeepSubtitles: true, Container: "mkv", AudioFormat: "m4a",
		AudioBitrate: 0, VideoCodec: "auto", FormatIDs: "", FormatSort: "",
		MergeAudio: false, Thumbnail: false, EmbedThumbnail: false, CropArtwork: false,
		Metadata: true, Chapters: true, SplitChapters: false, ClipStart: nil, ClipEnd: nil,
		Sponsorblock: "", RateKbps: 0, ConcurrentFragments: 1, Retries: 5,
		ForceIPv4: false, Proxy: "", CookieProfile: "", UseCookies: false,
		RestrictFilenames: false, SkipExisting: true,
		FilenameTemplate: "%(title).100B [%(id)s].%(ext)s", CustomArgs: "",
		Downloader: "native", FolderLayout: "source", Privacy: false,
	}
}

func inList(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func inEnum(value string, key string) bool { return inList(value, MediaEnums[key]) }

func validateMediaEnums(value NativeMediaOptions) error {
	if !inList(strconv.Itoa(value.Height), heightStrings()) {
		return fmt.Errorf("下载画质无效")
	}
	if !inEnum(value.AudioQuality, "audio_quality") {
		return fmt.Errorf("下载音质无效")
	}
	if !inEnum(value.SubtitleFormat, "subtitle_format") || !inEnum(value.Container, "container") ||
		!inEnum(value.AudioFormat, "audio_format") || !inEnum(value.VideoCodec, "video_codec") ||
		!inEnum(value.Downloader, "downloader") || !inEnum(value.FolderLayout, "folder_layout") {
		return fmt.Errorf("下载选项无效")
	}
	return nil
}

func validateMediaText(value NativeMediaOptions) error {
	if controlChars.MatchString(value.SubtitleLanguages + value.FormatIDs + value.FormatSort + value.Sponsorblock + value.Proxy + value.FilenameTemplate + value.CustomArgs) {
		return fmt.Errorf("下载文本不能包含控制字符")
	}
	if len(value.SubtitleLanguages) > 240 || !subtitleLanguagesPattern.MatchString(value.SubtitleLanguages) ||
		len(value.FormatIDs) > 300 || !formatIDsPattern.MatchString(value.FormatIDs) ||
		len(value.FormatSort) > 240 || !formatSortPattern.MatchString(value.FormatSort) ||
		len(value.CookieProfile) > 32 || !cookieProfilePattern.MatchString(value.CookieProfile) {
		return fmt.Errorf("下载选项无效")
	}
	if value.CookieProfile != "" && !value.UseCookies {
		return fmt.Errorf("选择登录档案时需启用使用登录档案")
	}
	return nil
}

func validateMediaClip(value NativeMediaOptions) error {
	if (value.ClipStart == nil) != (value.ClipEnd == nil) ||
		(value.ClipEnd != nil && value.ClipStart != nil && *value.ClipEnd <= *value.ClipStart) ||
		(value.ClipEnd != nil && (*value.ClipEnd < 0 || *value.ClipEnd > 86400)) ||
		(value.ClipStart != nil && (*value.ClipStart < 0 || *value.ClipStart > 86400)) {
		return fmt.Errorf("片段结束时间必须大于开始时间")
	}
	return nil
}

func validateMediaSponsor(value NativeMediaOptions) error {
	if value.Sponsorblock == "" {
		return nil
	}
	for _, sponsor := range strings.Split(value.Sponsorblock, ",") {
		if !inList(sponsor, MediaSponsors) {
			return fmt.Errorf("SponsorBlock 分类无效")
		}
	}
	if value.SubtitleOnly || value.SplitChapters {
		return fmt.Errorf("片段移除与仅字幕或章节拆分请分别建立任务")
	}
	return nil
}

func normalizeMediaOptions(value *NativeMediaOptions) error {
	if value.FilenameTemplate == "" || filenameForbidden.MatchString(value.FilenameTemplate) ||
		strings.Contains(value.FilenameTemplate, "..") ||
		!strings.HasSuffix(value.FilenameTemplate, ".%(ext)s") {
		return fmt.Errorf("文件名模板须为单层文件名，以 .%%(ext)s 结尾")
	}
	if value.TranslatedSubtitles {
		value.AutoSubtitles = true
	}
	if value.AutoSubtitles {
		value.Subtitles = true
	}
	if value.SubtitleOnly {
		if value.AudioOnly || value.EmbedSubtitles || value.ClipEnd != nil {
			return fmt.Errorf("仅字幕不能同时选纯音频、嵌入或裁剪")
		}
		value.Subtitles = true
	}
	if value.EmbedSubtitles {
		if value.AudioOnly || !value.Subtitles {
			return fmt.Errorf("嵌入字幕需选择视频及下载字幕")
		}
		value.Container = "mkv"
	}
	if value.SplitChapters && value.ClipEnd != nil {
		return fmt.Errorf("章节拆分和时间片段请分别建立任务")
	}
	if value.CropArtwork && !value.EmbedThumbnail {
		return fmt.Errorf("裁剪封面需启用嵌入封面")
	}
	if value.SubtitleOnly && value.EmbedThumbnail {
		return fmt.Errorf("仅字幕没有可嵌入封面的媒体，可单独下载封面")
	}
	return nil
}

func MediaOptions(input any) (NativeMediaOptions, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return NativeMediaOptions{}, fmt.Errorf("下载选项无效")
	}
	value := defaultOptions()
	if err := json.Unmarshal(raw, &value); err != nil {
		return NativeMediaOptions{}, fmt.Errorf("下载选项无效")
	}
	if err := validateMediaEnums(value); err != nil {
		return value, err
	}
	if err := validateMediaText(value); err != nil {
		return value, err
	}
	if err := validateMediaClip(value); err != nil {
		return value, err
	}
	if err := validateMediaSponsor(value); err != nil {
		return value, err
	}
	if value.Proxy != "" {
		if err := validateProxy(value.Proxy); err != nil {
			return value, err
		}
	}
	if err := normalizeMediaOptions(&value); err != nil {
		return value, err
	}
	if _, err := MediaTuning(value.CustomArgs); err != nil {
		return value, err
	}
	if _, err := MediaSort(value.FormatSort); err != nil {
		return value, err
	}
	return value, nil
}

func heightStrings() []string {
	result := []string{}
	for _, height := range MediaHeights {
		result = append(result, fmt.Sprintf("%d", height))
	}
	return result
}
