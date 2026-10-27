package media

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var subtitleLanguagePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type SubtitleCue struct {
	Start int64
	End   int64
	Text  string
}

type DownloadedSubtitle struct {
	Relative      string
	Language      string
	Automatic     bool
	EmbedRelative string
}

func quoteSubtitlePattern(pattern string) string {
	var builder strings.Builder
	for _, char := range pattern {
		switch char {
		case '.', '+', '?', '^', '$', '{', '}', '(', ')', '[', ']', '|', '\\':
			builder.WriteByte('\\')
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

func matchSubtitlePattern(pattern, language string) bool {
	value := strings.ReplaceAll(pattern, ".*", "\x00")
	value = quoteSubtitlePattern(value)
	value = strings.ReplaceAll(value, "*", ".*")
	value = strings.ReplaceAll(value, "\x00", ".*")
	expression, err := regexp.Compile("(?i)^" + value + "$")
	return err == nil && expression.MatchString(language)
}

func subtitlePatterns(patterns string) ([]string, []string) {
	include, exclude := []string{}, []string{}
	for _, token := range strings.Split(patterns, ",") {
		if strings.HasPrefix(token, "-") {
			exclude = append(exclude, strings.TrimPrefix(token, "-"))
			continue
		}
		include = append(include, token)
	}
	return include, exclude
}

func matchesAnySubtitlePattern(patterns []string, language string) bool {
	for _, pattern := range patterns {
		if matchSubtitlePattern(pattern, language) {
			return true
		}
	}
	return false
}

func MatchesSubtitleLanguage(language, patterns string) bool {
	include, exclude := subtitlePatterns(patterns)
	return matchesAnySubtitlePattern(include, language) && !matchesAnySubtitlePattern(exclude, language)
}

var (
	vttStampPattern  = regexp.MustCompile(`^(?:(\d{2,}):)?(\d{2}):(\d{2})\.(\d{3})$`)
	vttHeaderPattern = regexp.MustCompile(`^(?:WEBVTT|NOTE|STYLE|REGION)(?:\s|$)`)
	vttTimePattern   = regexp.MustCompile(`^(\S+)\s+-->\s+(\S+)(?:\s+.*)?$`)
	vttBlockPattern  = regexp.MustCompile(`\n\s*\n`)
)

func vttSeconds(stamp string) (float64, error) {
	match := vttStampPattern.FindStringSubmatch(stamp)
	if match == nil {
		return 0, fmt.Errorf("站点字幕时间戳无效")
	}
	hours, _ := strconv.Atoi(defaultString(match[1], "0"))
	minutes, _ := strconv.Atoi(match[2])
	secs, _ := strconv.Atoi(match[3])
	millis, _ := strconv.Atoi(match[4])
	if minutes > 59 || secs > 59 {
		return 0, fmt.Errorf("站点字幕时间戳无效")
	}
	return float64(hours*3600+minutes*60+secs) + float64(millis)/1000, nil
}

func vttCueBlock(block string) (SubtitleCue, bool, error) {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	if len(lines) == 0 || vttHeaderPattern.MatchString(lines[0]) {
		return SubtitleCue{}, false, nil
	}
	index := -1
	for position, line := range lines {
		if strings.Contains(line, "-->") {
			index = position
			break
		}
	}
	if index < 0 {
		return SubtitleCue{}, false, nil
	}
	times := vttTimePattern.FindStringSubmatch(lines[index])
	if times == nil {
		return SubtitleCue{}, false, fmt.Errorf("站点字幕时间戳无效")
	}
	from, err := vttSeconds(times[1])
	if err != nil {
		return SubtitleCue{}, false, err
	}
	to, err := vttSeconds(times[2])
	if err != nil {
		return SubtitleCue{}, false, err
	}
	content := html.UnescapeString(stripTags(strings.Join(lines[index+1:], " ")))
	content = strings.TrimSpace(content)
	if content == "" {
		return SubtitleCue{}, false, nil
	}
	return SubtitleCue{Start: int64(math.Round(from * 1000)), End: int64(math.Round(to * 1000)), Text: content}, true, nil
}

func NativeVttCues(text string) ([]SubtitleCue, error) {
	body := strings.ReplaceAll(strings.TrimPrefix(text, "\uFEFF"), "\r", "")
	if !regexp.MustCompile(`^WEBVTT(?:\s|$)`).MatchString(body) {
		return nil, fmt.Errorf("站点字幕不是有效 WebVTT")
	}
	cues := []SubtitleCue{}
	for _, block := range vttBlockPattern.Split(strings.TrimSpace(body), -1) {
		cue, ok, err := vttCueBlock(block)
		if err != nil {
			return nil, err
		}
		if ok {
			cues = append(cues, cue)
		}
	}
	if len(cues) == 0 || len(cues) > 20000 {
		return nil, fmt.Errorf("站点字幕时间或正文无效")
	}
	return cues, nil
}

var subtitleTagPattern = regexp.MustCompile(`<[^>]*>`)

func stripTags(value string) string { return subtitleTagPattern.ReplaceAllString(value, "") }

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func RetimeCues(cues []SubtitleCue, kept []MediaInterval) []SubtitleCue {
	output := []SubtitleCue{}
	offset := int64(0)
	for _, interval := range kept {
		start := int64(math.Round(interval.Start * 1000))
		end := int64(math.Round(interval.End * 1000))
		for _, cue := range cues {
			from := max64(start, cue.Start)
			to := min64(end, cue.End)
			if to <= from {
				continue
			}
			output = append(output, SubtitleCue{
				Start: offset + from - start, End: offset + to - start, Text: cue.Text,
			})
		}
		offset += end - start
	}
	sort.SliceStable(output, func(left, right int) bool { return output[left].Start < output[right].Start })
	return output
}

func selectSubtitleTracks(source Source, options NativeMediaOptions) []SubtitleTrack {
	tracks := []SubtitleTrack{}
	for _, track := range source.SubtitleTracks {
		if !MatchesSubtitleLanguage(track.Language, options.SubtitleLanguages) {
			continue
		}
		if track.Automatic && !options.AutoSubtitles {
			continue
		}
		tracks = append(tracks, track)
	}
	return tracks
}

func filterKeptCues(cues []SubtitleCue, kept []MediaInterval) ([]SubtitleCue, error) {
	if kept == nil {
		return cues, nil
	}
	cues = RetimeCues(cues, kept)
	filtered := []SubtitleCue{}
	for _, cue := range cues {
		if cue.End > cue.Start {
			filtered = append(filtered, cue)
		}
	}
	if len(filtered) == 0 || len(filtered) > 20000 {
		return nil, fmt.Errorf("片段移除后的字幕为空或条目过多，请调整分类")
	}
	return filtered, nil
}

func saveSubtitleFiles(directory, language string, selected subtitleFormat, srt string, embedSubtitles bool) (string, string, error) {
	relative := fmt.Sprintf("subtitle-%s.%s", language, selected.Extension)
	if err := saveMediaFile(directory, relative, []byte(selected.Text)); err != nil {
		return "", "", err
	}
	embed := ""
	if embedSubtitles {
		embed = fmt.Sprintf("embed-%s.srt", language)
		if err := saveMediaFile(directory, embed, []byte(srt)); err != nil {
			return "", "", err
		}
	}
	return relative, embed, nil
}

func downloadSubtitleTrack(ctx context.Context, track SubtitleTrack, headers map[string]string,
	source Source, options NativeMediaOptions, directory string, fetcher AuthFetcher,
	kept []MediaInterval, languages map[string]bool) (DownloadedSubtitle, error) {
	if !subtitleLanguagePattern.MatchString(track.Language) || languages[track.Language] {
		return DownloadedSubtitle{}, fmt.Errorf("字幕语言编号无效或重复")
	}
	languages[track.Language] = true
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	response, err := fetcher(track.URL, headers, child)
	if err != nil {
		cancel()
		return DownloadedSubtitle{}, err
	}
	body, readErr := readBoundedBody(response, 4_000_000)
	cancel()
	if readErr != nil {
		return DownloadedSubtitle{}, readErr
	}
	cues, err := parseSubtitleCues(track.Format, body)
	if err != nil {
		return DownloadedSubtitle{}, err
	}
	cues, err = normalizeCues(cues, source.Catalog.Duration)
	if err != nil {
		return DownloadedSubtitle{}, err
	}
	cues, err = filterKeptCues(cues, kept)
	if err != nil {
		return DownloadedSubtitle{}, err
	}
	formats, srt, err := subtitleFormats(cues)
	if err != nil {
		return DownloadedSubtitle{}, err
	}
	format, ok := formats[options.SubtitleFormat]
	if !ok {
		return DownloadedSubtitle{}, fmt.Errorf("字幕格式无效")
	}
	relative, embed, err := saveSubtitleFiles(directory, track.Language, format, srt, options.EmbedSubtitles)
	if err != nil {
		return DownloadedSubtitle{}, err
	}
	return DownloadedSubtitle{
		Relative: relative, Language: track.Language, Automatic: track.Automatic, EmbedRelative: embed,
	}, nil
}

func DownloadSubtitles(source Source, options NativeMediaOptions, directory string, fetcher AuthFetcher, ctx context.Context, kept []MediaInterval) ([]DownloadedSubtitle, error) {
	tracks := selectSubtitleTracks(source, options)
	if len(tracks) == 0 {
		return nil, fmt.Errorf("所选语言没有可下载字幕；原资料保留，可使用本地转写")
	}
	files := []DownloadedSubtitle{}
	languages := map[string]bool{}
	headers := source.DownloadHeaders
	if headers == nil {
		headers = map[string]string{"Referer": "https://www.bilibili.com/"}
	}
	for _, track := range tracks {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		file, err := downloadSubtitleTrack(ctx, track, headers, source, options, directory, fetcher, kept, languages)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

type youtubeJSON3Event struct {
	StartMs  float64  `json:"tStartMs"`
	Duration *float64 `json:"dDurationMs"`
	Segments []struct {
		UTF8 string `json:"utf8"`
	} `json:"segs"`
}

func parseYoutubeJSON3(body []byte) ([]SubtitleCue, error) {
	var payload struct {
		Events []youtubeJSON3Event `json:"events"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Events) > 20000 {
		return nil, fmt.Errorf("站点字幕格式无效")
	}
	cues := []SubtitleCue{}
	for _, event := range payload.Events {
		if event.Duration == nil || *event.Duration <= 0 {
			continue
		}
		text := ""
		meaningful := false
		for _, segment := range event.Segments {
			text += segment.UTF8
			if strings.TrimSpace(segment.UTF8) != "" {
				meaningful = true
			}
		}
		if !meaningful {
			continue
		}
		cues = append(cues, SubtitleCue{
			Start: int64(math.Round(event.StartMs)), End: int64(math.Round(event.StartMs + *event.Duration)), Text: text,
		})
	}
	if len(cues) == 0 {
		return nil, fmt.Errorf("站点字幕格式无效")
	}
	return cues, nil
}

func parseBilibiliJSON(body []byte) ([]SubtitleCue, error) {
	var payload struct {
		Body []struct {
			From    float64 `json:"from"`
			To      float64 `json:"to"`
			Content string  `json:"content"`
		} `json:"body"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Body) == 0 || len(payload.Body) > 20000 {
		return nil, fmt.Errorf("站点字幕格式无效")
	}
	cues := []SubtitleCue{}
	for _, row := range payload.Body {
		cues = append(cues, SubtitleCue{
			Start: int64(math.Round(row.From * 1000)), End: int64(math.Round(row.To * 1000)), Text: row.Content,
		})
	}
	return cues, nil
}

func parseSubtitleCues(format string, body []byte) ([]SubtitleCue, error) {
	switch format {
	case "webvtt":
		return NativeVttCues(string(body))
	case "youtube-json3":
		return parseYoutubeJSON3(body)
	default:
		return parseBilibiliJSON(body)
	}
}

func normalizeCues(cues []SubtitleCue, duration *float64) ([]SubtitleCue, error) {
	sort.SliceStable(cues, func(left, right int) bool { return cues[left].Start < cues[right].Start })
	result := []SubtitleCue{}
	for _, cue := range cues {
		text := strings.TrimSpace(strings.Map(func(char rune) rune {
			if char < 0x20 {
				return ' '
			}
			return char
		}, cue.Text))
		if cue.End <= cue.Start || text == "" || (duration != nil && float64(cue.End)/1000 > *duration*1.25+30) {
			return nil, fmt.Errorf("字幕时间或正文无效")
		}
		result = append(result, SubtitleCue{Start: cue.Start, End: cue.End, Text: text})
	}
	return result, nil
}

type subtitleFormat struct {
	Extension string
	Text      string
}

func originalSubtitleJSON(cues []SubtitleCue) (string, error) {
	rows := []map[string]any{}
	for _, cue := range cues {
		rows = append(rows, map[string]any{
			"from": float64(cue.Start) / 1000, "to": float64(cue.End) / 1000, "content": cue.Text,
		})
	}
	serialized, err := json.Marshal(map[string]any{"body": rows})
	if err != nil {
		return "", err
	}
	return string(serialized), nil
}

func vttClock(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}

func assClock(ms int64) string {
	return fmt.Sprintf("%d:%02d:%02d.%02d", ms/3600000, (ms/60000)%60, (ms/1000)%60, (ms%1000)/10)
}

func formatVTT(cues []SubtitleCue) string {
	blocks := []string{}
	for _, cue := range cues {
		blocks = append(blocks, vttClock(cue.Start)+" --> "+vttClock(cue.End)+"\n"+strings.ReplaceAll(cue.Text, "-->", "→")+"\n")
	}
	return "WEBVTT\n\n" + strings.Join(blocks, "\n")
}

func formatLRC(cues []SubtitleCue) string {
	lines := []string{}
	for _, cue := range cues {
		lines = append(lines, fmt.Sprintf("[%02d:%02d.%02d]%s",
			cue.Start/60000, (cue.Start/1000)%60, (cue.Start%1000)/10, cue.Text))
	}
	return strings.Join(lines, "\n")
}

func formatASS(cues []SubtitleCue) string {
	const header = "[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Microsoft YaHei,44,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,30,30,30,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
	lines := []string{}
	for _, cue := range cues {
		clean := strings.Map(func(char rune) rune {
			if char == '{' || char == '}' || char == '\\' {
				return -1
			}
			return char
		}, cue.Text)
		lines = append(lines, fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s", assClock(cue.Start), assClock(cue.End), clean))
	}
	return header + strings.Join(lines, "\n")
}

func subtitleFormats(cues []SubtitleCue) (map[string]subtitleFormat, string, error) {
	original, err := originalSubtitleJSON(cues)
	if err != nil {
		return nil, "", err
	}
	srt := formatSRT(cues)
	return map[string]subtitleFormat{
		"original": {Extension: "json", Text: original},
		"srt":      {Extension: "srt", Text: srt},
		"vtt":      {Extension: "vtt", Text: formatVTT(cues)},
		"lrc":      {Extension: "lrc", Text: formatLRC(cues)},
		"ass":      {Extension: "ass", Text: formatASS(cues)},
	}, srt, nil
}

func formatSRT(cues []SubtitleCue) string {
	blocks := []string{}
	for index, cue := range cues {
		blocks = append(blocks, fmt.Sprintf("%d\n%s --> %s\n%s",
			index+1, srtClock(cue.Start), srtClock(cue.End), cue.Text))
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func srtClock(ms int64) string {
	hours := ms / 3600000
	minutes := (ms / 60000) % 60
	seconds := (ms / 1000) % 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, ms%1000)
}

func readBoundedBody(response *http.Response, limit int64) ([]byte, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("课件辅助文件下载失败（HTTP %d）", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("课件辅助文件超过大小上限")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("课件辅助文件超过大小上限")
	}
	return body, nil
}

func saveMediaFile(directory, relative string, body []byte) error {
	path := filepath.Join(directory, relative)
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(body) {
			return nil
		}
		return fmt.Errorf("辅助成果已存在且内容不同，保留原文件")
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(body)
	return err
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
