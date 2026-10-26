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

	"github.com/EveSunMaple/Zynote/backend/internal/media"
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

func (service *Service) Capture(ctx context.Context, request CaptureRequest) (Capture, error) {
	if !idPattern.MatchString(request.ID) {
		return Capture{}, badRequest("画面任务标识无效")
	}
	source, err := filepath.Abs(request.Path)
	if err != nil {
		return Capture{}, badRequest("所选源文件不可用")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Capture{}, badRequest("所选源文件不可用")
	}
	hash, err := fileDigest(source)
	if err != nil {
		return Capture{}, badRequest("所选源文件不可用")
	}
	if hash != request.ExpectedHash {
		return Capture{}, badRequest("原文件已改变，请重新建立任务")
	}
	base := filepath.Join(service.FramesRoot, "native-visual")
	directory := filepath.Join(base, request.ID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return Capture{}, badRequest("画面暂存目录无效")
	}
	if item, err := os.Lstat(directory); err != nil || item.Mode()&os.ModeSymlink != 0 || !item.IsDir() {
		return Capture{}, badRequest("画面暂存目录无效")
	}
	extension := strings.ToLower(filepath.Ext(source))
	frames := []Frame{}
	duration := 0.0
	imageSource := extension == ".png" || extension == ".jpg" || extension == ".jpeg" || extension == ".webp"
	if imageSource {
		path := filepath.Join(directory, "frame-1.png")
		if err := service.command(ctx, "ffmpeg", []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-i", source, "-frames:v", "1",
			"-vf", "scale=1600:1600:force_original_aspect_ratio=decrease", "-q:v", "2", "-y", path,
		}, nil); err != nil {
			return Capture{}, badRequest("图片无法读取")
		}
		if frameInfo, err := os.Stat(path); err != nil || frameInfo.Size() == 0 {
			return Capture{}, badRequest("图片无法读取")
		}
		frameHash, err := fileDigest(path)
		if err != nil {
			return Capture{}, badRequest("图片无法读取")
		}
		frames = append(frames, Frame{Path: path, Time: 0, Hash: frameHash})
	} else {
		raw, err := service.commandOutput(ctx, "ffprobe", []string{
			"-v", "error", "-show_entries", "format=duration:stream=codec_type,duration", "-of", "json", source,
		})
		if err != nil {
			return Capture{}, badRequest("源文件没有视频画面")
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
			return Capture{}, badRequest("源文件没有视频画面")
		}
		duration, err = strconv.ParseFloat(fmt.Sprint(metadata.Format.Duration), 64)
		if err != nil || !isFinite(duration) || duration <= 0 {
			return Capture{}, badRequest("源文件没有视频画面")
		}
		hasVideo := false
		for _, stream := range metadata.Streams {
			if stream.CodecType == "video" {
				hasVideo = true
				break
			}
		}
		if !hasVideo {
			return Capture{}, badRequest("源文件没有视频画面")
		}
		times := coverageTimes(duration, request.Cap)
		if request.Policy == "balanced" {
			type bin struct {
				time  float64
				score float64
			}
			bins := make([]bin, len(times))
			for index, value := range times {
				bins[index] = bin{time: value, score: -1}
			}
			pending := []byte{}
			var previous []byte
			index := 0
			_ = service.command(ctx, "ffmpeg", []string{
				"-hide_banner", "-loglevel", "error", "-nostdin", "-i", source,
				"-an", "-vf", "fps=1/5,scale=32:18,format=gray", "-f", "rawvideo", "pipe:1",
			}, func(chunk []byte) {
				pending = append(pending, chunk...)
				for len(pending) >= 576 {
					pixels := append([]byte(nil), pending[:576]...)
					pending = pending[576:]
					time := math.Min(duration-0.05, float64(index)*5)
					index++
					light := 0
					change := 0
					for point := 0; point < len(pixels); point++ {
						light += int(pixels[point])
						if previous != nil {
							difference := int(pixels[point]) - int(previous[point])
							if difference < 0 {
								difference = -difference
							}
							change += difference
						}
					}
					binIndex := int(math.Floor((time / duration) * float64(len(bins))))
					if binIndex >= len(bins) {
						binIndex = len(bins) - 1
					}
					score := 1.0
					if previous != nil {
						score = float64(change) / 576
					}
					if float64(light)/576 > 4 && score > bins[binIndex].score {
						bins[binIndex].time = math.Max(0, time)
						bins[binIndex].score = score
					}
					previous = pixels
				}
			})
			for index := range times {
				times[index] = bins[index].time
			}
		}
		for index, time := range times {
			if err := ctx.Err(); err != nil {
				return Capture{}, err
			}
			path := filepath.Join(directory, fmt.Sprintf("frame-%d.png", index+1))
			if err := service.command(ctx, "ffmpeg", []string{
				"-hide_banner", "-loglevel", "error", "-nostdin", "-ss", strconv.FormatFloat(time, 'f', -1, 64),
				"-i", source, "-frames:v", "1", "-vf", "scale=1600:1600:force_original_aspect_ratio=decrease",
				"-q:v", "2", "-y", path,
			}, nil); err != nil {
				return Capture{}, badRequest("截取的画面无法读取")
			}
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				return Capture{}, badRequest("截取的画面无法读取")
			}
			frameHash, err := fileDigest(path)
			if err != nil {
				return Capture{}, badRequest("截取的画面无法读取")
			}
			frames = append(frames, Frame{Path: path, Time: time, Hash: frameHash})
		}
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, err
	}
	if after, err := fileDigest(source); err != nil || after != hash {
		return Capture{}, badRequest("提取期间源文件改变，请重新选择")
	}
	result := Capture{ID: request.ID, Source: source, SourceHash: hash, Duration: duration, Frames: frames, Gaps: []any{}}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(directory, "source.json"), encoded, 0o644); err != nil {
		return Capture{}, badRequest("画面暂存目录无效")
	}
	return result, nil
}

func (service *Service) commandOutput(ctx context.Context, tool string, args []string) (string, error) {
	output, err := media.MediaCommand(media.MediaCommandInput{
		Resources: service.Resources, Tool: tool, Args: args, Ctx: ctx,
		AllowSystemFallback: service.AllowSystemFallback, Platform: service.Platform,
	})
	if err != nil {
		return "", err
	}
	return output, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
