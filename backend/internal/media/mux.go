package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

type MediaProbe struct {
	Duration  float64 `json:"duration"`
	Video     int     `json:"video"`
	Audio     int     `json:"audio"`
	Subtitles int     `json:"subtitles"`
	Chapters  int     `json:"chapters"`
	Covers    int     `json:"covers"`
}

type NativeMediaMux struct {
	Resources           string
	AllowSystemFallback bool
	Platform            string
}

func localMediaFile(path string) (string, error) {
	target := filepath.Clean(path)
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("媒体输入不是可验证的本地文件")
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil || strings.ToLower(resolved) != strings.ToLower(target) {
		return "", fmt.Errorf("媒体输入不是可验证的本地文件")
	}
	return target, nil
}

func (mux *NativeMediaMux) Probe(ctx context.Context, path string) (MediaProbe, error) {
	source, err := localMediaFile(path)
	if err != nil {
		return MediaProbe{}, err
	}
	raw, err := MediaCommand(MediaCommandInput{
		Resources: mux.Resources,
		Tool:      "ffprobe",
		Args: []string{
			"-v", "error",
			"-protocol_whitelist", "file,pipe",
			"-format_whitelist", "mov,matroska,webm,flv,mp3,aac,ogg,wav,flac,mpegts",
			"-show_entries", "format=duration:stream=codec_type:stream_disposition=attached_pic:stream_tags=filename:chapter",
			"-of", "json",
			source,
		},
		Ctx:                 ctx,
		AllowSystemFallback: mux.AllowSystemFallback,
		Platform:            mux.Platform,
	})
	if err != nil {
		return MediaProbe{}, err
	}
	var data struct {
		Format struct {
			Duration json.Number `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType   string `json:"codec_type"`
			Disposition struct {
				AttachedPic *int `json:"attached_pic"`
			} `json:"disposition"`
			Tags struct {
				Filename string `json:"filename"`
			} `json:"tags"`
		} `json:"streams"`
		Chapters []json.RawMessage `json:"chapters"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return MediaProbe{}, fmt.Errorf("媒体探测结果无效")
	}
	duration, err := data.Format.Duration.Float64()
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return MediaProbe{}, fmt.Errorf("媒体探测结果无效")
	}
	probe := MediaProbe{Duration: duration, Chapters: len(data.Chapters)}
	for _, stream := range data.Streams {
		attached := stream.Disposition.AttachedPic != nil && *stream.Disposition.AttachedPic != 0
		switch stream.CodecType {
		case "video":
			if !attached {
				probe.Video++
			}
		case "audio":
			probe.Audio++
		case "subtitle":
			probe.Subtitles++
		}
		if attached || (stream.CodecType == "attachment" && stream.Tags.Filename == "cover.jpg") {
			probe.Covers++
		}
	}
	return probe, nil
}
