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

type muxSetup struct {
	input     MuxInput
	directory string
	tracks    []LocalTrack
	options   NativeMediaOptions
	audio     bool
	extension string
	kept      []MediaInterval
	chapters  []MuxChapter
	cover     string
	output    string
}

type muxPlan struct {
	args          []string
	subtitles     []MuxSubtitle
	chapterIndex  int
	coverInput    int
	attachedCover bool
	videoIndex    int
	audioIndices  []int
	filterFile    string
}

func muxDirectory(path string) (string, error) {
	directory := filepath.Clean(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("媒体成果目录无效")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || !strings.EqualFold(resolved, directory) {
		return "", fmt.Errorf("媒体成果目录无效")
	}
	return directory, nil
}

func muxTracks(input MuxInput, directory string) ([]LocalTrack, error) {
	if len(input.Tracks) == 0 || len(input.Tracks) > 8 || input.Options.SubtitleOnly {
		return nil, fmt.Errorf("音视频合并输入无效")
	}
	tracks := []LocalTrack{}
	for _, track := range input.Tracks {
		path, err := localMediaFile(track.Path)
		if err != nil {
			return nil, err
		}
		prefix := strings.ToLower(directory) + string(os.PathSeparator)
		if !strings.HasPrefix(strings.ToLower(path), prefix) {
			return nil, fmt.Errorf("媒体输入不属于该任务")
		}
		tracks = append(tracks, LocalTrack{Path: path, Kind: track.Kind})
	}
	return tracks, nil
}

func muxOutputExtension(options NativeMediaOptions) (bool, string) {
	audio := options.AudioOnly
	extension := options.Container
	if !audio {
		return false, extension
	}
	switch options.AudioFormat {
	case "best":
		extension = "mka"
	case "vorbis":
		extension = "ogg"
	default:
		extension = options.AudioFormat
	}
	return true, extension
}

func muxCover(input MuxInput, directory, extension string) (string, error) {
	if input.Cover == "" {
		return "", nil
	}
	cover, err := localMediaFile(input.Cover)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Dir(cover), directory) {
		return "", fmt.Errorf("封面不属于该任务")
	}
	if !inStringList(extension, []string{"mkv", "mka", "mp4", "m4a", "mp3", "flac"}) {
		return "", fmt.Errorf("此格式不能嵌入封面，请改用 MKV、MP4、M4A、MP3 或 FLAC")
	}
	return cover, nil
}

func muxOutputPath(directory, name, extension string) (string, error) {
	if name != "" {
		if !strings.HasSuffix(name, "."+extension) ||
			layoutNameForbidden.MatchString(name) ||
			name == "." || name == ".." {
			return "", fmt.Errorf("媒体输出名称无效")
		}
		return filepath.Join(directory, name), nil
	}
	return filepath.Join(directory, "result."+extension), nil
}

func prepareMux(input MuxInput) (*muxSetup, error) {
	directory, err := muxDirectory(input.Directory)
	if err != nil {
		return nil, err
	}
	tracks, err := muxTracks(input, directory)
	if err != nil {
		return nil, err
	}
	options := input.Options
	audio, extension := muxOutputExtension(options)
	if !inStringList(extension, []string{"mkv", "mp4", "webm", "mka", "m4a", "mp3", "opus", "flac", "wav", "aac", "ogg"}) {
		return nil, fmt.Errorf("媒体输出格式无效")
	}
	var kept []MediaInterval
	if input.KeepSegments != nil {
		kept, err = CheckedMediaIntervals(input.KeepSegments, input.ExpectedDuration)
		if err != nil {
			return nil, err
		}
	}
	chapters := input.Chapters
	if kept != nil && input.Chapters != nil {
		chapters = RetimeChapters(input.Chapters, kept)
	}
	cover, err := muxCover(input, directory, extension)
	if err != nil {
		return nil, err
	}
	output, err := muxOutputPath(directory, input.OutputName, extension)
	if err != nil {
		return nil, err
	}
	return &muxSetup{
		input: input, directory: directory, tracks: tracks, options: options,
		audio: audio, extension: extension, kept: kept, chapters: chapters, cover: cover, output: output,
	}, nil
}

func appendMuxSubtitles(args []string, subtitles []MuxSubtitle, directory string) ([]string, []MuxSubtitle, error) {
	if len(subtitles) > 100 {
		return nil, nil, fmt.Errorf("字幕轨道过多")
	}
	valid := []MuxSubtitle{}
	for _, subtitle := range subtitles {
		path, err := localMediaFile(subtitle.Path)
		if err != nil {
			return nil, nil, err
		}
		if !strings.EqualFold(filepath.Dir(path), directory) || !muxLanguagePattern.MatchString(subtitle.Language) {
			return nil, nil, fmt.Errorf("字幕文件不属于该任务")
		}
		args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "srt", "-i", path)
		valid = append(valid, subtitle)
	}
	return args, valid, nil
}

func appendMuxChapters(args []string, setup *muxSetup) ([]string, int, error) {
	options := setup.options
	if !options.Chapters || len(setup.chapters) == 0 || (options.ClipEnd != nil && setup.kept == nil) {
		return args, -1, nil
	}
	metadata := []string{}
	for _, chapter := range setup.chapters {
		if !finite(chapter.Start) || !finite(chapter.End) || chapter.Start < 0 ||
			chapter.End <= chapter.Start || chapter.End > setup.input.ExpectedDuration+1 {
			return nil, -1, fmt.Errorf("章节时间无效")
		}
		metadata = append(metadata, fmt.Sprintf(
			"[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n",
			int64(math.Round(chapter.Start*1000)), int64(math.Round(chapter.End*1000)),
			escapeFFMetadata(clipRunes(chapter.Title, 300)),
		))
	}
	path := filepath.Join(setup.directory, "chapters.ffmeta")
	body := ";FFMETADATA1\n" + strings.Join(metadata, "\n")
	if err := writeExclusiveVerified(path, body, "章节恢复文件已改变，请核验后继续"); err != nil {
		return nil, -1, err
	}
	index := len(setup.tracks) + len(setup.input.Subtitles)
	args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "ffmetadata", "-i", path)
	return args, index, nil
}

func muxTrackIndices(tracks []LocalTrack, audio bool) (int, []int, error) {
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
		return -1, nil, fmt.Errorf("没有可用音轨或画面")
	}
	return videoIndex, audioIndices, nil
}

func appendMuxStreams(args []string, setup *muxSetup, plan *muxPlan) ([]string, string, error) {
	options := setup.options
	filterFile := ""
	if setup.kept != nil {
		audioTracks := plan.audioIndices
		if setup.audio {
			audioTracks = plan.audioIndices[:1]
		} else if !options.MergeAudio && len(plan.audioIndices) > 1 {
			audioTracks = plan.audioIndices[:1]
		}
		filters := []string{}
		labels := []string{}
		for rangeIndex, interval := range setup.kept {
			group := []string{}
			if !setup.audio {
				filters = append(filters, fmt.Sprintf(
					"[%d:v:0]trim=start=%s:end=%s,setpts=PTS-STARTPTS[v%d]",
					plan.videoIndex, formatNumber(interval.Start), formatNumber(interval.End), rangeIndex,
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
		if !setup.audio {
			outputs += "[edited_v]"
		}
		for audioIndex := range audioTracks {
			outputs += fmt.Sprintf("[edited_a%d]", audioIndex)
		}
		filters = append(filters, strings.Join(labels, "")+fmt.Sprintf(
			"concat=n=%d:v=%d:a=%d", len(setup.kept), boolInt(!setup.audio), len(audioTracks),
		)+outputs)
		filterFile = filepath.Base(setup.output) + ".filter.txt"
		path := filepath.Join(setup.directory, filterFile)
		body := strings.Join(filters, ";\n")
		if err := writeExclusiveVerified(path, body, "片段拼接恢复文件已改变"); err != nil {
			return nil, "", err
		}
		args = append(args, "-filter_complex_script", path)
		if !setup.audio {
			args = append(args, "-map", "[edited_v]")
		}
		for audioIndex := range audioTracks {
			args = append(args, "-map", fmt.Sprintf("[edited_a%d]", audioIndex))
		}
		if setup.audio && setup.cover == "" {
			args = append(args, "-vn")
		}
		return args, filterFile, nil
	}
	if setup.audio {
		args = append(args, "-map", fmt.Sprintf("%d:a:0", plan.audioIndices[0]))
		if setup.cover == "" {
			args = append(args, "-vn")
		}
		return args, "", nil
	}
	args = append(args, "-map", fmt.Sprintf("%d:v:0", plan.videoIndex))
	selectedAudio := plan.audioIndices
	if !options.MergeAudio && len(plan.audioIndices) > 1 {
		selectedAudio = plan.audioIndices[:1]
	}
	for _, trackIndex := range selectedAudio {
		args = append(args, "-map", fmt.Sprintf("%d:a:0?", trackIndex))
	}
	return args, "", nil
}

func appendMuxCodecs(args []string, setup *muxSetup, plan *muxPlan) []string {
	options := setup.options
	for index, subtitle := range plan.subtitles {
		args = append(args, "-map", fmt.Sprintf("%d:s:0", len(setup.tracks)+index),
			"-metadata:s:s:"+strconv.Itoa(index), "language="+subtitle.Language)
	}
	if len(plan.subtitles) > 0 {
		args = append(args, "-c:s", "srt")
	}
	cut := options.ClipEnd != nil || setup.kept != nil
	if !setup.audio {
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
	if setup.cover != "" {
		if plan.attachedCover {
			args = append(args, "-attach", setup.cover,
				"-metadata:s:t", "mimetype=image/jpeg", "-metadata:s:t", "filename=cover.jpg")
		} else {
			streamIndex := 0
			if !setup.audio {
				streamIndex = 1
			}
			args = append(args, "-map", fmt.Sprintf("%d:v:0", plan.coverInput),
				fmt.Sprintf("-c:v:%d", streamIndex), "mjpeg",
				fmt.Sprintf("-disposition:v:%d", streamIndex), "attached_pic")
		}
	}
	audioCodec := ""
	if setup.audio {
		switch options.AudioFormat {
		case "best":
			if setup.kept != nil {
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
		args = append(args, "-metadata", "title="+clipRunes(muxControlChars.ReplaceAllString(setup.input.Title, " "), 240))
	} else {
		args = append(args, "-map_metadata", "-1")
	}
	if !options.Chapters {
		args = append(args, "-map_chapters", "-1")
	} else if plan.chapterIndex >= 0 {
		args = append(args, "-map_chapters", strconv.Itoa(plan.chapterIndex))
	}
	return append(args, setup.output)
}

func buildMuxPlan(setup *muxSetup) (*muxPlan, error) {
	plan := &muxPlan{chapterIndex: -1, coverInput: -1}
	args := []string{ffmpegHideBanner, ffmpegLogLevel, "error", ffmpegNoStdin, "-n"}
	for _, track := range setup.tracks {
		args = append(args,
			ffmpegProtocolWhitelist, ffmpegFilePipe,
			"-format_whitelist", "mov,matroska,webm,flv,mp3,aac,ogg,wav,flac,mpegts",
			"-i", track.Path,
		)
	}
	var err error
	args, plan.subtitles, err = appendMuxSubtitles(args, setup.input.Subtitles, setup.directory)
	if err != nil {
		return nil, err
	}
	args, plan.chapterIndex, err = appendMuxChapters(args, setup)
	if err != nil {
		return nil, err
	}
	plan.attachedCover = setup.cover != "" && (setup.extension == "mkv" || setup.extension == "mka")
	plan.coverInput = len(setup.tracks) + len(plan.subtitles)
	if plan.chapterIndex >= 0 {
		plan.coverInput++
	}
	if setup.cover != "" && !plan.attachedCover {
		args = append(args, ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "image2", "-i", setup.cover)
	}
	if setup.kept == nil && setup.options.ClipStart != nil && setup.options.ClipEnd != nil {
		args = append(args, "-ss", formatNumber(*setup.options.ClipStart), "-t", formatNumber(*setup.options.ClipEnd-*setup.options.ClipStart))
	}
	plan.videoIndex, plan.audioIndices, err = muxTrackIndices(setup.tracks, setup.audio)
	if err != nil {
		return nil, err
	}
	args, plan.filterFile, err = appendMuxStreams(args, setup, plan)
	if err != nil {
		return nil, err
	}
	plan.args = appendMuxCodecs(args, setup, plan)
	return plan, nil
}

func muxInputFiles(setup *muxSetup, plan *muxPlan) []string {
	inputs := []string{}
	for _, track := range setup.tracks {
		inputs = append(inputs, track.Path)
	}
	for _, subtitle := range plan.subtitles {
		inputs = append(inputs, subtitle.Path)
	}
	if setup.cover != "" {
		inputs = append(inputs, setup.cover)
	}
	return inputs
}

func muxExpectedDuration(setup *muxSetup) float64 {
	expected := setup.input.ExpectedDuration
	if setup.kept != nil {
		expected = 0
		for _, interval := range setup.kept {
			expected += interval.End - interval.Start
		}
	} else if setup.options.ClipStart != nil && setup.options.ClipEnd != nil {
		expected = *setup.options.ClipEnd - *setup.options.ClipStart
	}
	return expected
}

func validateMuxProof(proof MediaProbe, setup *muxSetup, plan *muxPlan, expected float64) error {
	if (!setup.audio && proof.Video == 0) || (setup.audio && proof.Audio == 0) || (len(plan.audioIndices) > 0 && proof.Audio == 0) {
		return fmt.Errorf("成果缺少所选音轨或画面，保留恢复文件")
	}
	if proof.Subtitles != len(plan.subtitles) || (plan.chapterIndex >= 0 && proof.Chapters != len(setup.chapters)) {
		return fmt.Errorf("成果字幕或章节数量不符，保留恢复文件")
	}
	if setup.cover != "" && proof.Covers == 0 {
		return fmt.Errorf("成果未保留封面，恢复文件保留")
	}
	if !finite(expected) || expected <= 0 || math.Abs(proof.Duration-expected) > math.Max(1.5, expected*0.015) {
		return fmt.Errorf("成果时长与所选分集不一致，保留恢复文件")
	}
	return nil
}

func finalizeMux(mux *NativeMediaMux, setup *muxSetup, plan *muxPlan) (MuxResult, error) {
	hashes := []string{}
	for _, path := range muxInputFiles(setup, plan) {
		hash, err := MediaFileHash(path)
		if err != nil {
			return MuxResult{}, err
		}
		hashes = append(hashes, hash)
	}
	planParts := []any{"mux-v1", plan.args, setup.input.ExpectedDuration, setup.chapters}
	if setup.kept != nil {
		planParts = append(planParts, setup.kept)
	}
	for _, hash := range hashes {
		planParts = append(planParts, hash)
	}
	artifact, err := NewOwnedMediaArtifact(setup.directory, filepath.Base(setup.output), hashJSON(planParts))
	if err != nil {
		return MuxResult{}, err
	}
	prepared, err := artifact.Prepare()
	if err != nil {
		return MuxResult{}, err
	}
	if !prepared {
		if _, err := MediaCommand(MediaCommandInput{
			Resources: mux.Resources, Tool: "ffmpeg", Args: plan.args, Ctx: setup.input.Ctx,
			AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
		}); err != nil {
			return MuxResult{}, err
		}
	}
	proof, err := mux.Probe(setup.input.Ctx, setup.output)
	if err != nil {
		return MuxResult{}, err
	}
	if err := validateMuxProof(proof, setup, plan, muxExpectedDuration(setup)); err != nil {
		return MuxResult{}, err
	}
	if _, err := artifact.Finish(); err != nil {
		return MuxResult{}, err
	}
	intermediates := artifact.Intermediates()
	if plan.filterFile != "" {
		intermediates = append(intermediates, plan.filterFile)
	}
	return MuxResult{
		Path: setup.output, Relative: filepath.Base(setup.output), Probe: proof,
		Intermediates: intermediates, Reused: prepared,
	}, nil
}

func (mux *NativeMediaMux) Mux(input MuxInput) (MuxResult, error) {
	setup, err := prepareMux(input)
	if err != nil {
		return MuxResult{}, err
	}
	plan, err := buildMuxPlan(setup)
	if err != nil {
		return MuxResult{}, err
	}
	return finalizeMux(mux, setup, plan)
}

func coverImageExtension(bytes []byte) string {
	switch {
	case len(bytes) >= 3 && bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF:
		return "jpg"
	case len(bytes) >= 8 && string(bytes[:8]) == "\x89PNG\r\n\x1a\n":
		return "png"
	case len(bytes) >= 12 && string(bytes[:4]) == "RIFF" && string(bytes[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

func coverDirectory(directory string) (string, error) {
	base := filepath.Clean(directory)
	info, err := os.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("封面目录无效")
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil || !strings.EqualFold(resolved, base) {
		return "", fmt.Errorf("封面目录无效")
	}
	return base, nil
}

func (mux *NativeMediaMux) coverSize(ctx context.Context, source string) (int, int, error) {
	raw, err := MediaCommand(MediaCommandInput{
		Resources: mux.Resources, Tool: "ffprobe",
		Args: []string{
			"-v", "error", ffmpegProtocolWhitelist, ffmpegFilePipe, "-f", "image2",
			"-show_entries", "stream=width,height", "-of", "json", source,
		},
		Ctx: ctx, AllowSystemFallback: mux.AllowSystemFallback, Platform: mux.Platform,
	})
	if err != nil {
		return 0, 0, err
	}
	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	if json.Unmarshal([]byte(raw), &probe) != nil || len(probe.Streams) == 0 ||
		probe.Streams[0].Width <= 0 || probe.Streams[0].Height <= 0 {
		return 0, 0, fmt.Errorf("封面探测结果无效")
	}
	return probe.Streams[0].Width, probe.Streams[0].Height, nil
}

func coverFilter(crop bool) string {
	filter := "scale=w=min(1200\\,iw):h=min(1200\\,ih):force_original_aspect_ratio=decrease"
	if crop {
		filter = "crop=min(iw\\,ih):min(iw\\,ih)," + filter
	}
	return filter
}

func (mux *NativeMediaMux) PrepareCover(directory string, bytes []byte, crop bool, ctx context.Context) (map[string]any, error) {
	if len(bytes) > 8_000_000 {
		return nil, fmt.Errorf("封面文件过大")
	}
	extension := coverImageExtension(bytes)
	if extension == "" {
		return nil, fmt.Errorf("封面不是可识别的图片")
	}
	base, err := coverDirectory(directory)
	if err != nil {
		return nil, err
	}
	source := filepath.Join(base, "cover-source."+extension)
	output := filepath.Join(base, coverFileName)
	if err := writeExclusiveVerified(source, string(bytes), "封面恢复文件已改变"); err != nil {
		return nil, err
	}
	width, height, err := mux.coverSize(ctx, source)
	if err != nil {
		return nil, err
	}
	if width*height > 40_000_000 {
		return nil, fmt.Errorf("封面图片像素过大")
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
				"-vf", coverFilter(crop), "-frames:v", "1", "-q:v", "2", "-update", "1", output,
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
