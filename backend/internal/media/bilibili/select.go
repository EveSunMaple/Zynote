package bilibili

import (
	"math"
	"strconv"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

func SelectStreams(source Source, options media.NativeMediaOptions) ([]MediaStream, error) {
	if options.FormatIDs != "" {
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
	tuning, err := media.MediaTuning(options.CustomArgs)
	if err != nil {
		return nil, err
	}
	free := tuning["--prefer-free-formats"] == true
	audio := sortBiliStreams(filterKind(source.Streams, "audio"), options.FormatSort, free)
	limit := math.Inf(1)
	if options.AudioQuality != "best" {
		parsed, err := strconv.ParseFloat(options.AudioQuality, 64)
		if err != nil {
			return nil, contract.InputInvalid("下载音质无效")
		}
		limit = parsed * 1000
	}
	var pickedAudio *MediaStream
	for index := range audio {
		if audio[index].Bandwidth <= limit {
			pickedAudio = &audio[index]
			break
		}
	}
	if pickedAudio == nil && len(audio) > 0 {
		pickedAudio = &audio[len(audio)-1]
	}
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
	video := []MediaStream{}
	for _, stream := range source.Streams {
		if stream.Kind != "video" {
			continue
		}
		if options.Height != 0 && stream.Height > options.Height {
			continue
		}
		video = append(video, stream)
	}
	prefix := map[string]string{"auto": "", "h264": "avc", "h265": "hev", "vp9": "vp", "av1": "av01"}[options.VideoCodec]
	if prefix != "" {
		filtered := []MediaStream{}
		for _, stream := range video {
			if strings.HasPrefix(stream.Codec, prefix) {
				filtered = append(filtered, stream)
			}
		}
		video = filtered
	}
	video = sortBiliStreams(video, options.FormatSort, free)
	if len(video) > 0 {
		if pickedAudio != nil {
			return []MediaStream{video[0], *pickedAudio}, nil
		}
		return []MediaStream{video[0]}, nil
	}
	combinedCandidates := []MediaStream{}
	for _, stream := range source.Streams {
		if stream.Kind != "combined" {
			continue
		}
		allowed := stream.Transport == "hls"
		if !allowed {
			heightOK := options.Height == 0 || (stream.Height > 0 && stream.Height <= options.Height)
			codecOK := prefix == "" || strings.HasPrefix(stream.Codec, prefix)
			allowed = heightOK && codecOK
		}
		if allowed {
			combinedCandidates = append(combinedCandidates, stream)
		}
	}
	combined := sortBiliStreams(combinedCandidates, options.FormatSort, free)
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
