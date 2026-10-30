package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const msgInvalidEpisode = "所选分集无效"

func selectedEpisodes(series mediaBilibili.VideoSeries, pages []int) ([]mediaBilibili.VideoEpisode, error) {
	if len(pages) < 1 || len(pages) > 1000 {
		return nil, contract.InputInvalid(msgInvalidEpisode)
	}
	seen := map[int]bool{}
	selected := []mediaBilibili.VideoEpisode{}
	for _, page := range pages {
		if seen[page] {
			return nil, contract.InputInvalid(msgInvalidEpisode)
		}
		seen[page] = true
		found := false
		for _, episode := range series.Episodes {
			if episode.Page == page {
				selected = append(selected, episode)
				found = true
				break
			}
		}
		if !found {
			return nil, contract.InputInvalid(msgInvalidEpisode)
		}
	}
	return selected, nil
}

func (inv *Invoker) transcribeEpisode(ctx context.Context, store contract.StateStore, state *contract.State, episode mediaBilibili.VideoEpisode, courseName string) (string, error) {
	videoID, page, _, err := biliTaskName(episode.URL, "")
	if err != nil {
		return "", err
	}
	task, err := videoPptService(state).CreateBlankTask(videoID+"-P"+page+"-字幕转写", map[string]any{
		"kind": "subtitle", "outputName": videoID + "-P" + page + "-离线转写.srt",
		"courseName": courseName, "url": episode.URL, "page": atoiSafe(page),
	})
	if err != nil {
		return "", err
	}
	if err := inv.downloadBiliVideo(ctx, store, state, episode.URL, task.Path); err != nil {
		return "", err
	}
	result, err := inv.transcribeVideoTask(ctx, state, task)
	if err != nil {
		return "", err
	}
	return textValue(result.(map[string]any)["savedPath"]), nil
}

func (inv *Invoker) batchSubtitleItem(ctx context.Context, store contract.StateStore, state *contract.State, episode mediaBilibili.VideoEpisode, courseName string) map[string]any {
	item := map[string]any{"page": episode.Page, "title": episode.Title}
	subtitle, err := inv.fetchVideoSubtitles(ctx, store, state, episode.URL)
	if err == nil {
		path, saveErr := saveVideoSubtitleFile(state, episode.URL, episode.Title, subtitle.SRT, courseName)
		if saveErr != nil {
			item["status"] = "失败"
			item["error"] = saveErr.Error()
		} else {
			item["status"] = "已完成"
			item["path"] = path
		}
		return item
	}
	inv.videoProgress(fmt.Sprintf("P%d 无可用站内字幕，正在下载并离线转写", episode.Page))
	path, err := inv.transcribeEpisode(ctx, store, state, episode, courseName)
	if err != nil {
		item["status"] = "失败"
		item["error"] = err.Error()
		return item
	}
	item["status"] = "已完成"
	item["path"] = path
	return item
}

func (inv *Invoker) handleVideoBatchSubtitles(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL      string `json:"url"`
		Pages    []int  `json:"pages"`
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid(msgInvalidEpisode)
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	return inv.runJob("video-subtitles", func(ctx context.Context) (any, error) {
		series, err := inv.fetchVideoSeries(ctx, store, state, input.URL)
		if err != nil {
			return nil, err
		}
		selected, err := selectedEpisodes(series, input.Pages)
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for index, episode := range selected {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			inv.videoProgress(fmt.Sprintf("正在取得所选字幕 %d/%d · P%d %s", index+1, len(selected), episode.Page, episode.Title))
			items = append(items, inv.batchSubtitleItem(ctx, store, state, episode, courseName))
		}
		return items, nil
	})
}

func (inv *Invoker) processBatchEpisode(ctx context.Context, store contract.StateStore, state *contract.State, episode mediaBilibili.VideoEpisode, prompt, courseName string, item map[string]any) error {
	task, err := videoPptService(state).CreateBlankTask(fmt.Sprintf("P%03d-%s", episode.Page, episode.Title), nil)
	if err != nil {
		return err
	}
	if err := inv.downloadBiliVideo(ctx, store, state, episode.URL, task.Path); err != nil {
		return err
	}
	chat, err := inv.videoChat(store, state)
	if err != nil {
		return err
	}
	prepared, err := videoProcessor(state).Prepare(ctx, task, chat, inv.videoProgress)
	if err != nil {
		return err
	}
	if prepared.Source.Warning != "" {
		return contract.InputInvalid(prepared.Source.Warning)
	}
	review, err := videoProcessor(state).Extract(ctx, task, prepared.Source.Crop, prompt, chat, inv.videoProgress)
	if err != nil {
		return err
	}
	if review.Warning != "" {
		return contract.InputInvalid(review.Warning)
	}
	choices := make([]videoppt.Choice, 0, len(review.Pages))
	for _, page := range review.Pages {
		choices = append(choices, videoppt.Choice{Page: page.Page, Keep: page.Keep, Crop: page.Crop})
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
	if err != nil {
		return err
	}
	result, _, err := videoProcessor(state).Export(ctx, task, choices, directory)
	if err != nil {
		return err
	}
	if err := videoPptService(state).DiscardCompleted(task.ID); err != nil {
		item["error"] = err.Error()
	}
	item["path"] = result.Path
	return nil
}

func (inv *Invoker) handleVideoBatch(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) { // NOSONAR: retained cohesive flow; decomposition tracked separately to avoid behavior regressions.
	var input struct {
		URL      string `json:"url"`
		Pages    []int  `json:"pages"`
		CourseID string `json:"courseId"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid(msgInvalidEpisode)
	}
	prompt := input.Prompt
	if prompt == "" {
		prompt = defaultVideoPrompt
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	return inv.runJob("video-batch", func(ctx context.Context) (any, error) {
		series, err := inv.fetchVideoSeries(ctx, store, state, input.URL)
		if err != nil {
			return nil, err
		}
		selected, err := selectedEpisodes(series, input.Pages)
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(selected))
		for _, episode := range selected {
			items = append(items, map[string]any{"page": episode.Page, "title": episode.Title, "status": "等待处理"})
		}
		notify := func() {
			inv.emit(map[string]any{"kind": "video-batch", "items": items})
		}
		notify()
		for index, episode := range selected {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			item := items[index]
			item["status"] = "处理中"
			notify()
			inv.videoProgress(fmt.Sprintf("正在处理所选课程 %d/%d · P%d %s", index+1, len(selected), episode.Page, episode.Title))
			err := inv.processBatchEpisode(ctx, store, state, episode, prompt, courseName, item)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				item["status"] = "失败"
				item["error"] = err.Error()
			} else {
				item["status"] = "已完成"
			}
			notify()
		}
		return items, nil
	})
}
