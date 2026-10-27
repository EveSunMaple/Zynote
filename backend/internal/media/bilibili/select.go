package bilibili

import (
	"math"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func selectExactStreams(source Source, options media.NativeMediaOptions) ([]MediaStream, error) {
	ids := strings.Split(options.FormatIDs, "+")
	chosen := []MediaStream{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, contract.InputInvalid("精确格式与输出类型冲突")
		}
		seen[id] = true
		found := false
		for _, stream := range source.Streams {
			if stream.ID == id {
				chosen = append(chosen, stream)
				found = true
				break
			}
		}
		if !found {
			return nil, contract.InputInvalid("所选格式已失效")
		}
	}
	hasVideo := false
	for _, stream := range chosen {
		if stream.Kind == "video" {
			hasVideo = true
		}
	}
	if options.AudioOnly && hasVideo {
		return nil, contract.InputInvalid("精确格式与输出类型冲突")
	}
	if !options.AudioOnly && !hasVideo {
		return nil, contract.InputInvalid("请选择一个视频格式")
	}
	return chosen, nil
}

func audioStreamLimit(options media.NativeMediaOptions) (float64, error) {
	if options.AudioQuality == "best" {
		return math.Inf(1), nil
	}
	parsed, err := strconv.ParseFloat(options.AudioQuality, 64)
	if err != nil {
		return 0, contract.InputInvalid("下载音质无效")
	}
	return parsed * 1000, nil
}

func pickAudioStream(audio []MediaStream, limit float64) *MediaStream {
	for index := range audio {
		if audio[index].Bandwidth <= limit {
			return &audio[index]
		}
	}
	if len(audio) > 0 {
		return &audio[len(audio)-1]
	}
	return nil
}

func videoCodecPrefix(codec string) string {
	return map[string]string{"auto": "", "h264": "avc", "h265": "hev", "vp9": "vp", "av1": "av01"}[codec]
}

func filterVideoStreams(streams []MediaStream, height int, prefix string) []MediaStream {
	result := []MediaStream{}
	for _, stream := range streams {
		if stream.Kind != "video" || (height != 0 && stream.Height > height) {
			continue
		}
		if prefix != "" && !strings.HasPrefix(stream.Codec, prefix) {
			continue
		}
		result = append(result, stream)
	}
	return result
}

func combinedStreams(streams []MediaStream, height int, prefix string) []MediaStream {
	result := []MediaStream{}
	for _, stream := range streams {
		if stream.Kind != "combined" {
			continue
		}
		allowed := stream.Transport == "hls"
		if !allowed {
			heightOK := height == 0 || (stream.Height > 0 && stream.Height <= height)
			codecOK := prefix == "" || strings.HasPrefix(stream.Codec, prefix)
			allowed = heightOK && codecOK
		}
		if allowed {
			result = append(result, stream)
		}
	}
	return result
}

func SelectStreams(source Source, options media.NativeMediaOptions) ([]MediaStream, error) {
	if options.FormatIDs != "" {
		return selectExactStreams(source, options)
	}
	tuning, err := media.MediaTuning(options.CustomArgs)
	if err != nil {
		return nil, err
	}
	free := tuning["--prefer-free-formats"] == true
	audio := sortBiliStreams(filterKind(source.Streams, "audio"), options.FormatSort, free)
	limit, err := audioStreamLimit(options)
	if err != nil {
		return nil, err
	}
	pickedAudio := pickAudioStream(audio, limit)
	if options.AudioOnly {
		if pickedAudio != nil {
			return []MediaStream{*pickedAudio}, nil
		}
		for _, stream := range source.Streams {
			if stream.Kind == "combined" {
				return []MediaStream{stream}, nil
			}
		}
		return nil, contract.InputInvalid("此视频没有可用音轨")
	}
	prefix := videoCodecPrefix(options.VideoCodec)
	video := sortBiliStreams(filterVideoStreams(source.Streams, options.Height, prefix), options.FormatSort, free)
	if len(video) > 0 {
		if pickedAudio != nil {
			return []MediaStream{video[0], *pickedAudio}, nil
		}
		return []MediaStream{video[0]}, nil
	}
	combined := sortBiliStreams(combinedStreams(source.Streams, options.Height, prefix), options.FormatSort, free)
	if len(combined) > 0 {
		return []MediaStream{combined[0]}, nil
	}
	return nil, contract.InputInvalid("没有符合画质和编码要求的格式，请调整选择")
}

func filterKind(streams []MediaStream, kind string) []MediaStream {
	result := []MediaStream{}
	for _, stream := range streams {
		if stream.Kind == kind {
			result = append(result, stream)
		}
	}
	return result
}

func sortBiliStreams(streams []MediaStream, sortSpec string, free bool) []MediaStream {
	if len(streams) == 0 {
		return streams
	}
	byID := map[string]MediaStream{}
	rows := make([]media.NativeMediaStream, 0, len(streams))
	for _, stream := range streams {
		byID[stream.ID] = stream
		rows = append(rows, media.NativeMediaStream{
			ID: stream.ID, Height: stream.Height, Bandwidth: stream.Bandwidth,
			Codec: stream.Codec, Extension: stream.Extension,
		})
	}
	sorted := media.SortMediaStreams(rows, sortSpec, free)
	result := make([]MediaStream, 0, len(sorted))
	for _, row := range sorted {
		result = append(result, byID[row.ID])
	}
	return result
}
