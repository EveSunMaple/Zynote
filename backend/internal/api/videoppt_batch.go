package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
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
	if !mediaBilibili.IsNoSubtitle(err) {
		item["status"] = "失败"
		item["error"] = err.Error()
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
