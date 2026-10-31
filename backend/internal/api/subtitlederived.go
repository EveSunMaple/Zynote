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
		filepath.Join(state.LibraryRoot, "NoteGeneration"), state.LibraryRoot,
	)
}

func (inv *Invoker) subtitleDerivedDependencies(store contract.StateStore, state *contract.State) subtitlederived.Dependencies {
	knowledgeStore, _ := inv.knowledgeStoreFor(state)
	var client *ai.Client
	getClient := func() (*ai.Client, error) {
		if client != nil {
			return client, nil
		}
		profile, err := config.ActiveProfile(state.ConfigRoot, store.Reverse())
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
		ValidateModel: func(context.Context) error {
			_, err := getClient()
			return err
		},
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
	for index := range tasks {
		if record, err := inv.jobs.QueueRecord(filepath.Join(state.LibraryRoot, "TaskQueue"), tasks[index].ID); err == nil {
			tasks[index].QueueState = record.State
		}
	}
	return tasks, nil
}

func (inv *Invoker) runDerivedQueued(store contract.StateStore, state *contract.State, id string) (any, error) {
	key := "subtitle-derived:" + id
	result, err := inv.jobs.RunQueued(context.Background(), filepath.Join(state.LibraryRoot, "TaskQueue"), id, key, func(ctx context.Context) (any, error) {
		return subtitleDerivedService(state).Run(ctx, id, inv.subtitleDerivedDependencies(store, state))
	})
	return inv.finishJob(key, "", result, err)
}

func (inv *Invoker) handleSubtitleDerivedResume(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("笔记任务标识无效")
	}
	if _, err := subtitleDerivedService(state).Read(input.ID); err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return inv.runDerivedQueued(store, state, input.ID)
}

type subtitleDerivedInput struct {
	ID    string   `json:"id"`
	URLs  []string `json:"urls"`
	Modes []string `json:"modes"`
}

func parseSubtitleDerivedInput(params json.RawMessage) (subtitleDerivedInput, error) {
	var input subtitleDerivedInput
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) ||
		len(input.URLs) < 1 || len(input.URLs) > 1000 || len(input.Modes) < 1 || len(input.Modes) > 4 {
		return input, contract.InputInvalid("笔记任务参数无效")
	}
	return input, nil
}

func findSubtitleSyncTask(state *contract.State, id string) (*subtitlesync.Task, error) {
	tasks, err := subtitleSyncService(state).List()
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	for index := range tasks {
		if tasks[index].ID == id {
			return &tasks[index], nil
		}
	}
	return nil, contract.InputInvalid("同步记录不存在")
}

func derivedLocators(urls []string) ([]string, error) {
	locators := []string{}
	seen := map[string]bool{}
	for _, value := range urls {
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
	return locators, nil
}

func derivedSources(task *subtitlesync.Task, locators []string) ([]subtitlederived.Source, error) {
	sources := []subtitlederived.Source{}
	for _, locator := range locators {
		found := false
		for _, item := range task.Items {
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
	return sources, nil
}

func (inv *Invoker) handleSubtitleDerivedStart(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	input, err := parseSubtitleDerivedInput(params)
	if err != nil {
		return nil, err
	}
	syncTask, err := findSubtitleSyncTask(state, input.ID)
	if err != nil {
		return nil, err
	}
	locators, err := derivedLocators(input.URLs)
	if err != nil {
		return nil, err
	}
	sources, err := derivedSources(syncTask, locators)
	if err != nil {
		return nil, err
	}
	task, err := subtitleDerivedService(state).Create(sources, input.Modes, syncTask.CourseName)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	return inv.runDerivedQueued(store, state, task.ID)
}
