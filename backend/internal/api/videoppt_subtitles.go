package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

const msgSubtitleParamsInvalid = "字幕参数无效"

var videoSubtitleFormat = regexp.MustCompile(`^(txt|md|vtt)$`)
var subtitleTimePattern = regexp.MustCompile(`--\s*>`)

func subtitleClock(milliseconds int64, millis bool) string {
	if milliseconds < 0 {
		milliseconds = 0
	}
	seconds := milliseconds / 1000
	value := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, (seconds/60)%60, seconds%60)
	if millis {
		value += fmt.Sprintf(".%03d", milliseconds%1000)
	}
	return value
}

func subtitleText(raw, format string) (string, error) {
	segments, err := documents.ParseSrt(raw)
	if err != nil {
		return "", contract.InputInvalid(err.Error())
	}
	if len(segments) == 0 {
		return "", contract.InputInvalid("没有可导出的字幕")
	}
	if format == "vtt" {
		lines := []string{}
		for index, segment := range segments {
			text := subtitleTimePattern.ReplaceAllString(segment.Text, "→")
			lines = append(lines, fmt.Sprintf("%d\n%s --> %s\n%s", index+1,
				subtitleClock(segment.Start, true), subtitleClock(segment.End, true), text))
		}
		return "WEBVTT\n\n" + strings.Join(lines, "\n\n") + "\n", nil
	}
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		lines = append(lines, "["+subtitleClock(segment.Start, false)+"] "+segment.Text)
	}
	separator := "\n"
	prefix := ""
	if format == "md" {
		prefix = "# 视频字幕\n\n"
		separator = "\n\n"
	}
	return prefix + strings.Join(lines, separator) + "\n", nil
}

func (inv *Invoker) subtitleCourseName(state *contract.State, courseID string) (string, error) {
	if courseID == "" {
		return "", nil
	}
	if !config.IsUUID(courseID) {
		return "", contract.InputInvalid("课程标识无效")
	}
	return inv.courseName(state, courseID)
}

func (inv *Invoker) handleVideoSubtitleImport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid(msgSubtitleParamsInvalid)
	}
	course, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", course)
	if err != nil {
		return nil, err
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title": "载入已保存的字幕", "extensions": []string{"srt"}, "defaultPath": directory,
	}, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	info, err := os.Lstat(*picked.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4000000 {
		return nil, contract.InputInvalid("字幕文件过大")
	}
	body, err := os.ReadFile(*picked.Path)
	if err != nil {
		return nil, contract.InputInvalid("字幕文件不可读")
	}
	text := string(body)
	segments, err := documents.ParseSrt(text)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	title := strings.TrimSuffix(filepath.Base(*picked.Path), filepath.Ext(*picked.Path))
	return map[string]any{
		"title": title, "page": 1, "source": "已有 SRT 字幕",
		"warning": "原文按文件内容载入，未自动校正。",
		"srt":     text, "count": len(segments), "savedPath": *picked.Path,
	}, nil
}

func (inv *Invoker) handleVideoSubtitleSave(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL      string `json:"url"`
		SRT      string `json:"srt"`
		Title    string `json:"title"`
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.SRT) < 1 || len(input.SRT) > 4000000 ||
		len([]rune(input.Title)) < 1 || len([]rune(input.Title)) > 200 {
		return nil, contract.InputInvalid(msgSubtitleParamsInvalid)
	}
	normalized, err := bilibili.VideoURL(input.URL)
	if err != nil {
		return nil, err
	}
	course, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	return saveVideoSubtitleFile(state, normalized, input.Title, input.SRT, course)
}

func (inv *Invoker) handleVideoSubtitleExport(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Format   string `json:"format"`
		SRT      string `json:"srt"`
		Title    string `json:"title"`
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !videoSubtitleFormat.MatchString(input.Format) ||
		len(input.SRT) < 1 || len(input.SRT) > 4000000 ||
		len([]rune(input.Title)) < 1 || len([]rune(input.Title)) > 200 {
		return nil, contract.InputInvalid(msgSubtitleParamsInvalid)
	}
	text, err := subtitleText(input.SRT, input.Format)
	if err != nil {
		return nil, err
	}
	course, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", course)
	if err != nil {
		return nil, err
	}
	name := library.FolderName(input.Title) + "-字幕." + input.Format
	path, err := library.ReserveFile(directory, name)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		_ = os.Remove(path)
		return nil, contract.Unavailable("字幕导出失败")
	}
	check, err := os.ReadFile(path)
	if err != nil || string(check) != text {
		_ = os.Remove(path)
		return nil, contract.Unavailable("字幕导出校验失败")
	}
	return path, nil
}

func saveVideoSubtitleFile(state *contract.State, normalizedURL, title, srt, courseName string) (string, error) {
	if _, err := documents.ParseSrt(srt); err != nil {
		return "", contract.InputInvalid(err.Error())
	}
	parsed, _ := url.Parse(normalizedURL)
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	videoID := ""
	if len(parts) > 0 {
		videoID = parts[len(parts)-1]
	}
	page := parsed.Query().Get("p")
	if page == "" {
		page = "1"
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return "", err
	}
	name := videoID + "-P" + page + "-" + library.FolderName(title) + ".srt"
	path, err := library.ReserveFile(directory, name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(srt), 0o644); err != nil {
		_ = os.Remove(path)
		return "", contract.Unavailable("字幕保存失败")
	}
	check, err := os.ReadFile(path)
	if err != nil || string(check) != srt {
		_ = os.Remove(path)
		return "", contract.Unavailable("字幕保存校验失败")
	}
	return path, nil
}
