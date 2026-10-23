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

func (runner *Runner) Run(id string, ctx context.Context) (map[string]any, error) {
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
	if options.Sponsorblock != "" {
		err := fmt.Errorf("原生执行器尚未接入片段移除，请关闭 SponsorBlock 后重试")
		runner.fail(id, ctx, err)
		return nil, err
	}
	tuning, err := media.MediaTuning(options.CustomArgs)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	minimum := 0.0
	if value, ok := tuning["--sleep-interval"].(int); ok {
		minimum = float64(value)
	}
	maximum := minimum
	if value, ok := tuning["--max-sleep-interval"].(int); ok {
		maximum = float64(value)
	}
	if maximum < minimum {
		err := fmt.Errorf("最长等待间隔不能小于最短间隔")
		runner.fail(id, ctx, err)
		return nil, err
	}
	if maximum > 0 {
		delay := minimum + (maximum-minimum)*rand.Float64()
		timer := time.NewTimer(time.Duration(delay * float64(time.Second)))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			err := fmt.Errorf("下载等待已取消")
			runner.fail(id, ctx, err)
			return nil, err
		}
	}
	transport, err := media.NewMediaTransport(options)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	defer transport.Close()
	if options.CookieProfile != "" && runner.Auth == nil {
		err := fmt.Errorf("登录档案存储不可用")
		runner.fail(id, ctx, err)
		return nil, err
	}
	fetcher := media.AuthFetcher(transport.Fetch)
	if runner.Auth != nil {
		scoped, err := runner.Auth.Scoped(job.SourceURL, options, fetcher)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		fetcher = scoped
	}
	if options.Downloader == "aria2c" {
		job, err = runner.Jobs.Transition(id, job.Revision, "running", "", "", joinedWarning(job.Warning, "旧 aria2c 设置已改用原生并行分段，不再调用外部下载器"), nil)
		if err != nil {
			return nil, err
		}
	}
	var catalogResult any
	if runner.ScopedCatalog != nil {
		catalogResult, err = runner.ScopedCatalog.ReadWithFetcher(ctx, job.SourceURL, false, options, fetcher)
	} else {
		catalogResult, err = runner.Catalog.Read(job.SourceURL, false, options)
	}
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	source, ok := catalogResult.(media.Source)
	if !ok || source.Identity != job.Identity {
		err := fmt.Errorf("分集身份已改变，请重新读取视频")
		runner.fail(id, ctx, err)
		return nil, err
	}
	directory, err := runner.Jobs.TaskDirectory(id)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	sidecars, err := media.MediaSidecars(directory, source.Description, source.Catalog.URL, tuning)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	if options.SplitChapters && len(source.Chapters) == 0 {
		err := fmt.Errorf("此分集没有站点章节，无法按章节拆分；可指定时间片段")
		runner.fail(id, ctx, err)
		return nil, err
	}
	subtitles := []media.DownloadedSubtitle{}
	if options.Subtitles {
		subtitles, err = media.DownloadSubtitles(source, options, directory, fetcher, ctx)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		automatic := false
		for _, subtitle := range subtitles {
			if subtitle.Automatic {
				automatic = true
				break
			}
		}
		if automatic {
			job, err = runner.Jobs.Transition(id, job.Revision, "running", "字幕已保存", "", joinedWarning(job.Warning, "包含站点自动字幕，可能存在识别错误"), nil)
			if err != nil {
				return nil, err
			}
		}
	}
	coverPath := ""
	coverRelative := ""
	coverIntermediates := []string{}
	if options.Thumbnail || options.EmbedThumbnail {
		if source.Thumbnail == "" {
			err := fmt.Errorf("此视频未提供封面，恢复资料保留")
			runner.fail(id, ctx, err)
			return nil, err
		}
		response, err := fetcher(source.Thumbnail, source.DownloadHeaders, ctx)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		bytes, readErr := io.ReadAll(io.LimitReader(response.Body, 8_000_000+1))
		_ = response.Body.Close()
		if readErr != nil {
			runner.fail(id, ctx, readErr)
			return nil, readErr
		}
		if len(bytes) > 8_000_000 {
			err := fmt.Errorf("封面文件过大")
			runner.fail(id, ctx, err)
			return nil, err
		}
		prepared, err := runner.Mux.PrepareCover(directory, bytes, options.CropArtwork, ctx)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		coverPath, _ = prepared["path"].(string)
		coverRelative, _ = prepared["relative"].(string)
		if values, ok := prepared["intermediates"].([]string); ok {
			coverIntermediates = values
		}
	}
	if options.SubtitleOnly {
		if ctx.Err() != nil {
			err := fmt.Errorf("下载已取消")
			runner.fail(id, ctx, err)
			return nil, err
		}
		names := []string{}
		for _, subtitle := range subtitles {
			names = append(names, subtitle.Relative)
		}
		names = append(names, sidecars...)
		if options.Thumbnail && coverRelative != "" {
			names = append(names, coverRelative)
		}
		layout, err := media.PublishMediaLayout(directory, names, options, job.Created)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		evidence := []media.Receipt{}
		for _, name := range layout.Outputs {
			receipt, err := runner.Jobs.Evidence(id, name)
			if err != nil {
				runner.fail(id, ctx, err)
				return nil, err
			}
			evidence = append(evidence, receipt)
		}
		cleanup := append([]string{}, layout.Cleanup...)
		cleanup = append(cleanup, coverIntermediates...)
		job, err = runner.Jobs.Complete(id, job.Revision, evidence, cleanup)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		remaining, err := runner.Jobs.CleanupCompleted(id)
		if err != nil {
			return nil, err
		}
		cleanupPending := []string{}
		for _, name := range remaining {
			cleanupPending = append(cleanupPending, filepath.Join(directory, name))
		}
		duration := 0.0
		if source.Catalog.Duration != nil {
			duration = *source.Catalog.Duration
		}
		outputPath := filepath.Join(directory, evidence[0].Relative)
		return map[string]any{
			"job": job,
			"output": map[string]any{
				"path": outputPath, "relative": evidence[0].Relative, "duration": duration,
				"video": 0, "audio": 0, "subtitles": len(subtitles), "chapters": 0, "covers": 0,
			},
			"cleanupPending": cleanupPending,
		}, nil
	}
	selected, err := bilibili.SelectStreams(source, options)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	tracks := []media.LocalTrack{}
	transferKeys := []string{}
	concatFiles := []string{}
	buffer := 0
	if value, ok := tuning["--buffer-size"].(int); ok {
		buffer = value
	}
	chunkBytes := int64(0)
	if value, ok := tuning["--http-chunk-size"].(int); ok {
		chunkBytes = int64(value)
	}
	for index, stream := range selected {
		job, err = runner.Jobs.Transition(id, job.Revision, "running",
			fmt.Sprintf("下载音视频轨道 %d/%d", index+1, len(selected)), "", "", nil)
		if err != nil {
			return nil, err
		}
		if stream.Transport == "hls" {
			hlsResult, err := media.DownloadNativeHls(media.HlsInput{
				Directory: directory,
				Key:       fmt.Sprintf("track-%d", index),
				Identity:  job.Identity,
				URL:       stream.URLs[0],
				Options:   options,
				Headers:   source.DownloadHeaders,
				Ctx:       ctx,
				Fetch:     fetcher,
				Check:     func(value string) (string, error) { return bilibili.MediaAddress(value) },
				Progress: func(downloaded int64, total *int64) {
					if runner.Progress != nil {
						runner.Progress(id, downloaded, total)
					}
				},
			})
			if err != nil {
				runner.fail(id, ctx, err)
				return nil, err
			}
			for _, track := range hlsResult.Tracks {
				tracks = append(tracks, media.LocalTrack{Path: track.Path, Kind: track.Kind})
			}
			concatFiles = append(concatFiles, hlsResult.Intermediates...)
			continue
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
			if ctx.Err() != nil {
				err := fmt.Errorf("下载已取消")
				runner.fail(id, ctx, err)
				return nil, err
			}
			key := fmt.Sprintf("track-%d", index)
			if segmented {
				key = fmt.Sprintf("%s-segment-%d", key, segmentIndex)
			}
			identity := fmt.Sprintf("%s:%s:%d", job.Identity, stream.ID, segmentIndex)
			transferKeys = append(transferKeys, key)
			var transfer *media.TransferResult
			var lastErr error
			concurrency := options.ConcurrentFragments
			if options.Downloader == "aria2c" && concurrency < 4 {
				concurrency = 4
			}
			for attempt := 0; attempt <= options.Retries; attempt++ {
				if ctx.Err() != nil {
					break
				}
				address := urls[attempt%len(urls)]
				transfer, lastErr = media.TransferMediaRanges(media.ParallelInput{
					TransferInput: media.TransferInput{
						Directory: directory,
						Key:       key,
						Identity:  identity,
						URL:       address,
						Headers:   source.DownloadHeaders,
						Buffer:    buffer,
						Ctx:       ctx,
						Fetch:     fetcher,
						Progress: func(downloaded int64, total *int64) {
							if runner.Progress != nil {
								runner.Progress(id, downloaded, total)
							}
						},
					},
					Concurrency: concurrency,
					ChunkBytes:  chunkBytes,
					Head:        transport.Head,
				})
				if transfer != nil {
					break
				}
				if attempt < options.Retries {
					delay := time.Duration(500*(attempt+1)) * time.Millisecond
					if delay > 5*time.Second {
						delay = 5 * time.Second
					}
					timer := time.NewTimer(delay)
					select {
					case <-timer.C:
					case <-ctx.Done():
						timer.Stop()
					}
				}
			}
			if transfer == nil {
				if lastErr == nil {
					lastErr = fmt.Errorf("轨道下载失败")
				}
				runner.fail(id, ctx, lastErr)
				return nil, lastErr
			}
			concatFiles = append(concatFiles, transfer.Intermediates...)
			if transfer.Warning != "" {
				job, err = runner.Jobs.Transition(id, job.Revision, "running", "", "", joinedWarning(job.Warning, transfer.Warning), nil)
				if err != nil {
					return nil, err
				}
			}
			paths = append(paths, transfer.Path)
		}
		if segmented {
			concatenated, err := runner.Mux.Concatenate(directory, paths, fmt.Sprintf("track-%d", index), ctx)
			if err != nil {
				runner.fail(id, ctx, err)
				return nil, err
			}
			path, _ := concatenated["path"].(string)
			if values, ok := concatenated["intermediates"].([]string); ok {
				concatFiles = append(concatFiles, values...)
			}
			tracks = append(tracks, media.LocalTrack{Path: path, Kind: stream.Kind})
		} else {
			tracks = append(tracks, media.LocalTrack{Path: paths[0], Kind: stream.Kind})
		}
	}
	if ctx.Err() != nil {
		err := fmt.Errorf("下载已取消")
		runner.fail(id, ctx, err)
		return nil, err
	}
	job, err = runner.Jobs.Transition(id, job.Revision, "running", "合并并核对画面、音轨和完整时长", "", "", nil)
	if err != nil {
		return nil, err
	}
	defaults, _ := media.MediaOptions(map[string]any{})
	extension := options.Container
	if options.AudioOnly {
		switch options.AudioFormat {
		case "best":
			extension = "mka"
		case "vorbis":
			extension = "ogg"
		default:
			extension = options.AudioFormat
		}
	}
	outputName := ""
	if options.FilenameTemplate != defaults.FilenameTemplate || options.RestrictFilenames {
		outputName, err = media.MediaFilename(options, job.Title, source.SourceID, extension)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
	}
	chapters := []media.MuxChapter{}
	for _, chapter := range source.Chapters {
		chapters = append(chapters, media.MuxChapter{Start: chapter.Start, End: chapter.End, Title: chapter.Title})
	}
	duration := 0.0
	if source.Catalog.Duration != nil {
		duration = *source.Catalog.Duration
	}
	embedSubtitles := []media.MuxSubtitle{}
	for _, subtitle := range subtitles {
		if subtitle.EmbedRelative != "" {
			embedSubtitles = append(embedSubtitles, media.MuxSubtitle{
				Path: filepath.Join(directory, subtitle.EmbedRelative), Language: subtitle.Language,
			})
		}
	}
	muxResult, err := runner.Mux.Mux(media.MuxInput{
		Directory: directory, Tracks: tracks, Options: options, ExpectedDuration: duration,
		Title: job.Title, Ctx: ctx, Chapters: chapters, Cover: coverPath, OutputName: outputName,
		Subtitles: embedSubtitles,
	})
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	concatFiles = append(concatFiles, muxResult.Intermediates...)
	chapterOutputs := []string{}
	if options.SplitChapters {
		for index, chapter := range chapters {
			if ctx.Err() != nil {
				break
			}
			chapterOptions := options
			start, end := chapter.Start, chapter.End
			chapterOptions.ClipStart = &start
			chapterOptions.ClipEnd = &end
			chapterOptions.SplitChapters = false
			part, err := runner.Mux.Mux(media.MuxInput{
				Directory: directory, Tracks: tracks, Options: chapterOptions, ExpectedDuration: duration,
				Title: chapter.Title, Ctx: ctx, Chapters: chapters, Cover: coverPath,
				OutputName: fmt.Sprintf("chapter-%03d.%s", index+1, extension), Subtitles: embedSubtitles,
			})
			if err != nil {
				runner.fail(id, ctx, err)
				return nil, err
			}
			chapterOutputs = append(chapterOutputs, part.Relative)
			concatFiles = append(concatFiles, part.Intermediates...)
		}
	}
	if ctx.Err() != nil {
		err := fmt.Errorf("下载已取消")
		runner.fail(id, ctx, err)
		return nil, err
	}
	names := []string{muxResult.Relative}
	names = append(names, chapterOutputs...)
	names = append(names, sidecars...)
	keepSubtitles := options.KeepSubtitles || !options.EmbedSubtitles
	if keepSubtitles {
		for _, subtitle := range subtitles {
			names = append(names, subtitle.Relative)
		}
	}
	if options.Thumbnail && coverRelative != "" {
		names = append(names, coverRelative)
	}
	layout, err := media.PublishMediaLayout(directory, names, options, job.Created)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	evidence := []media.Receipt{}
	for _, name := range layout.Outputs {
		receipt, err := runner.Jobs.Evidence(id, name)
		if err != nil {
			runner.fail(id, ctx, err)
			return nil, err
		}
		evidence = append(evidence, receipt)
	}
	cleanup := []string{}
	for _, key := range transferKeys {
		cleanup = append(cleanup, key+".partial", key+".resume.json", key+".resume.json.writing")
	}
	cleanup = append(cleanup, "chapters.ffmeta")
	cleanup = append(cleanup, concatFiles...)
	cleanup = append(cleanup, coverIntermediates...)
	if coverRelative != "" && !options.Thumbnail {
		cleanup = append(cleanup, coverRelative)
	}
	cleanup = append(cleanup, layout.Cleanup...)
	for _, subtitle := range subtitles {
		if subtitle.EmbedRelative != "" {
			cleanup = append(cleanup, subtitle.EmbedRelative)
		}
		if !keepSubtitles {
			cleanup = append(cleanup, subtitle.Relative)
		}
	}
	job, err = runner.Jobs.Complete(id, job.Revision, evidence, cleanup)
	if err != nil {
		runner.fail(id, ctx, err)
		return nil, err
	}
	remaining, err := runner.Jobs.CleanupCompleted(id)
	if err != nil {
		return nil, err
	}
	cleanupPending := []string{}
	for _, name := range remaining {
		cleanupPending = append(cleanupPending, filepath.Join(directory, name))
	}
	outputPath := filepath.Join(directory, evidence[0].Relative)
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
