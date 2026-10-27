package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var muxLanguagePattern = regexp.MustCompile(`^[-\w]{1,64}$`)
var muxControlChars = regexp.MustCompile(`[\x00-\x1f]`)

const (
	ffmpegLogLevel          = "-loglevel"
	ffmpegNoStdin           = "-nostdin"
	ffmpegHideBanner        = "-hide_banner"
	ffmpegProtocolWhitelist = "-protocol_whitelist"
	ffmpegFilePipe          = "file,pipe"
	coverFileName           = "cover.jpg"
)

func writeExclusiveVerified(path, body, changedMessage string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		if _, writeErr := file.WriteString(body); writeErr != nil {
			_ = file.Close()
			return writeErr
		}
		return file.Close()
	}
	if !os.IsExist(err) {
		return err
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s", changedMessage)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil || string(raw) != body {
		return fmt.Errorf("%s", changedMessage)
	}
	return nil
}

type LocalTrack struct {
	Path string
	Kind string
}

type MuxSubtitle struct {
	Path     string
	Language string
}

type MuxInput struct {
	Directory        string
	Tracks           []LocalTrack
	Options          NativeMediaOptions
	ExpectedDuration float64
	Title            string
	Ctx              context.Context // NOSONAR: request-scoped input struct; the caller owns this context for a single operation.
	Subtitles        []MuxSubtitle
	Chapters         []MuxChapter
	Cover            string
	OutputName       string
	KeepSegments     []MediaInterval
}

type MuxResult struct {
	Path          string     `json:"path"`
	Relative      string     `json:"relative"`
	Probe         MediaProbe `json:"probe"`
	Intermediates []string   `json:"intermediates"`
	Reused        bool       `json:"reused"`
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func escapeFFMetadata(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\n", "\\\n", "\r", "\\\r")
	return replacer.Replace(value)
}

func hashJSON(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func inStringList(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (mux *NativeMediaMux) Mux(input MuxInput) (MuxResult, error) {
	directory := filepath.Clean(input.Directory)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return MuxResult{}, fmt.Errorf("媒体成果目录无效")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || !strings.EqualFold(resolved, directory) {
		return MuxResult{}, fmt.Errorf("媒体成果目录无效")
	}
	if len(input.Tracks) == 0 || len(input.Tracks) > 8 || input.Options.SubtitleOnly {
		return MuxResult{}, fmt.Errorf("音视频合并输入无效")
	}
	tracks := []LocalTrack{}
	for _, track := range input.Tracks {
		path, err := localMediaFile(track.Path)
		if err != nil {
			return MuxResult{}, err
		}
		prefix := strings.ToLower(directory) + string(os.PathSeparator)
		if !strings.HasPrefix(strings.ToLower(path), prefix) {
			return MuxResult{}, fmt.Errorf("媒体输入不属于该任务")
		}
		tracks = append(tracks, LocalTrack{Path: path, Kind: track.Kind})
	}
	options := input.Options
	audio := options.AudioOnly
	extension := options.Container
	if audio {
		switch options.AudioFormat {
		case "best":
			extension = "mka"
		case "vorbis":
			extension = "ogg"
		default:
			extension = options.AudioFormat
		}
	}
	var kept []MediaInterval
	if input.KeepSegments != nil {
		kept, err = CheckedMediaIntervals(input.KeepSegments, input.ExpectedDuration)
		if err != nil {
			return MuxResult{}, err
		}
	}
	chapters := input.Chapters
	if kept != nil && input.Chapters != nil {
		chapters = RetimeChapters(input.Chapters, kept)
	}
	if !inStringList(extension, []string{"mkv", "mp4", "webm", "mka", "m4a", "mp3", "opus", "flac", "wav", "aac", "ogg"}) {
		return MuxResult{}, fmt.Errorf("媒体输出格式无效")
	}
	cover := ""
	if input.Cover != "" {
		cover, err = localMediaFile(input.Cover)
		if err != nil {
			return MuxResult{}, err
		}
		if !strings.EqualFold(filepath.Dir(cover), directory) {
			return MuxResult{}, fmt.Errorf("封面不属于该任务")
		}
	}
	if cover != "" && !inStringList(extension, []string{"mkv", "mka", "mp4", "m4a", "mp3", "flac"}) {
		return MuxResult{}, fmt.Errorf("此格式不能嵌入封面，请改用 MKV、MP4、M4A、MP3 或 FLAC")
	}
	if input.OutputName != "" {
		if !strings.HasSuffix(input.OutputName, "."+extension) ||
			layoutNameForbidden.MatchString(input.OutputName) ||
			input.OutputName == "." || input.OutputName == ".." {
			return MuxResult{}, fmt.Errorf("媒体输出名称无效")
		}
	}
	output := filepath.Join(directory, input.OutputName)
	if input.OutputName == "" {
		output = filepath.Join(directory, "result."+extension)
	}
	args := []string{ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-n"}
	for _, track := range tracks {
		args = append(args,
			ffmpegProtocolWhitelist, ffmpegFilePipe,
			"-format_whitelist", "mov,matroska,webm,flv,mp3,aac,ogg,wav,flac,mpegts",
			"-i", track.Path,
		)
	}
	subtitleInputs := input.Subtitles
	if len(subtitleInputs) > 100 {
		return MuxResult{}, fmt.Errorf("字幕轨道过多")
	}
	for _, subtitle := range subtitleInputs {
		path, err := localMediaFile(subtitle.Path)
		if err != nil {
			return MuxResult{}, err
		}
		if !strings.EqualFold(filepath.Dir(path), directory) || !muxLanguagePattern.MatchString(subtitle.Language) {
			return MuxResult{}, fmt.Errorf("字幕文件不属于该任务")
		}
		args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "srt", "-i", path)
	}
	chapterIndex := -1
	if options.Chapters && len(chapters) > 0 && (options.ClipEnd == nil || kept != nil) {
		metadata := []string{}
		for _, chapter := range chapters {
			if !finite(chapter.Start) || !finite(chapter.End) || chapter.Start < 0 ||
				chapter.End <= chapter.Start || chapter.End > input.ExpectedDuration+1 {
				return MuxResult{}, fmt.Errorf("章节时间无效")
			}
			metadata = append(metadata, fmt.Sprintf(
				"[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n",
				int64(math.Round(chapter.Start*1000)), int64(math.Round(chapter.End*1000)),
				escapeFFMetadata(clipRunes(chapter.Title, 300)),
			))
		}
		path := filepath.Join(directory, "chapters.ffmeta")
		body := ";FFMETADATA1\n" + strings.Join(metadata, "\n")
		if err := writeExclusiveVerified(path, body, "章节恢复文件已改变，请核验后继续"); err != nil {
			return MuxResult{}, err
		}
		chapterIndex = len(tracks) + len(subtitleInputs)
		args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "ffmetadata", "-i", path)
	}
	attachedCover := cover != "" && (extension == "mkv" || extension == "mka")
	coverInput := len(tracks) + len(subtitleInputs)
	if chapterIndex >= 0 {
		coverInput++
	}
	if cover != "" && !attachedCover {
		args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "image2", "-i", cover)
	}
	if kept == nil && options.ClipStart != nil && options.ClipEnd != nil {
		args = append(args, "-ss", formatNumber(*options.ClipStart), "-t", formatNumber(*options.ClipEnd-*options.ClipStart))
	}
	videoIndex := -1
	for index, track := range tracks {
		if track.Kind != "audio" {
			videoIndex = index
			break
		}
	}
	audioIndices := []int{}
	for index, track := range tracks {
		if track.Kind != "video" {
			audioIndices = append(audioIndices, index)
		}
	}
	if (audio && len(audioIndices) == 0) || (!audio && videoIndex < 0) {
		return MuxResult{}, fmt.Errorf("没有可用音轨或画面")
	}
	filterFile := ""
	if kept != nil {
		audioTracks := audioIndices
		if audio {
			audioTracks = audioIndices[:1]
		} else if !options.MergeAudio && len(audioIndices) > 1 {
			audioTracks = audioIndices[:1]
		}
		filters := []string{}
		labels := []string{}
		for rangeIndex, interval := range kept {
			group := []string{}
			if !audio {
				filters = append(filters, fmt.Sprintf(
					"[%d:v:0]trim=start=%s:end=%s,setpts=PTS-STARTPTS[v%d]",
					videoIndex, formatNumber(interval.Start), formatNumber(interval.End), rangeIndex,
				))
				group = append(group, fmt.Sprintf("[v%d]", rangeIndex))
			}
			for audioIndex, trackIndex := range audioTracks {
				filters = append(filters, fmt.Sprintf(
					"[%d:a:0]atrim=start=%s:end=%s,asetpts=PTS-STARTPTS[a%d_%d]",
					trackIndex, formatNumber(interval.Start), formatNumber(interval.End), rangeIndex, audioIndex,
				))
				group = append(group, fmt.Sprintf("[a%d_%d]", rangeIndex, audioIndex))
			}
			labels = append(labels, strings.Join(group, ""))
		}
		outputs := ""
		if !audio {
			outputs += "[edited_v]"
		}
		for audioIndex := range audioTracks {
			outputs += fmt.Sprintf("[edited_a%d]", audioIndex)
		}
		filters = append(filters, strings.Join(labels, "")+fmt.Sprintf(
			"concat=n=%d:v=%d:a=%d", len(kept), boolInt(!audio), len(audioTracks),
		)+outputs)
		filterFile = filepath.Base(output) + ".filter.txt"
		path := filepath.Join(directory, filterFile)
		body := strings.Join(filters, ";\n")
		if err := writeExclusiveVerified(path, body, "片段拼接恢复文件已改变"); err != nil {
			return MuxResult{}, err
		}
		args = append(args, "-filter_complex_script", path)
		if !audio {
			args = append(args, "-map", "[edited_v]")
		}
		for audioIndex := range audioTracks {
			args = append(args, "-map", fmt.Sprintf("[edited_a%d]", audioIndex))
		}
		if audio && cover == "" {
			args = append(args, "-vn")
		}
	} else if audio {
		args = append(args, "-map", fmt.Sprintf("%d:a:0", audioIndices[0]))
		if cover == "" {
			args = append(args, "-vn")
		}
	} else {
		args = append(args, "-map", fmt.Sprintf("%d:v:0", videoIndex))
		selectedAudio := audioIndices
		if !options.MergeAudio && len(audioIndices) > 1 {
			selectedAudio = audioIndices[:1]
		}
		for _, trackIndex := range selectedAudio {
			args = append(args, "-map", fmt.Sprintf("%d:a:0?", trackIndex))
		}
	}
	for index, subtitle := range subtitleInputs {
		args = append(args, "-map", fmt.Sprintf("%d:s:0", len(tracks)+index),
			"-metadata:s:s:"+strconv.Itoa(index), "language="+subtitle.Language)
	}
	if len(subtitleInputs) > 0 {
		args = append(args, "-c:s", "srt")
	}
	cut := options.ClipEnd != nil || kept != nil
	if !audio {
		videoCodec := "copy"
		if options.Container == "webm" {
			videoCodec = "libvpx-vp9"
		} else if cut {
			videoCodec = "libx264"
		}
		args = append(args, "-c:v", videoCodec)
		if cut && options.Container != "webm" {
			args = append(args, "-preset", "fast", "-crf", "18")
		}
	}
	if cover != "" {
		if attachedCover {
			args = append(args, "-attach", cover,
				"-metadata:s:t", "mimetype=image/jpeg", "-metadata:s:t", "filename=cover.jpg")
		} else {
			streamIndex := 0
			if !audio {
				streamIndex = 1
			}
			args = append(args, "-map", fmt.Sprintf("%d:v:0", coverInput),
				fmt.Sprintf("-c:v:%d", streamIndex), "mjpeg",
				fmt.Sprintf("-disposition:v:%d", streamIndex), "attached_pic")
		}
	}
	audioCodec := ""
	if audio {
		switch options.AudioFormat {
		case "best":
			if kept != nil {
				audioCodec = "aac"
			} else {
				audioCodec = "copy"
			}
		case "m4a":
			audioCodec = "aac"
		case "mp3":
			audioCodec = "libmp3lame"
		case "opus":
			audioCodec = "libopus"
		case "flac":
			audioCodec = "flac"
		case "wav":
			audioCodec = "pcm_s16le"
		case "aac":
			audioCodec = "aac"
		case "vorbis":
			audioCodec = "libvorbis"
		}
	} else if options.Container == "webm" {
		audioCodec = "libopus"
	} else if options.Container == "mp4" || cut {
		audioCodec = "aac"
	} else {
		audioCodec = "copy"
	}
	args = append(args, "-c:a", audioCodec)
	if options.AudioBitrate > 0 && audioCodec != "copy" && audioCodec != "flac" && audioCodec != "pcm_s16le" {
		args = append(args, "-b:a", fmt.Sprintf("%dk", options.AudioBitrate))
	}
	if options.Metadata {
		args = append(args, "-metadata", "title="+clipRunes(muxControlChars.ReplaceAllString(input.Title, " "), 240))
	} else {
		args = append(args, "-map_metadata", "-1")
	}
	if !options.Chapters {
		args = append(args, "-map_chapters", "-1")
	} else if chapterIndex >= 0 {
		args = append(args, "-map_chapters", strconv.Itoa(chapterIndex))
	}
	args = append(args, output)
	inputs := []string{}
	for _, track := range tracks {
		inputs = append(inputs, track.Path)
	}
	for _, subtitle := range subtitleInputs {
		inputs = append(inputs, subtitle.Path)
	}
	if cover != "" {
		inputs = append(inputs, cover)
	}
	hashes := []string{}
	for _, path := range inputs {
		hash, err := MediaFileHash(path)
		if err != nil {
			return MuxResult{}, err
		}
		hashes = append(hashes, hash)
	}
	planParts := []any{"mux-v1", args, input.ExpectedDuration, chapters}
	if kept != nil {
		planParts = append(planParts, kept)
	}
	for _, hash := range hashes {
		planParts = append(planParts, hash)
	}
	artifact, err := NewOwnedMediaArtifact(directory, filepath.Base(output), hashJSON(planParts))
	if err != nil {
		return MuxResult{}, err
	}
	prepared, err := artifact.Prepare()
	if err != nil {
		return MuxResult{}, err
	}
	if !prepared {
		if _, err := MediaCommand(MediaCommandInput{
			Resources: mux.Resources, Tool: "ffmpeg", Args: args, Ctx: input.Ctx,
			AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
		}); err != nil {
			return MuxResult{}, err
		}
	}
	proof, err := mux.Probe(input.Ctx, output)
	if err != nil {
		return MuxResult{}, err
	}
	expected := input.ExpectedDuration
	if kept != nil {
		expected = 0
		for _, interval := range kept {
			expected += interval.End - interval.Start
		}
	} else if cut && options.ClipStart != nil && options.ClipEnd != nil {
		expected = *options.ClipEnd - *options.ClipStart
	}
	if (!audio && proof.Video == 0) || (audio && proof.Audio == 0) || (len(audioIndices) > 0 && proof.Audio == 0) {
		return MuxResult{}, fmt.Errorf("成果缺少所选音轨或画面，保留恢复文件")
	}
	if proof.Subtitles != len(subtitleInputs) || (chapterIndex >= 0 && proof.Chapters != len(chapters)) {
		return MuxResult{}, fmt.Errorf("成果字幕或章节数量不符，保留恢复文件")
	}
	if cover != "" && proof.Covers == 0 {
		return MuxResult{}, fmt.Errorf("成果未保留封面，恢复文件保留")
	}
	if !finite(expected) || expected <= 0 || math.Abs(proof.Duration-expected) > math.Max(1.5, expected*0.015) {
		return MuxResult{}, fmt.Errorf("成果时长与所选分集不一致，保留恢复文件")
	}
	if _, err := artifact.Finish(); err != nil {
		return MuxResult{}, err
	}
	intermediates := artifact.Intermediates()
	if filterFile != "" {
		intermediates = append(intermediates, filterFile)
	}
	return MuxResult{
		Path: output, Relative: filepath.Base(output), Probe: proof,
		Intermediates: intermediates, Reused: prepared,
	}, nil
}

func (mux *NativeMediaMux) PrepareCover(directory string, bytes []byte, crop bool, ctx context.Context) (map[string]any, error) {
	if len(bytes) > 8_000_000 {
		return nil, fmt.Errorf("封面文件过大")
	}
	extension := ""
	if len(bytes) >= 3 && bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF {
		extension = "jpg"
	} else if len(bytes) >= 8 && string(bytes[:8]) == "\x89PNG\r\n\x1a\n" {
		extension = "png"
	} else if len(bytes) >= 12 && string(bytes[:4]) == "RIFF" && string(bytes[8:12]) == "WEBP" {
		extension = "webp"
	}
	if extension == "" {
		return nil, fmt.Errorf("封面不是可识别的图片")
	}
	base := filepath.Clean(directory)
	info, err := os.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("封面目录无效")
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil || !strings.EqualFold(resolved, base) {
		return nil, fmt.Errorf("封面目录无效")
	}
	source := filepath.Join(base, "cover-source."+extension)
	output := filepath.Join(base, coverFileName)
	if err := writeExclusiveVerified(source, string(bytes), "封面恢复文件已改变"); err != nil {
		return nil, err
	}
	raw, err := MediaCommand(MediaCommandInput{
		Resources: mux.Resources, Tool: "ffprobe",
		Args: []string{
			"-v", "error", ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "image2",
			"-show_entries", "stream=width,height", "-of", "json", source,
		},
		Ctx: ctx, AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
	})
	if err != nil {
		return nil, err
	}
	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	if json.Unmarshal([]byte(raw), &probe) != nil || len(probe.Streams) == 0 ||
		probe.Streams[0].Width <= 0 || probe.Streams[0].Height <= 0 {
		return nil, fmt.Errorf("封面探测结果无效")
	}
	if probe.Streams[0].Width*probe.Streams[0].Height > 40_000_000 {
		return nil, fmt.Errorf("封面图片像素过大")
	}
	filter := "scale=w=min(1200\\,iw):h=min(1200\\,ih):force_original_aspect_ratio=decrease"
	if crop {
		filter = "crop=min(iw\\,ih):min(iw\\,ih)," + filter
	}
	artifact, err := NewOwnedMediaArtifact(base, coverFileName, hashJSON([]any{
		"cover-v1", ComputeHash(bytes), crop,
	}))
	if err != nil {
		return nil, err
	}
	prepared, err := artifact.Prepare()
	if err != nil {
		return nil, err
	}
	if !prepared {
		if _, err := MediaCommand(MediaCommandInput{
			Resources: mux.Resources, Tool: "ffmpeg",
			Args: []string{
				ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-n",
				ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "image2", "-i", source,
				"-vf", filter, "-frames:v", "1", "-q:v", "2", "-update", "1", output,
			},
			Ctx: ctx, AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
		}); err != nil {
			return nil, err
		}
	}
	outputInfo, err := os.Lstat(output)
	if err != nil || outputInfo.Size() == 0 {
		return nil, fmt.Errorf("封面转换未生成结果")
	}
	if _, err := artifact.Finish(); err != nil {
		return nil, err
	}
	intermediates := append([]string{filepath.Base(source)}, artifact.Intermediates()...)
	return map[string]any{"path": output, "relative": coverFileName, "intermediates": intermediates}, nil
}

func (mux *NativeMediaMux) Concatenate(directory string, paths []string, key string, ctx context.Context) (map[string]any, error) {
	if !regexp.MustCompile(`^track-\d+$`).MatchString(key) || len(paths) < 2 || len(paths) > 1000 {
		return nil, fmt.Errorf("视频分段列表无效")
	}
	base := filepath.Clean(directory)
	lines := []string{}
	for _, path := range paths {
		target, err := localMediaFile(path)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(filepath.Dir(target), base) ||
			!regexp.MustCompile(`^track-\d+-segment-\d+\.partial$`).MatchString(filepath.Base(target)) {
			return nil, fmt.Errorf("视频分段不属于该任务")
		}
		lines = append(lines, "file '"+filepath.Base(target)+"'")
	}
	list := filepath.Join(base, key+".ffconcat")
	body := "ffconcat version 1.0\n" + strings.Join(lines, "\n") + "\n"
	if err := writeExclusiveVerified(list, body, "视频拼接列表已改变"); err != nil {
		return nil, err
	}
	output := filepath.Join(base, "joined-"+key+".mkv")
	hashes := []string{}
	for _, path := range paths {
		hash, err := MediaFileHash(path)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, hash)
	}
	planParts := []any{"concat-v1", lines}
	for _, hash := range hashes {
		planParts = append(planParts, hash)
	}
	artifact, err := NewOwnedMediaArtifact(base, filepath.Base(output), hashJSON(planParts))
	if err != nil {
		return nil, err
	}
	prepared, err := artifact.Prepare()
	if err != nil {
		return nil, err
	}
	if !prepared {
		if _, err := MediaCommand(MediaCommandInput{
			Resources: mux.Resources, Tool: "ffmpeg",
			Args: []string{
				ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-n",
				ffmpegProtocolWhitelist, ffmpegFilePipe,
				"-format_whitelist", "concat,mov,matroska,webm,flv,mp3,aac,ogg,wav,flac,mpegts",
				"-f", "concat", "-safe", "1", "-i", list, "-map", "0", "-c", "copy", output,
			},
			Ctx: ctx, AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
		}); err != nil {
			return nil, err
		}
	}
	proof, err := mux.Probe(ctx, output)
	if err != nil {
		return nil, err
	}
	if proof.Video == 0 {
		return nil, fmt.Errorf("拼接结果没有画面")
	}
	if _, err := artifact.Finish(); err != nil {
		return nil, err
	}
	intermediates := append([]string{filepath.Base(list), filepath.Base(output)}, artifact.Intermediates()...)
	return map[string]any{"path": output, "intermediates": intermediates}, nil
}
