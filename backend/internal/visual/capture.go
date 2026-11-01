package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/framecandidate"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

const (
	errSourceUnavailable = "所选源文件不可用"
	errFramesDirInvalid  = "画面暂存目录无效"
	errImageUnreadable   = "图片无法读取"
	errNoVideo           = "源文件没有视频画面"
	errFrameUnreadable   = "截取的画面无法读取"
	ffmpegNoStdin        = "-nostdin"
	ffmpegLogLevel       = "-loglevel"
	ffmpegHideBanner     = "-hide_banner"
)

type CaptureRequest struct {
	ID           string
	Path         string
	ExpectedHash string
	Cap          int
	Policy       string
}

func coverageTimes(duration float64, capacity int) []float64 {
	count := capacity
	byDuration := int(math.Ceil(duration / 5))
	if byDuration < 1 {
		byDuration = 1
	}
	if byDuration < count {
		count = byDuration
	}
	times := make([]float64, 0, count)
	for index := 0; index < count; index++ {
		value := math.Min(math.Max(0, duration-0.05), duration*(float64(index)+0.5)/float64(count))
		times = append(times, value)
	}
	return times
}

func (service *Service) command(ctx context.Context, tool string, args []string, stream func([]byte)) error {
	_, err := media.MediaCommand(media.MediaCommandInput{
		Resources: service.Resources, Tool: tool, Args: args, Ctx: ctx, Stream: stream,
		AllowSystemFallback: service.AllowSystemFallback, Platform: service.Platform,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

type balanceBin struct {
	time   float64
	score  float64
	Sample *framecandidate.Sample
}

func (service *Service) balancedSelection(ctx context.Context, source string, duration float64, times []float64) ([]float64, []framecandidate.Sample, error) {
	bins := make([]balanceBin, len(times))
	for index, value := range times {
		bins[index] = balanceBin{time: value, score: -1}
	}
	pending := []byte{}
	var previous []byte
	index := 0
	var scanErr error
	err := service.command(ctx, "ffmpeg", []string{
		ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-i", source,
		"-an", "-vf", "fps=1/5,scale=32:18,format=gray", "-f", "rawvideo", "pipe:1",
	}, func(chunk []byte) {
		pending = append(pending, chunk...)
		for len(pending) >= 576 {
			pixels := append([]byte(nil), pending[:576]...)
			pending = pending[576:]
			time := math.Min(duration-0.05, float64(index)*5)
			index++
			metrics, measureErr := framecandidate.MeasureGray(pixels, previous)
			if measureErr != nil {
				scanErr = measureErr
				continue
			}
			binIndex := int(math.Floor((time / duration) * float64(len(bins))))
			if binIndex < 0 {
				binIndex = 0
			}
			if binIndex >= len(bins) {
				binIndex = len(bins) - 1
			}
			score := 1.0
			if previous != nil {
				score = metrics.MeanChange
			}
			if metrics.MeanLight > 4 && score > bins[binIndex].score {
				bins[binIndex].time = math.Max(0, time)
				bins[binIndex].score = score
				bins[binIndex].Sample = &framecandidate.Sample{TimeMs: int64(math.Round(math.Max(0, time) * 1000)), SampleTimeMs: int64(math.Round(math.Max(0, time) * 1000)), Reason: "balanced-change", GrayHash: framecandidate.GrayHash(pixels), Metrics: &metrics}
			}
			previous = pixels
		}
	})
	if err != nil {
		return nil, nil, fmt.Errorf("画面候选扫描失败，未发布成果: %w", err)
	}
	if scanErr != nil {
		return nil, nil, scanErr
	}
	if index == 0 || len(pending) != 0 {
		return nil, nil, fmt.Errorf("画面候选扫描返回不完整，未发布成果")
	}
	for index := range times {
		times[index] = bins[index].time
	}
	samples := make([]framecandidate.Sample, len(times))
	for index, at := range times {
		samples[index] = framecandidate.Sample{TimeMs: int64(math.Round(at * 1000)), SampleTimeMs: int64(math.Round(at * 1000)), Reason: "coverage-fallback"}
		if bins[index].Sample != nil {
			samples[index] = *bins[index].Sample
		}
	}
	return times, samples, nil
}

func (service *Service) balancedTimes(ctx context.Context, source string, duration float64, times []float64) ([]float64, error) {
	selected, _, err := service.balancedSelection(ctx, source, duration, times)
	return selected, err
}

func (service *Service) probeVideoDuration(ctx context.Context, source string) (float64, error) {
	raw, err := service.commandOutput(ctx, "ffprobe", []string{
		"-v", "error", "-show_entries", "format=duration:stream=codec_type,duration", "-of", "json", source,
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("视频检查失败: %w", err)
	}
	var metadata struct {
		Format *struct {
			Duration any `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata.Format == nil {
		return 0, badRequest(errNoVideo)
	}
	duration, err := strconv.ParseFloat(fmt.Sprint(metadata.Format.Duration), 64)
	if err != nil || !isFinite(duration) || duration <= 0 {
		return 0, badRequest(errNoVideo)
	}
	for _, stream := range metadata.Streams {
		if stream.CodecType == "video" {
			return duration, nil
		}
	}
	return 0, badRequest(errNoVideo)
}

func (service *Service) captureImageFrame(ctx context.Context, source, directory string) ([]Frame, error) {
	path := filepath.Join(directory, "frame-1.png")
	if err := service.command(ctx, "ffmpeg", []string{
		ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-i", source, "-frames:v", "1",
		"-vf", "scale=1600:1600:force_original_aspect_ratio=decrease", "-q:v", "2", "-y", path,
	}, nil); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("图片提取失败: %w", err)
	}
	if frameInfo, err := os.Stat(path); err != nil || frameInfo.Size() == 0 {
		return nil, badRequest(errImageUnreadable)
	}
	frameHash, err := fileDigest(path)
	if err != nil {
		return nil, badRequest(errImageUnreadable)
	}
	return []Frame{{Path: path, Time: 0, Hash: frameHash}}, nil
}

func (service *Service) captureVideoFrames(ctx context.Context, source, directory string, times []float64) ([]Frame, error) {
	frames := []Frame{}
	for index, time := range times {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(directory, fmt.Sprintf("frame-%d.png", index+1))
		if err := service.command(ctx, "ffmpeg", []string{
			ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-ss", strconv.FormatFloat(time, 'f', -1, 64),
			"-i", source, "-frames:v", "1", "-vf", "scale=1600:1600:force_original_aspect_ratio=decrease",
			"-q:v", "2", "-y", path,
		}, nil); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("视频画面提取失败: %w", err)
		}
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			return nil, badRequest(errFrameUnreadable)
		}
		frameHash, err := fileDigest(path)
		if err != nil {
			return nil, badRequest(errFrameUnreadable)
		}
		frames = append(frames, Frame{Path: path, Time: time, Hash: frameHash})
	}
	return frames, nil
}

func (service *Service) Capture(ctx context.Context, request CaptureRequest) (Capture, error) {
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if request.Cap < 1 || request.Cap > 12 || (request.Policy != "balanced" && request.Policy != "coverage") {
		return Capture{}, badRequest("画面提取参数无效")
	}
	if !idPattern.MatchString(request.ID) {
		return Capture{}, badRequest("画面任务标识无效")
	}
	source, err := filepath.Abs(request.Path)
	if err != nil {
		return Capture{}, badRequest(errSourceUnavailable)
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Capture{}, badRequest(errSourceUnavailable)
	}
	hash, err := fileDigest(source)
	if err != nil {
		return Capture{}, badRequest(errSourceUnavailable)
	}
	if hash != request.ExpectedHash {
		return Capture{}, badRequest("原文件已改变，请重新建立任务")
	}
	base := filepath.Join(service.FramesRoot, "native-visual")
	directory := filepath.Join(base, request.ID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Capture{}, badRequest(errFramesDirInvalid)
	}
	if item, err := os.Lstat(directory); err != nil || item.Mode()&os.ModeSymlink != 0 || !item.IsDir() {
		return Capture{}, badRequest(errFramesDirInvalid)
	}
	extension := strings.ToLower(filepath.Ext(source))
	frames := []Frame{}
	var samples []framecandidate.Sample
	duration := 0.0
	imageSource := extension == ".png" || extension == ".jpg" || extension == ".jpeg" || extension == ".webp"
	if imageSource {
		frames, err = service.captureImageFrame(ctx, source, directory)
	} else {
		duration, err = service.probeVideoDuration(ctx, source)
		if err == nil {
			times := coverageTimes(duration, request.Cap)
			if request.Policy == "balanced" {
				times, samples, err = service.balancedSelection(ctx, source, duration, times)
			}
			if err == nil {
				frames, err = service.captureVideoFrames(ctx, source, directory, times)
			}
		}
	}
	if err != nil {
		return Capture{}, err
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if after, err := fileDigest(source); err != nil || after != hash {
		return Capture{}, badRequest("提取期间源文件改变，请重新选择")
	}
	if samples == nil {
		for _, frame := range frames {
			reason := "coverage"
			if imageSource {
				reason = "image"
			}
			samples = append(samples, framecandidate.Sample{TimeMs: int64(math.Round(frame.Time * 1000)), SampleTimeMs: int64(math.Round(frame.Time * 1000)), Reason: reason})
		}
	}
	binding := visualCandidateBinding(hash, source, request.Cap, request.Policy, duration)
	evidence, err := framecandidate.Publish(service.Root, request.ID, binding, samples)
	if err != nil {
		return Capture{}, err
	}
	manifest, err := framecandidate.Read(service.Root, request.ID, *evidence, binding)
	if err != nil {
		return Capture{}, err
	}
	if err := recordExtractions(frames, evidence, manifest); err != nil {
		return Capture{}, err
	}
	result := Capture{ID: request.ID, Source: source, SourceHash: hash, Duration: duration, Frames: frames, Gaps: []any{}, CandidateEvidence: evidence}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "source.json"), encoded, 0o644); err != nil {
		return Capture{}, badRequest(errFramesDirInvalid)
	}
	return result, nil
}

func (service *Service) commandOutput(ctx context.Context, tool string, args []string) (string, error) {
	output, err := media.MediaCommand(media.MediaCommandInput{
		Resources: service.Resources, Tool: tool, Args: args, Ctx: ctx,
		AllowSystemFallback: service.AllowSystemFallback, Platform: service.Platform,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	return output, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
