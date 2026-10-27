package download

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type CatalogReader interface {
	Read(input string, playlist bool, options any) (any, error)
}

type ScopedCatalogReader interface {
	ReadWithFetcher(ctx context.Context, input string, playlist bool, options any, fetcher media.AuthFetcher) (any, error)
}

func joinedWarning(parts ...string) string {
	result := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			result = append(result, part)
		}
	}
	return strings.Join(result, "；")
}

type Runner struct {
	Jobs          *media.DownloadJobs
	Auth          *media.MediaAuth
	Mux           *media.NativeMediaMux
	Catalog       CatalogReader
	ScopedCatalog ScopedCatalogReader
	Progress      func(id string, downloaded int64, total *int64)
}

func (runner *Runner) fail(id string, ctx context.Context, err error) {
	current, readErr := runner.Jobs.Read(id)
	if readErr != nil || current == nil || current.State != "running" {
		return
	}
	if ctx.Err() != nil {
		_, _ = runner.Jobs.Transition(id, current.Revision, "cancelled", "已取消，恢复材料保留", "", "", nil)
		return
	}
	_, _ = runner.Jobs.Transition(id, current.Revision, "error", "未发布成果，恢复材料保留", err.Error(), "", nil)
}

type runState struct {
	runner             *Runner
	id                 string
	ctx                context.Context // NOSONAR: request-scoped context is owned by the caller for a single operation.
	job                *media.NativeDownloadJob
	options            media.NativeMediaOptions
	tuning             map[string]any
	transport          *media.MediaTransport
	fetcher            media.AuthFetcher
	source             media.Source
	directory          string
	sidecars           []string
	keepSegments       []media.MediaInterval
	subtitles          []media.DownloadedSubtitle
	tracks             []media.LocalTrack
	coverPath          string
	coverRelative      string
	coverIntermediates []string
	transferKeys       []string
	concatFiles        []string
}

func (state *runState) fail(err error) error {
	state.runner.fail(state.id, state.ctx, err)
	return err
}

func runnerDelay(ctx context.Context, tuning map[string]any) error {
	minimum := 0.0
	if value, ok := tuning["--sleep-interval"].(int); ok {
		minimum = float64(value)
	}
	maximum := minimum
	if value, ok := tuning["--max-sleep-interval"].(int); ok {
		maximum = float64(value)
	}
	if maximum < minimum {
		return fmt.Errorf("最长等待间隔不能小于最短间隔")
	}
	if maximum <= 0 {
		return nil
	}
	delay := minimum + (maximum-minimum)*rand.Float64() // NOSONAR: download retry jitter only; never used for security decisions.
	timer := time.NewTimer(time.Duration(delay * float64(time.Second)))
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		timer.Stop()
		return fmt.Errorf("下载等待已取消")
	}
}

func (runner *Runner) prepareRun(id string, ctx context.Context) (*runState, error) {
	job, err := runner.Jobs.Read(id)
	if err != nil {
		return nil, err
	}
	if job.State != "queued" {
		return nil, fmt.Errorf("任务不在等待队列中")
	}
	options, err := media.MediaOptions(job.Recipe)
	if err != nil {
		return nil, err
	}
	job, err = runner.Jobs.Transition(id, job.Revision, "running", "重新核对分集身份", "", "", nil)
	if err != nil {
		return nil, err
	}
	state := &runState{runner: runner, id: id, ctx: ctx, job: job, options: options}
	tuning, err := media.MediaTuning(options.CustomArgs)
	if err != nil {
		return state, state.fail(err)
	}
	state.tuning = tuning
	if err := runnerDelay(ctx, tuning); err != nil {
		return state, state.fail(err)
	}
	transport, err := media.NewMediaTransport(options)
	if err != nil {
		return state, state.fail(err)
	}
	state.transport = transport
	if options.CookieProfile != "" && runner.Auth == nil {
		return state, state.fail(fmt.Errorf("登录档案存储不可用"))
	}
	fetcher := media.AuthFetcher(transport.Fetch)
	if runner.Auth != nil {
		scoped, err := runner.Auth.Scoped(job.SourceURL, options, fetcher)
		if err != nil {
			return state, state.fail(err)
		}
		fetcher = scoped
	}
	state.fetcher = fetcher
	if options.Downloader == "aria2c" {
		state.job, err = runner.Jobs.Transition(id, state.job.Revision, "running", "", "", joinedWarning(state.job.Warning, "旧 aria2c 设置已改用原生并行分段，不再调用外部下载器"), nil)
		if err != nil {
			return state, err
		}
	}
	var catalogResult any
	if runner.ScopedCatalog != nil {
		catalogResult, err = runner.ScopedCatalog.ReadWithFetcher(ctx, state.job.SourceURL, false, options, fetcher)
	} else {
		catalogResult, err = runner.Catalog.Read(state.job.SourceURL, false, options)
	}
	if err != nil {
		return state, state.fail(err)
	}
	source, ok := catalogResult.(media.Source)
	if !ok || source.Identity != state.job.Identity {
		return state, state.fail(fmt.Errorf("分集身份已改变，请重新读取视频"))
	}
	state.source = source
	directory, err := runner.Jobs.TaskDirectory(id)
	if err != nil {
		return state, state.fail(err)
	}
	state.directory = directory
	return state, nil
}

func (state *runState) prepareSidecarsAndSegments() error {
	sidecars, err := media.MediaSidecars(state.directory, state.source.Description, state.source.Catalog.URL, state.tuning)
	if err != nil {
		return state.fail(err)
	}
	state.sidecars = sidecars
	if state.options.Sponsorblock != "" {
		plan, err := media.SponsorBlockPlan(state.source, state.options, state.directory, state.fetcher, state.ctx)
		if err != nil {
			return state.fail(err)
		}
		state.sidecars = append(state.sidecars, plan.Relative)
		state.job, err = state.runner.Jobs.Transition(state.id, state.job.Revision, "running", "已核验片段移除计划", "", joinedWarning(state.job.Warning, plan.Warning), nil)
		if err != nil {
			return err
		}
		if len(plan.Removed) > 0 || state.options.ClipStart != nil {
			state.keepSegments = plan.Kept
		}
	}
	if state.options.SplitChapters && len(state.source.Chapters) == 0 {
		return state.fail(fmt.Errorf("此分集没有站点章节，无法按章节拆分；可指定时间片段"))
	}
	return nil
}

func (state *runState) prepareSubtitles() error {
	if !state.options.Subtitles {
		return nil
	}
	subtitles, err := media.DownloadSubtitles(state.source, state.options, state.directory, state.fetcher, state.ctx, state.keepSegments)
	if err != nil {
		return state.fail(err)
	}
	state.subtitles = subtitles
	automatic := false
	for _, subtitle := range subtitles {
		if subtitle.Automatic {
			automatic = true
			break
		}
	}
	if automatic {
		state.job, err = state.runner.Jobs.Transition(state.id, state.job.Revision, "running", "字幕已保存", "", joinedWarning(state.job.Warning, "包含站点自动字幕，可能存在识别错误"), nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (state *runState) prepareCover() error {
	if !state.options.Thumbnail && !state.options.EmbedThumbnail {
		return nil
	}
	if state.source.Thumbnail == "" {
		return state.fail(fmt.Errorf("此视频未提供封面，恢复资料保留"))
	}
	response, err := state.fetcher(state.source.Thumbnail, state.source.DownloadHeaders, state.ctx)
	if err != nil {
		return state.fail(err)
	}
	bytes, readErr := io.ReadAll(io.LimitReader(response.Body, 8_000_000+1))
	_ = response.Body.Close()
	if readErr != nil {
		return state.fail(readErr)
	}
	if len(bytes) > 8_000_000 {
		return state.fail(fmt.Errorf("封面文件过大"))
	}
	prepared, err := state.runner.Mux.PrepareCover(state.directory, bytes, state.options.CropArtwork, state.ctx)
	if err != nil {
		return state.fail(err)
	}
	state.coverPath, _ = prepared["path"].(string)
	state.coverRelative, _ = prepared["relative"].(string)
	if values, ok := prepared["intermediates"].([]string); ok {
		state.coverIntermediates = values
	}
	return nil
}

func (state *runState) prepareMedia() error {
	if err := state.prepareSidecarsAndSegments(); err != nil {
		return err
	}
	if err := state.prepareSubtitles(); err != nil {
		return err
	}
	return state.prepareCover()
}

func (state *runState) completePublished(names []string, extraCleanup []string) (*media.NativeDownloadJob, []media.Receipt, []string, error) { // NOSONAR: path handling matches the platform API contract.
	layout, err := media.PublishMediaLayout(state.directory, names, state.options, state.job.Created)
	if err != nil {
		return nil, nil, nil, state.fail(err)
	}
	evidence := []media.Receipt{}
	for _, name := range layout.Outputs {
		receipt, err := state.runner.Jobs.Evidence(state.id, name)
		if err != nil {
			return nil, nil, nil, state.fail(err)
		}
		evidence = append(evidence, receipt)
	}
	cleanup := append([]string{}, layout.Cleanup...)
	cleanup = append(cleanup, extraCleanup...)
	job, err := state.runner.Jobs.Complete(state.id, state.job.Revision, evidence, cleanup)
	if err != nil {
		return nil, nil, nil, state.fail(err)
	}
	remaining, err := state.runner.Jobs.CleanupCompleted(state.id)
	if err != nil {
		return nil, nil, nil, err
	}
	cleanupPending := []string{}
	for _, name := range remaining {
		cleanupPending = append(cleanupPending, filepath.Join(state.directory, name))
	}
	return job, evidence, cleanupPending, nil
}

func (state *runState) subtitleOnlyResult() (map[string]any, error) {
	if state.ctx.Err() != nil {
		return nil, state.fail(fmt.Errorf("下载已取消"))
	}
	names := []string{}
	for _, subtitle := range state.subtitles {
		names = append(names, subtitle.Relative)
	}
	names = append(names, state.sidecars...)
	if state.options.Thumbnail && state.coverRelative != "" {
		names = append(names, state.coverRelative)
	}
	job, evidence, cleanupPending, err := state.completePublished(names, state.coverIntermediates)
	if err != nil {
		return nil, err
	}
	duration := 0.0
	if state.source.Catalog.Duration != nil {
		duration = *state.source.Catalog.Duration
	}
	outputPath := filepath.Join(state.directory, evidence[0].Relative)
	return map[string]any{
		"job": job,
		"output": map[string]any{
			"path": outputPath, "relative": evidence[0].Relative, "duration": duration,
			"video": 0, "audio": 0, "subtitles": len(state.subtitles), "chapters": 0, "covers": 0,
		},
		"cleanupPending": cleanupPending,
	}, nil
}

func (state *runState) downloadSegment(key, identity string, urls []string, concurrency int, buffer int, chunkBytes int64) (*media.TransferResult, error) {
	state.transferKeys = append(state.transferKeys, key)
	var transfer *media.TransferResult
	var lastErr error
	for attempt := 0; attempt <= state.options.Retries; attempt++ {
		if state.ctx.Err() != nil {
			break
		}
		address := urls[attempt%len(urls)]
		transfer, lastErr = media.TransferMediaRanges(media.ParallelInput{
			TransferInput: media.TransferInput{
				Directory: state.directory,
				Key:       key,
				Identity:  identity,
				URL:       address,
				Headers:   state.source.DownloadHeaders,
				Buffer:    buffer,
				Ctx:       state.ctx,
				Fetch:     state.fetcher,
				Progress: func(downloaded int64, total *int64) {
					if state.runner.Progress != nil {
						state.runner.Progress(state.id, downloaded, total)
					}
				},
			},
			Concurrency: concurrency,
			ChunkBytes:  chunkBytes,
			Head:        state.transport.Head,
		})
		if transfer != nil {
			break
		}
		if attempt < state.options.Retries {
			delay := time.Duration(500*(attempt+1)) * time.Millisecond
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-state.ctx.Done():
				timer.Stop()
			}
		}
	}
	if transfer == nil {
		if lastErr == nil {
			lastErr = fmt.Errorf("轨道下载失败")
		}
		return nil, state.fail(lastErr)
	}
	state.concatFiles = append(state.concatFiles, transfer.Intermediates...)
	if transfer.Warning != "" {
		var err error
		state.job, err = state.runner.Jobs.Transition(state.id, state.job.Revision, "running", "", "", joinedWarning(state.job.Warning, transfer.Warning), nil)
		if err != nil {
			return nil, err
		}
	}
	return transfer, nil
}

func (state *runState) downloadStream(index int, stream media.MediaStream, buffer int, chunkBytes int64) error { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	if stream.Transport == "hls" {
		hlsResult, err := media.DownloadNativeHls(media.HlsInput{
			Directory: state.directory,
			Key:       fmt.Sprintf("track-%d", index),
			Identity:  state.job.Identity,
			URL:       stream.URLs[0],
			Options:   state.options,
			Headers:   state.source.DownloadHeaders,
			Ctx:       state.ctx,
			Fetch:     state.fetcher,
			Check:     func(value string) (string, error) { return bilibili.MediaAddress(value) },
			Progress: func(downloaded int64, total *int64) {
				if state.runner.Progress != nil {
					state.runner.Progress(state.id, downloaded, total)
				}
			},
		})
		if err != nil {
			return state.fail(err)
		}
		for _, track := range hlsResult.Tracks {
			state.tracks = append(state.tracks, media.LocalTrack{Path: track.Path, Kind: track.Kind})
		}
		state.concatFiles = append(state.concatFiles, hlsResult.Intermediates...)
		return nil
	}
	segmented := stream.Kind == "combined" && len(stream.URLs) > 1
	segments := [][]string{{stream.URLs[0]}}
	if segmented {
		segments = [][]string{}
		for _, address := range stream.URLs {
			segments = append(segments, []string{address})
		}
	}
	paths := []string{}
	for segmentIndex, urls := range segments {
		if state.ctx.Err() != nil {
			return state.fail(fmt.Errorf("下载已取消"))
		}
		key := fmt.Sprintf("track-%d", index)
		if segmented {
			key = fmt.Sprintf("%s-segment-%d", key, segmentIndex)
		}
		identity := fmt.Sprintf("%s:%s:%d", state.job.Identity, stream.ID, segmentIndex)
		concurrency := state.options.ConcurrentFragments
		if state.options.Downloader == "aria2c" && concurrency < 4 {
			concurrency = 4
		}
		transfer, err := state.downloadSegment(key, identity, urls, concurrency, buffer, chunkBytes)
		if err != nil {
			return err
		}
		paths = append(paths, transfer.Path)
	}
	if segmented {
		concatenated, err := state.runner.Mux.Concatenate(state.directory, paths, fmt.Sprintf("track-%d", index), state.ctx)
		if err != nil {
			return state.fail(err)
		}
		path, _ := concatenated["path"].(string)
		if values, ok := concatenated["intermediates"].([]string); ok {
			state.concatFiles = append(state.concatFiles, values...)
		}
		state.tracks = append(state.tracks, media.LocalTrack{Path: path, Kind: stream.Kind})
		return nil
	}
	state.tracks = append(state.tracks, media.LocalTrack{Path: paths[0], Kind: stream.Kind})
	return nil
}

func (state *runState) downloadTracks() error {
	selected, err := bilibili.SelectStreams(state.source, state.options)
	if err != nil {
		return state.fail(err)
	}
	buffer := 0
	if value, ok := state.tuning["--buffer-size"].(int); ok {
		buffer = value
	}
	chunkBytes := int64(0)
	if value, ok := state.tuning["--http-chunk-size"].(int); ok {
		chunkBytes = int64(value)
	}
	for index, stream := range selected {
		state.job, err = state.runner.Jobs.Transition(state.id, state.job.Revision, "running",
			fmt.Sprintf("下载音视频轨道 %d/%d", index+1, len(selected)), "", "", nil)
		if err != nil {
			return err
		}
		if err := state.downloadStream(index, stream, buffer, chunkBytes); err != nil {
			return err
		}
	}
	if state.ctx.Err() != nil {
		return state.fail(fmt.Errorf("下载已取消"))
	}
	return nil
}

func (state *runState) muxOutput() (media.MuxResult, []string, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	var err error
	state.job, err = state.runner.Jobs.Transition(state.id, state.job.Revision, "running", "合并并核对画面、音轨和完整时长", "", "", nil)
	if err != nil {
		return media.MuxResult{}, nil, err
	}
	defaults, _ := media.MediaOptions(map[string]any{})
	extension := state.options.Container
	if state.options.AudioOnly {
		switch state.options.AudioFormat {
		case "best":
			extension = "mka"
		case "vorbis":
			extension = "ogg"
		default:
			extension = state.options.AudioFormat
		}
	}
	outputName := ""
	if state.options.FilenameTemplate != defaults.FilenameTemplate || state.options.RestrictFilenames {
		outputName, err = media.MediaFilename(state.options, state.job.Title, state.source.SourceID, extension)
		if err != nil {
			return media.MuxResult{}, nil, state.fail(err)
		}
	}
	chapters := []media.MuxChapter{}
	for _, chapter := range state.source.Chapters {
		chapters = append(chapters, media.MuxChapter{Start: chapter.Start, End: chapter.End, Title: chapter.Title})
	}
	duration := 0.0
	if state.source.Catalog.Duration != nil {
		duration = *state.source.Catalog.Duration
	}
	embedSubtitles := []media.MuxSubtitle{}
	for _, subtitle := range state.subtitles {
		if subtitle.EmbedRelative != "" {
			embedSubtitles = append(embedSubtitles, media.MuxSubtitle{
				Path: filepath.Join(state.directory, subtitle.EmbedRelative), Language: subtitle.Language,
			})
		}
	}
	muxResult, err := state.runner.Mux.Mux(media.MuxInput{
		Directory: state.directory, Tracks: state.tracks, Options: state.options, ExpectedDuration: duration,
		Title: state.job.Title, Ctx: state.ctx, Chapters: chapters, Cover: state.coverPath, OutputName: outputName,
		Subtitles: embedSubtitles, KeepSegments: state.keepSegments,
	})
	if err != nil {
		return media.MuxResult{}, nil, state.fail(err)
	}
	chapterOutputs := []string{}
	if state.options.SplitChapters {
		for index, chapter := range chapters {
			if state.ctx.Err() != nil {
				break
			}
			chapterOptions := state.options
			start, end := chapter.Start, chapter.End
			chapterOptions.ClipStart = &start
			chapterOptions.ClipEnd = &end
			chapterOptions.SplitChapters = false
			part, err := state.runner.Mux.Mux(media.MuxInput{
				Directory: state.directory, Tracks: state.tracks, Options: chapterOptions, ExpectedDuration: duration,
				Title: chapter.Title, Ctx: state.ctx, Chapters: chapters, Cover: state.coverPath,
				OutputName: fmt.Sprintf("chapter-%03d.%s", index+1, extension), Subtitles: embedSubtitles,
			})
			if err != nil {
				return media.MuxResult{}, nil, state.fail(err)
			}
			chapterOutputs = append(chapterOutputs, part.Relative)
			state.concatFiles = append(state.concatFiles, part.Intermediates...)
		}
	}
	return muxResult, chapterOutputs, nil
}

func (state *runState) finish(muxResult media.MuxResult, chapterOutputs []string) (map[string]any, error) {
	if state.ctx.Err() != nil {
		return nil, state.fail(fmt.Errorf("下载已取消"))
	}
	names := []string{muxResult.Relative}
	names = append(names, chapterOutputs...)
	names = append(names, state.sidecars...)
	keepSubtitles := state.options.KeepSubtitles || !state.options.EmbedSubtitles
	if keepSubtitles {
		for _, subtitle := range state.subtitles {
			names = append(names, subtitle.Relative)
		}
	}
	if state.options.Thumbnail && state.coverRelative != "" {
		names = append(names, state.coverRelative)
	}
	cleanup := []string{}
	for _, key := range state.transferKeys {
		cleanup = append(cleanup, key+".partial", key+".resume.json", key+".resume.json.writing")
	}
	cleanup = append(cleanup, "chapters.ffmeta")
	cleanup = append(cleanup, state.concatFiles...)
	cleanup = append(cleanup, state.coverIntermediates...)
	if state.coverRelative != "" && !state.options.Thumbnail {
		cleanup = append(cleanup, state.coverRelative)
	}
	for _, subtitle := range state.subtitles {
		if subtitle.EmbedRelative != "" {
			cleanup = append(cleanup, subtitle.EmbedRelative)
		}
		if !keepSubtitles {
			cleanup = append(cleanup, subtitle.Relative)
		}
	}
	job, evidence, cleanupPending, err := state.completePublished(names, cleanup)
	if err != nil {
		return nil, err
	}
	outputPath := filepath.Join(state.directory, evidence[0].Relative)
	return map[string]any{
		"job": job,
		"output": map[string]any{
			"path": outputPath, "relative": evidence[0].Relative, "duration": muxResult.Probe.Duration,
			"video": muxResult.Probe.Video, "audio": muxResult.Probe.Audio,
			"subtitles": muxResult.Probe.Subtitles, "chapters": muxResult.Probe.Chapters,
			"covers": muxResult.Probe.Covers,
		},
		"cleanupPending": cleanupPending,
	}, nil
}

func (runner *Runner) Run(id string, ctx context.Context) (map[string]any, error) {
	state, err := runner.prepareRun(id, ctx)
	if state != nil && state.transport != nil {
		defer state.transport.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := state.prepareMedia(); err != nil {
		return nil, err
	}
	if state.options.SubtitleOnly {
		return state.subtitleOnlyResult()
	}
	if err := state.downloadTracks(); err != nil {
		return nil, err
	}
	muxResult, chapterOutputs, err := state.muxOutput()
	if err != nil {
		return nil, err
	}
	return state.finish(muxResult, chapterOutputs)
}
