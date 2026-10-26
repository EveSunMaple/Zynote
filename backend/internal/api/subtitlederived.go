package api

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlesync"
)

func subtitleDerivedService(state *contract.State) *subtitlederived.Service {
	return subtitlederived.New(
		filepath.Join(state.LibraryRoot, "笔记生成记录"), state.LibraryRoot,
	)
}

func (inv *Invoker) subtitleDerivedDependencies(store contract.StateStore, state *contract.State) subtitlederived.Dependencies {
	knowledgeStore, _ := inv.knowledgeStoreFor(state)
	var client *ai.Client
	getClient := func() (*ai.Client, error) {
		if client != nil {
			return client, nil
		}
		profile, err := config.ActiveProfile(state.DataRoot, store.Reverse())
		if err != nil {
			return nil, err
		}
		client = ai.New(ai.Profile{
			BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
			APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
		})
		return client, nil
	}
	return subtitlederived.Dependencies{
		Generation: func(ctx context.Context, url string) (string, error) {
			if knowledgeStore == nil {
				return "", nil
			}
			value, err := knowledgeStore.Generation(url)
			if err != nil || value == nil {
				return "", nil
			}
			if text, ok := value.(string); ok {
				return text, nil
			}
			return "", nil
		},
		Summarize: func(ctx context.Context, srt, mode string) (string, []subtitlederived.Point, error) {
			active, err := getClient()
			if err != nil {
				return "", nil, err
			}
			result, err := summarizeVideoText(ctx, active, srt, mode, inv.videoProgress)
			if err != nil {
				return "", nil, err
			}
			points := make([]subtitlederived.Point, 0, len(result.Points))
			for _, point := range result.Points {
				points = append(points, subtitlederived.Point{
					Cue: float64(point.Cue), Quote: point.Quote, Point: point.Point,
					Topic: point.Topic, Timestamp: point.Timestamp,
				})
			}
			return result.Markdown, points, nil
		},
		RenderSVG: func(title string, points []subtitlederived.Point) string {
			converted := make([]notePoint, 0, len(points))
			for _, point := range points {
				converted = append(converted, notePoint{
					Cue: point.Cue, Quote: point.Quote, Point: point.Point,
					Topic: point.Topic, Timestamp: point.Timestamp,
				})
			}
			return notesSVG(title, converted)
		},
		Progress: inv.videoProgress,
	}
}

func (inv *Invoker) handleSubtitleDerivedList(state *contract.State) (any, error) {
	tasks, err := subtitleDerivedService(state).List()
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return tasks, nil
}

func (inv *Invoker) handleSubtitleDerivedResume(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("笔记任务标识无效")
	}
	return inv.runJob("批量课程笔记", func(ctx context.Context) (any, error) {
		return subtitleDerivedService(state).Run(ctx, input.ID, inv.subtitleDerivedDependencies(store, state))
	})
}

func (inv *Invoker) handleSubtitleDerivedStart(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID    string   `json:"id"`
		URLs  []string `json:"urls"`
		Modes []string `json:"modes"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) ||
		len(input.URLs) < 1 || len(input.URLs) > 1000 || len(input.Modes) < 1 || len(input.Modes) > 4 {
		return nil, contract.InputInvalid("笔记任务参数无效")
	}
	return inv.runJob("批量课程笔记", func(ctx context.Context) (any, error) {
		tasks, err := subtitleSyncService(state).List()
		if err != nil {
			return nil, contract.Unavailable(err.Error())
		}
		var syncTask *subtitlesync.Task
		for index := range tasks {
			if tasks[index].ID == input.ID {
				syncTask = &tasks[index]
				break
			}
		}
		if syncTask == nil {
			return nil, contract.InputInvalid("同步记录不存在")
		}
		locators := []string{}
		seen := map[string]bool{}
		for _, value := range input.URLs {
			locator, err := subtitlesync.Locator(value)
			if err != nil {
				return nil, err
			}
			if seen[locator] {
				return nil, contract.InputInvalid("不要重复选择分集")
			}
			seen[locator] = true
			locators = append(locators, locator)
		}
		sources := []subtitlederived.Source{}
		for _, locator := range locators {
			found := false
			for _, item := range syncTask.Items {
				if item.URL != locator {
					continue
				}
				if item.Status != "已完成" || item.Path == "" || item.Hash == "" || item.Generation == nil {
					return nil, contract.InputInvalid("请选择已成功同步的分集")
				}
				sources = append(sources, subtitlederived.Source{
					URL: locator, Title: item.Title, Path: item.Path, Hash: item.Hash, Generation: *item.Generation,
				})
				found = true
				break
			}
			if !found {
				return nil, contract.InputInvalid("请选择已成功同步的分集")
			}
		}
		task, err := subtitleDerivedService(state).Create(sources, input.Modes, syncTask.CourseName)
		if err != nil {
			return nil, contract.InputInvalid(err.Error())
		}
		return subtitleDerivedService(state).Run(ctx, task.ID, inv.subtitleDerivedDependencies(store, state))
	})
}
