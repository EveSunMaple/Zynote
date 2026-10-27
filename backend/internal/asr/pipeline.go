package asr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

const modelName = "whisper-small-onnx-q8"

type Cue struct {
	From    float64 `json:"from"`
	To      float64 `json:"to"`
	Content string  `json:"content"`
}

type CallFunc func(ctx context.Context, method string, params any, timeoutMS int) (json.RawMessage, error)

type Pipeline struct {
	Resources           string
	DataRoot            string
	Platform            string
	AllowSystemFallback bool
	Call                CallFunc
	Progress            func(string)
}

type Result struct {
	SRT       string
	Count     int
	Directory string
}

func (result Result) Cleanup() error {
	root, err := filepath.Abs(filepath.Dir(result.Directory))
	if err != nil {
		return fmt.Errorf("字幕暂存目录超出清理范围")
	}
	directory, err := filepath.Abs(result.Directory)
	if err != nil {
		return fmt.Errorf("字幕暂存目录超出清理范围")
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("字幕暂存目录超出清理范围")
	}
	return os.RemoveAll(directory)
}

func cleanNativeError(err error) string {
	message := err.Error()
	index := strings.Index(message, ": ")
	if index > 0 && message[:index] == strings.ToUpper(message[:index]) {
		return message[index+2:]
	}
	return message
}

func (pipeline Pipeline) report(message string) {
	if pipeline.Progress != nil {
		pipeline.Progress(message)
	}
}

func validateASRInput(pipeline Pipeline, video string) (os.FileInfo, error) {
	if pipeline.Call == nil {
		return nil, fmt.Errorf("本地字幕转写不可用")
	}
	if !filepath.IsAbs(pipeline.Resources) || !filepath.IsAbs(pipeline.DataRoot) {
		return nil, fmt.Errorf("本地字幕模型或视频组件未安装，请在「运行组件」中安装同版本组件")
	}
	if _, err := os.Stat(filepath.Join(pipeline.Resources, "asr", "onnx-small", "acquisition.json")); err != nil {
		return nil, fmt.Errorf("本地字幕模型或视频组件未安装，请在「运行组件」中安装同版本组件")
	}
	before, err := os.Stat(video)
	if err != nil || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("视频文件不可读")
	}
	return before, nil
}

func writeASRRecovery(path, video string, before os.FileInfo, state string, fields map[string]any) {
	payload := map[string]any{
		"source": video, "size": before.Size(), "modified": float64(before.ModTime().UnixNano()) / 1e6,
		"state": state, "model": modelName,
	}
	for key, value := range fields {
		payload[key] = value
	}
	if encoded, err := json.Marshal(payload); err == nil {
		_ = os.WriteFile(path, encoded, 0o644)
	}
}

func decodeASRAudio(ctx context.Context, pipeline Pipeline, video, pcm string) error {
	_, err := media.MediaCommand(media.MediaCommandInput{
		Resources: pipeline.Resources,
		Tool:      "ffmpeg",
		Args: []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-i", video,
			"-vn", "-ac", "1", "-ar", "16000", "-f", "f32le", "-n", pcm,
		},
		Ctx:                 ctx,
		AllowSystemFallback: pipeline.AllowSystemFallback,
		Platform:            pipeline.Platform,
	})
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return fmt.Errorf("字幕转写已取消，原视频与恢复记录保留")
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "无法启动"):
		return fmt.Errorf("视频处理组件无法启动")
	case strings.Contains(message, "未找到媒体处理组件"), strings.Contains(message, "不可执行"), strings.Contains(message, "媒体执行"):
		return fmt.Errorf("本地字幕模型或视频组件未安装，请在「运行组件」中安装同版本组件")
	}
	return fmt.Errorf("完整音频读取失败，原视频保留")
}

func parseASRReply(raw []byte) (float64, []Cue, error) {
	var reply struct {
		Duration float64 `json:"duration"`
		Cues     []Cue   `json:"cues"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return 0, nil, fmt.Errorf("字幕结果无法读取")
	}
	if !isFinite(reply.Duration) || reply.Duration <= 0 || len(reply.Cues) > 100000 {
		return 0, nil, fmt.Errorf("字幕结果无法读取")
	}
	cues := make([]Cue, 0, len(reply.Cues))
	for _, cue := range reply.Cues {
		content := strings.TrimSpace(cue.Content)
		if content == "" || !isFinite(cue.From) || !isFinite(cue.To) ||
			cue.From < 0 || cue.To <= cue.From || cue.To > reply.Duration+0.1 {
			return 0, nil, fmt.Errorf("字幕结果无法读取")
		}
		cues = append(cues, Cue{From: cue.From, To: cue.To, Content: content})
	}
	return reply.Duration, cues, nil
}

func (pipeline Pipeline) Transcribe(ctx context.Context, video, language string) (Result, error) {
	before, err := validateASRInput(pipeline, video)
	if err != nil {
		return Result{}, err
	}
	root := filepath.Join(pipeline.DataRoot, "asr-tasks")
	directory := filepath.Join(root, ids.New())
	pcm := filepath.Join(directory, "audio.f32")
	recovery := filepath.Join(directory, "recovery.json")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Result{}, fmt.Errorf("字幕暂存目录不可用")
	}
	writeASRRecovery(recovery, video, before, "decoding", nil)
	pipeline.report("正在读取完整视频音频")
	if err := decodeASRAudio(ctx, pipeline, video, pcm); err != nil {
		return Result{}, err
	}
	if info, statErr := os.Stat(pcm); statErr != nil || info.Size() == 0 || info.Size()%4 != 0 {
		return Result{}, fmt.Errorf("完整音频读取失败，原视频保留")
	}
	writeASRRecovery(recovery, video, before, "transcribing", nil)
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("字幕转写已取消，原视频与恢复记录保留")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = "zh"
	}
	pipeline.report("正在识别语音范围并保留原始时间点")
	raw, err := pipeline.Call(ctx, "asr.transcribe", map[string]any{"path": pcm, "language": language}, 6*60*60*1000)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("字幕转写已取消，原视频与恢复记录保留")
		}
		message := cleanNativeError(err)
		if strings.Contains(message, "原生助手已退出") || strings.Contains(message, "请求超时") {
			return Result{}, fmt.Errorf("转写工作进程中断，原视频与恢复记录保留")
		}
		return Result{}, fmt.Errorf("%s", message)
	}
	_, cues, err := parseASRReply(raw)
	if err != nil {
		return Result{}, err
	}
	after, err := os.Stat(video)
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return Result{}, fmt.Errorf("转写期间原视频改变，请重新选择")
	}
	segments := Segments(cues)
	if len(segments) == 0 {
		return Result{}, fmt.Errorf("未识别到完整语音，原视频保留")
	}
	srt := learning.ToSrt(segments)
	if _, err := documents.ParseSrt(srt); err != nil {
		return Result{}, fmt.Errorf("字幕结果无法读取")
	}
	if err := os.WriteFile(filepath.Join(directory, "result.srt"), []byte(srt), 0o644); err != nil {
		return Result{}, fmt.Errorf("字幕暂存结果写入失败")
	}
	writeASRRecovery(recovery, video, before, "ready-to-save", map[string]any{"count": len(segments)})
	return Result{SRT: srt, Count: len(segments), Directory: directory}, nil
}

func Segments(cues []Cue) []documents.Segment {
	sorted := append([]Cue(nil), cues...)
	sort.SliceStable(sorted, func(left, right int) bool {
		return sorted[left].From < sorted[right].From
	})
	segments := []documents.Segment{}
	for index, cue := range sorted {
		end := cue.To
		if index+1 < len(sorted) && sorted[index+1].From < end {
			end = sorted[index+1].From
		}
		start := int64(math.Round(cue.From * 1000))
		stop := int64(math.Round(end * 1000))
		if stop <= start {
			continue
		}
		segments = append(segments, documents.Segment{Start: start, End: stop, Text: cue.Content})
	}
	return segments
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
