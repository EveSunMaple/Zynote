package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var sponsorSourcePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var sponsorHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const (
	sponsorAttribution = "SponsorBlock community / CC BY-NC-SA 4.0"
	sponsorSource      = "https://sponsor.ajay.app"
	sponsorLicense     = "https://creativecommons.org/licenses/by-nc-sa/4.0/"
)

type SponsorSegment struct {
	Segment       [2]float64 `json:"segment"`
	Category      string     `json:"category"`
	ActionType    string     `json:"actionType"`
	VideoDuration float64    `json:"videoDuration"`
	UUID          string     `json:"UUID"`
}

type SponsorReceipt struct {
	Version     int              `json:"version"`
	SourceID    string           `json:"sourceId"`
	Duration    float64          `json:"duration"`
	Categories  []string         `json:"categories"`
	Segments    []SponsorSegment `json:"segments"`
	SHA256      string           `json:"sha256"`
	Attribution string           `json:"attribution"`
	Source      string           `json:"source"`
	License     string           `json:"license"`
}

type SponsorPlan struct {
	Removed  []MediaInterval
	Kept     []MediaInterval
	Duration float64
	Relative string
	Warning  string
}

func sponsorReceiptHash(sourceID string, duration float64, categories []string, segments []SponsorSegment) string {
	encoded, _ := json.Marshal([]any{sourceID, duration, categories, segments})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sponsorDurationWarning(row SponsorSegment, duration float64) bool {
	return (row.VideoDuration > 0 && math.Abs(row.VideoDuration-duration) > 1) ||
		row.Segment[1] > duration || row.Segment[1] <= row.Segment[0]
}

func sponsorCategories(options NativeMediaOptions) ([]string, map[string]bool, error) {
	categorySet := map[string]bool{}
	for _, value := range strings.Split(options.Sponsorblock, ",") {
		categorySet[value] = true
	}
	categories := []string{}
	for value := range categorySet {
		categories = append(categories, value)
	}
	sort.Strings(categories)
	if len(categories) == 0 {
		return nil, nil, fmt.Errorf("片段移除分类无效")
	}
	for _, category := range categories {
		if !inList(category, MediaSponsors) {
			return nil, nil, fmt.Errorf("片段移除分类无效")
		}
	}
	return categories, categorySet, nil
}

func readSponsorReceipt(path string, source Source, duration float64, categories []string) ([]SponsorSegment, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1_000_000 {
		return nil, false, fmt.Errorf("片段移除恢复记录无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var receipt SponsorReceipt
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, false, fmt.Errorf("片段移除恢复记录无效")
	}
	if receipt.SourceID != source.SourceID || receipt.Duration != duration ||
		!sameStrings(receipt.Categories, categories) ||
		receipt.SHA256 != sponsorReceiptHash(receipt.SourceID, receipt.Duration, receipt.Categories, receipt.Segments) {
		return nil, false, fmt.Errorf("片段移除恢复记录已改变，原文件保留")
	}
	return receipt.Segments, true, nil
}

func parseSponsorRows(data []byte, source Source, categorySet map[string]bool) ([]SponsorSegment, error) {
	var rows []struct {
		VideoID  string `json:"videoID"`
		Segments []any  `json:"segments"`
	}
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) > 1000 {
		return nil, fmt.Errorf("片段来源响应格式异常")
	}
	matched := []any{}
	matchedCount := 0
	for _, row := range rows {
		if row.VideoID != source.SourceID {
			continue
		}
		matchedCount++
		matched = row.Segments
	}
	if matchedCount > 1 {
		return nil, fmt.Errorf("片段来源身份重复，未修改视频")
	}
	segments := []SponsorSegment{}
	for _, item := range matched {
		parsed, ok := parseSponsorSegment(item)
		if !ok || !categorySet[parsed.Category] || parsed.ActionType != "skip" {
			continue
		}
		segments = append(segments, parsed)
	}
	return segments, nil
}

func fetchSponsorSegments(ctx context.Context, fetcher AuthFetcher, source Source, categories []string, categorySet map[string]bool) ([]SponsorSegment, error) {
	sum := sha256.Sum256([]byte(source.SourceID))
	query := url.Values{}
	encodedCategories, _ := json.Marshal(categories)
	query.Set("categories", string(encodedCategories))
	query.Set("actionTypes", `["skip"]`)
	query.Set("service", "YouTube")
	target := "https://sponsor.ajay.app/api/skipSegments/" + hex.EncodeToString(sum[:])[:4] + "?" + query.Encode()
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	response, err := fetcher(target, map[string]string{"Accept": "application/json"}, child)
	if err != nil {
		cancel()
		return nil, err
	}
	if response.StatusCode == 404 {
		_ = response.Body.Close()
		cancel()
		return []SponsorSegment{}, nil
	}
	data, readErr := readBoundedBody(response, 1_000_000)
	cancel()
	if readErr != nil {
		return nil, readErr
	}
	return parseSponsorRows(data, source, categorySet)
}

func writeSponsorReceipt(path string, source Source, duration float64, categories []string, segments []SponsorSegment) error {
	receipt := SponsorReceipt{
		Version: 1, SourceID: source.SourceID, Duration: duration, Categories: categories,
		Segments: segments, SHA256: sponsorReceiptHash(source.SourceID, duration, categories, segments),
		Attribution: sponsorAttribution, Source: sponsorSource, License: sponsorLicense,
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func SponsorBlockPlan(source Source, options NativeMediaOptions, directory string, fetcher AuthFetcher, ctx context.Context) (*SponsorPlan, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	if source.Platform != "youtube" || !sponsorSourcePattern.MatchString(source.SourceID) {
		return nil, fmt.Errorf("片段移除目前仅支持 YouTube；其它平台请使用时间片段")
	}
	categories, categorySet, err := sponsorCategories(options)
	if err != nil {
		return nil, err
	}
	duration := 0.0
	if source.Catalog.Duration != nil {
		duration = *source.Catalog.Duration
	}
	if duration <= 0 || !finite(duration) {
		return nil, fmt.Errorf("缺少完整视频时长，不能核验片段位置")
	}
	relative := "sponsorblock-plan.json"
	path := filepath.Join(directory, relative)
	segments, cached, err := readSponsorReceipt(path, source, duration, categories)
	if err != nil {
		return nil, err
	}
	if !cached {
		segments, err = fetchSponsorSegments(ctx, fetcher, source, categories, categorySet)
		if err != nil {
			return nil, err
		}
		if len(segments) > 200 {
			return nil, fmt.Errorf("片段来源响应格式异常")
		}
	}
	for _, row := range segments {
		if sponsorDurationWarning(row, duration) {
			return nil, fmt.Errorf("片段标注与当前视频时长不符，未执行裁剪")
		}
	}
	if !cached {
		if err := writeSponsorReceipt(path, source, duration, categories, segments); err != nil {
			return nil, err
		}
	}
	removed := []MediaInterval{}
	for _, row := range segments {
		removed = append(removed, MediaInterval{Start: row.Segment[0], End: row.Segment[1]})
	}
	var clip *MediaInterval
	if options.ClipStart != nil && options.ClipEnd != nil {
		clip = &MediaInterval{Start: *options.ClipStart, End: *options.ClipEnd}
	}
	timeline, err := RemovalTimeline(duration, removed, clip)
	if err != nil {
		return nil, err
	}
	warning := "所选范围没有匹配的社区片段标注，未额外移除内容"
	if len(timeline.Removed) > 0 {
		windowStart, windowEnd := 0.0, duration
		if clip != nil {
			windowStart, windowEnd = clip.Start, clip.End
		}
		seconds := math.Round((windowEnd-windowStart-timeline.Duration)*100) / 100
		warning = fmt.Sprintf("已按所选分类移除 %d 段，共 %s 秒；标注来自 SponsorBlock 社区",
			len(timeline.Removed), strconv.FormatFloat(seconds, 'f', -1, 64))
	}
	return &SponsorPlan{
		Removed: timeline.Removed, Kept: timeline.Kept, Duration: timeline.Duration,
		Relative: relative, Warning: warning,
	}, nil
}

func parseSponsorSegment(value any) (SponsorSegment, bool) {
	row, ok := value.(map[string]any)
	if !ok {
		return SponsorSegment{}, false
	}
	interval, ok := row["segment"].([]any)
	if !ok || len(interval) != 2 {
		return SponsorSegment{}, false
	}
	start, startOK := interval[0].(float64)
	end, endOK := interval[1].(float64)
	category, categoryOK := row["category"].(string)
	action, actionOK := row["actionType"].(string)
	uuid, uuidOK := row["UUID"].(string)
	videoDuration, durationOK := row["videoDuration"].(float64)
	if !startOK || !endOK || !categoryOK || !actionOK || !uuidOK || !durationOK ||
		!finite(start) || !finite(end) || start < 0 || end <= start || len([]rune(category)) > 50 ||
		len([]rune(action)) > 20 || len([]rune(uuid)) > 100 || !finite(videoDuration) || videoDuration < 0 {
		return SponsorSegment{}, false
	}
	return SponsorSegment{
		Segment: [2]float64{start, end}, Category: category, ActionType: action,
		VideoDuration: videoDuration, UUID: uuid,
	}, true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
