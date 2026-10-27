package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

const (
	errBiliUrlRequired        = "请输入完整的 B 站视频网址"
	errSubtitleContentInvalid = "字幕内容为空或格式错误"
)

type VideoEpisode struct {
	Page     int     `json:"page"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	URL      string  `json:"url"`
}

type VideoSeries struct {
	Title    string         `json:"title"`
	Episodes []VideoEpisode `json:"episodes"`
}

type FetchedSubtitle struct {
	Title   string `json:"title"`
	Page    int    `json:"page"`
	Source  string `json:"source"`
	Warning string `json:"warning"`
	SRT     string `json:"srt"`
	Count   int    `json:"count"`
}

type pageRow struct {
	Page     int     `json:"page"`
	Part     string  `json:"part"`
	Duration float64 `json:"duration"`
	CID      int64   `json:"cid"`
}

var subtitleAds = regexp.MustCompile(`(?i)^(ai-|auto)`)
var subtitleCN = regexp.MustCompile(`(?i)^(zh|ai-zh)`)
var leadingNumber = regexp.MustCompile(`^\d+[.、\s]+`)
var controlChars = regexp.MustCompile(`[\x00-\x1f]+`)

func biliAPIBase() string {
	if value := strings.TrimSpace(os.Getenv("ZYNOTE_BILIBILI_API")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "https://api.bilibili.com"
}

func biliGet(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	if client == nil {
		client = &http.Client{}
	}
	requestContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return nil, contract.Unavailable("无法读取 B 站视频分集")
	}
	request.Header.Set("Referer", "https://www.bilibili.com/")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, contract.Unavailable("无法读取 B 站视频分集")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, contract.Unavailable(fmt.Sprintf("B 站 %s 返回 HTTP %d", request.URL.Path, response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return nil, contract.Unavailable("字幕响应过大")
	}
	return body, nil
}

func videoIDOf(parsed *url.URL) (string, string, error) {
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", contract.InputInvalid(errBiliUrlRequired)
	}
	id := parts[len(parts)-1]
	if strings.HasPrefix(id, "BV") {
		return "bvid", id, nil
	}
	if strings.HasPrefix(id, "av") {
		return "aid", strings.TrimPrefix(id, "av"), nil
	}
	return "", "", contract.InputInvalid(errBiliUrlRequired)
}

func fetchPageList(ctx context.Context, client *http.Client, normalized string) ([]pageRow, error) {
	parsed, err := url.Parse(normalized)
	if err != nil {
		return nil, contract.InputInvalid(errBiliUrlRequired)
	}
	key, id, err := videoIDOf(parsed)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set(key, id)
	body, err := biliGet(ctx, client, biliAPIBase()+"/x/player/pagelist?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var reply struct {
		Code int       `json:"code"`
		Data []pageRow `json:"data"`
	}
	if json.Unmarshal(body, &reply) != nil || reply.Code != 0 || len(reply.Data) == 0 || len(reply.Data) > 1000 {
		return nil, contract.Unavailable("未能读取视频分集目录，请稍后重试")
	}
	for index, page := range reply.Data {
		if page.Page != index+1 || strings.TrimSpace(page.Part) == "" {
			return nil, contract.Unavailable("视频分集目录不完整，请重新读取")
		}
	}
	return reply.Data, nil
}

func FetchVideoSeries(ctx context.Context, client *http.Client, value string) (VideoSeries, error) {
	normalized, err := VideoURL(value)
	if err != nil {
		return VideoSeries{}, err
	}
	pages, err := fetchPageList(ctx, client, normalized)
	if err != nil {
		return VideoSeries{}, err
	}
	parsed, _ := url.Parse(normalized)
	title := leadingNumber.ReplaceAllString(strings.TrimSpace(pages[0].Part), "")
	title = strings.TrimSpace(strings.SplitN(title, "-", 2)[0])
	if title == "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		title = parts[len(parts)-1]
	}
	episodes := make([]VideoEpisode, 0, len(pages))
	for index, page := range pages {
		part, _ := url.Parse(normalized)
		part.RawQuery = url.Values{"p": []string{strconv.Itoa(index + 1)}}.Encode()
		episodes = append(episodes, VideoEpisode{
			Page: index + 1, Title: clipRunes(strings.TrimSpace(page.Part), 200),
			Duration: page.Duration, URL: part.String(),
		})
	}
	return VideoSeries{Title: clipRunes(title, 200), Episodes: episodes}, nil
}

func trustedSubtitleAddress(value string) (string, error) {
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil ||
		(parsed.Port() != "" && parsed.Port() != "443") ||
		(!strings.HasSuffix(parsed.Hostname(), "hdslb.com") && !strings.HasSuffix(parsed.Hostname(), "bilibili.com")) {
		return "", contract.Unavailable("字幕地址不受信任")
	}
	return parsed.String(), nil
}

type subtitleTrack struct {
	Lan         string `json:"lan"`
	LanDoc      string `json:"lan_doc"`
	SubtitleURL string `json:"subtitle_url"`
	AIType      int    `json:"ai_type"`
}

type subtitleCue struct {
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Content string  `json:"content"`
}

func FetchVideoSubtitles(ctx context.Context, client *http.Client, value string) (FetchedSubtitle, error) {
	normalized, err := VideoURL(value)
	if err != nil {
		return FetchedSubtitle{}, err
	}
	pages, err := fetchPageList(ctx, client, normalized)
	if err != nil {
		return FetchedSubtitle{}, err
	}
	parsed, _ := url.Parse(normalized)
	pageNumber := 1
	if raw := parsed.Query().Get("p"); raw != "" {
		pageNumber, _ = strconv.Atoi(raw)
	}
	var selected *pageRow
	for index := range pages {
		if pages[index].Page == pageNumber {
			selected = &pages[index]
			break
		}
	}
	if selected == nil || selected.CID <= 0 {
		return FetchedSubtitle{}, contract.InputInvalid("所选分集不存在")
	}
	key, id, _ := videoIDOf(parsed)
	query := url.Values{key: []string{id}, "cid": []string{strconv.FormatInt(selected.CID, 10)}}
	body, err := biliGet(ctx, client, biliAPIBase()+"/x/player/v2?"+query.Encode())
	if err != nil {
		return FetchedSubtitle{}, err
	}
	var detail struct {
		Code int `json:"code"`
		Data struct {
			Subtitle struct {
				Subtitles []subtitleTrack `json:"subtitles"`
			} `json:"subtitle"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &detail) != nil || detail.Code != 0 {
		return FetchedSubtitle{}, contract.Unavailable("无法读取此分集的字幕目录")
	}
	tracks := detail.Data.Subtitle.Subtitles
	if len(tracks) == 0 {
		return FetchedSubtitle{}, contract.InputInvalid("此分集没有站内字幕；可下载视频后在课程字幕功能中转写")
	}
	sort.SliceStable(tracks, func(left, right int) bool {
		leftCN := subtitleCN.MatchString(tracks[left].Lan)
		rightCN := subtitleCN.MatchString(tracks[right].Lan)
		if leftCN != rightCN {
			return leftCN
		}
		leftAuto := subtitleAds.MatchString(tracks[left].Lan) || tracks[left].AIType != 0
		rightAuto := subtitleAds.MatchString(tracks[right].Lan) || tracks[right].AIType != 0
		return !leftAuto && rightAuto
	})
	var lastErr error
	for _, track := range tracks {
		address, err := trustedSubtitleAddress(track.SubtitleURL)
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := biliGet(ctx, client, address)
		if err != nil {
			if ctx.Err() != nil {
				return FetchedSubtitle{}, ctx.Err()
			}
			lastErr = err
			continue
		}
		var payload struct {
			Body []subtitleCue `json:"body"`
		}
		if json.Unmarshal(raw, &payload) != nil || len(payload.Body) == 0 || len(payload.Body) > 20000 {
			lastErr = contract.Unavailable(errSubtitleContentInvalid)
			continue
		}
		cues := make([]subtitleCue, 0, len(payload.Body))
		for _, cue := range payload.Body {
			content := strings.TrimSpace(controlChars.ReplaceAllString(cue.Content, " "))
			if cue.From < 0 || cue.To <= cue.From || content == "" || len([]rune(content)) > 2000 ||
				math.IsNaN(cue.From) || math.IsNaN(cue.To) || math.IsInf(cue.From, 0) || math.IsInf(cue.To, 0) {
				lastErr = contract.Unavailable("字幕时间或文字异常")
				cues = nil
				break
			}
			cues = append(cues, subtitleCue{From: cue.From, To: cue.To, Content: content})
		}
		if cues == nil {
			continue
		}
		sort.SliceStable(cues, func(left, right int) bool { return cues[left].From < cues[right].From })
		if selected.Duration > 0 && cues[len(cues)-1].To > selected.Duration*1.25+30 {
			lastErr = contract.Unavailable("字幕时间超出视频，可能属于其他视频")
			continue
		}
		segments := []documents.Segment{}
		for index, cue := range cues {
			end := cue.To
			if index+1 < len(cues) && cues[index+1].From < end {
				end = cues[index+1].From
			}
			start := int64(cue.From*1000 + 0.5)
			stop := int64(end*1000 + 0.5)
			if stop > start {
				segments = append(segments, documents.Segment{Start: start, End: stop, Text: cue.Content})
			}
		}
		if len(segments) == 0 {
			lastErr = contract.Unavailable(errSubtitleContentInvalid)
			continue
		}
		srt := learning.ToSrt(segments)
		if _, err := documents.ParseSrt(srt); err != nil {
			lastErr = contract.Unavailable(errSubtitleContentInvalid)
			continue
		}
		warning := ""
		if subtitleAds.MatchString(track.Lan) || track.AIType != 0 {
			warning = "自动字幕可能有错，请核对后再作为课堂原文使用。"
		}
		source := track.LanDoc
		if source == "" {
			source = track.Lan
		}
		return FetchedSubtitle{
			Title: selected.Part, Page: pageNumber, Source: source, Warning: warning,
			SRT: srt, Count: len(segments),
		}, nil
	}
	if lastErr != nil {
		return FetchedSubtitle{}, lastErr
	}
	return FetchedSubtitle{}, contract.InputInvalid("此分集没有可用字幕")
}
