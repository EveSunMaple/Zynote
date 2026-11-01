package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) beginVideoBatch(root, id string) (context.Context, func(), error) {
	inv.videoBatchMu.Lock()
	defer inv.videoBatchMu.Unlock()
	key := filepath.Join(root, id)
	if inv.videoBatches == nil {
		inv.videoBatches = map[string]context.CancelFunc{}
	}
	if inv.videoBatches[key] != nil {
		return nil, nil, contract.NewError(contract.CodeBusy, "该批次正在运行，请等待或取消")
	}
	ctx, cancel := context.WithCancel(context.Background())
	inv.videoBatches[key] = cancel
	return ctx, func() { cancel(); inv.videoBatchMu.Lock(); delete(inv.videoBatches, key); inv.videoBatchMu.Unlock() }, nil
}

func (inv *Invoker) cancelVideoBatches() {
	inv.videoBatchMu.Lock()
	defer inv.videoBatchMu.Unlock()
	for _, cancel := range inv.videoBatches {
		cancel()
	}
}

func (inv *Invoker) bindVideoBatch(store contract.StateStore, state *contract.State, params json.RawMessage, kind string) (videoppt.BatchOperation, error) {
	var input struct {
		OperationID string  `json:"operationId"`
		ASRLanguage *string `json:"asrLanguage"`
		URL         string  `json:"url"`
		Pages       []int   `json:"pages"`
		CourseID    string  `json:"courseId"`
		Prompt      string  `json:"prompt"`
	}
	if json.Unmarshal(params, &input) != nil {
		return videoppt.BatchOperation{}, contract.InputInvalid(msgInvalidEpisode)
	}
	language := ""
	if input.ASRLanguage != nil {
		var err error
		language, err = contract.NormalizeASRLanguage(*input.ASRLanguage)
		if err != nil {
			return videoppt.BatchOperation{}, err
		}
		if kind != "subtitle" {
			return videoppt.BatchOperation{}, contract.InputInvalid("仅字幕批次支持转写语言")
		}
	}
	service := videoPptService(state)
	if input.URL == "" && input.OperationID != "" {
		batch, err := service.Batch(input.OperationID)
		if err == nil && (batch.Input.Kind != kind || (input.ASRLanguage != nil && effectiveASRLanguage(batch.Input.ASRLanguage) != language)) {
			return batch, contract.InputInvalid("批次类型与已绑定任务不一致")
		}
		return batch, err
	}
	normalized, err := bilibili.VideoURL(input.URL)
	if err != nil {
		return videoppt.BatchOperation{}, err
	}
	if len(input.Pages) < 1 || len(input.Pages) > 1000 {
		return videoppt.BatchOperation{}, contract.InputInvalid(msgInvalidEpisode)
	}
	seen := map[int]bool{}
	for _, page := range input.Pages {
		if page < 1 || page > 99999 || seen[page] {
			return videoppt.BatchOperation{}, contract.InputInvalid(msgInvalidEpisode)
		}
		seen[page] = true
	}
	course, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return videoppt.BatchOperation{}, err
	}
	if kind == "subtitle" {
		input.Prompt = "station-first-then-asr"
	}
	if input.Prompt == "" {
		input.Prompt = defaultVideoPrompt
	}
	return service.BindBatch(input.OperationID, videoppt.BatchInput{Kind: kind, URL: normalized, Pages: input.Pages, Prompt: input.Prompt, CourseName: course, ASRLanguage: language})
}

func (inv *Invoker) prepareVideoBatch(ctx context.Context, store contract.StateStore, state *contract.State, batch videoppt.BatchOperation) (videoppt.BatchOperation, error) {
	if len(batch.Episodes) > 0 {
		return batch, nil
	}
	var prepared videoppt.BatchOperation
	_, err := inv.jobs.RunQueued(ctx, filepath.Join(state.LibraryRoot, "TaskQueue"), batch.ID, "video-batch-prepare:"+batch.ID, func(ctx context.Context) (any, error) {
		current, err := videoPptService(state).Batch(batch.ID)
		if err != nil {
			return nil, err
		}
		if len(current.Episodes) > 0 {
			prepared = current
			return nil, nil
		}
		series, err := inv.fetchVideoSeries(ctx, store, state, current.Input.URL)
		if err != nil {
			return nil, err
		}
		selected, err := selectedEpisodes(series, current.Input.Pages)
		if err != nil {
			return nil, err
		}
		episodes := make([]videoppt.BatchEpisode, 0, len(selected))
		for _, episode := range selected {
			episodes = append(episodes, videoppt.BatchEpisode{Page: episode.Page, Title: episode.Title, URL: episode.URL})
		}
		prepared, err = videoPptService(state).SetBatchEpisodes(batch.ID, episodes)
		return nil, err
	})
	return prepared, err
}

func (inv *Invoker) runVideoBatchEpisode(ctx context.Context, store contract.StateStore, state *contract.State, batch videoppt.BatchOperation, episode videoppt.BatchEpisode) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := batch.ChildID(episode.Page)
	if batch.Input.Kind == "subtitle" {
		task, _, err := inv.createQueuedVideoTaskLanguage(state, "", episode.URL, "subtitle-station", "", batch.Input.CourseName, false, &batch.Input.ASRLanguage, id)
		return inv.runBoundSubtitleContext(ctx, store, state, task, err)
	}
	// Bind the exact parent course snapshot before operation-only continuation.
	_, _, err := inv.createQueuedVideoTask(state, "", episode.URL, "auto", batch.Input.Prompt, batch.Input.CourseName, id)
	if err != nil {
		var completed *videoppt.CompletedOperationError
		if !errors.As(err, &completed) {
			return nil, videoPptError(err)
		}
		var result map[string]any
		if json.Unmarshal(completed.Result, &result) != nil {
			return nil, contract.Unavailable("已完成任务记录无效")
		}
		return result, nil
	}
	params, _ := json.Marshal(map[string]string{"operationId": id})
	return inv.handleVideoPptAutoContext(ctx, store, state, params)
}

func (inv *Invoker) handleVideoBatch(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	return inv.handleManagedVideoBatch(store, state, params, "")
}

func (inv *Invoker) handleVideoBatchSubtitles(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	return inv.handleManagedVideoBatch(store, state, params, "subtitle")
}

func (inv *Invoker) handleManagedVideoBatch(store contract.StateStore, state *contract.State, params json.RawMessage, kind string) (any, error) {
	batch, err := inv.bindVideoBatch(store, state, params, kind)
	if err != nil {
		return nil, videoPptError(err)
	}
	ctx, release, err := inv.beginVideoBatch(state.LibraryRoot, batch.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	batch, err = inv.prepareVideoBatch(ctx, store, state, batch)
	if err != nil {
		return nil, videoPptError(err)
	}
	items := make([]map[string]any, 0, len(batch.Episodes))
	for _, episode := range batch.Episodes {
		items = append(items, map[string]any{"page": episode.Page, "title": episode.Title, "status": "等待处理", "taskId": batch.ChildID(episode.Page)})
	}
	notify := func(taskID string) {
		inv.emit(map[string]any{"kind": "video-batch", "operationId": batch.ID, "taskId": taskID, "batchKind": batch.Input.Kind, "items": items})
	}
	notify("")
	for index, episode := range batch.Episodes {
		if ctx.Err() != nil {
			return nil, contract.NewError(contract.CodeCancelled, "批次已取消，任务已保留")
		}
		item := items[index]
		item["status"] = "处理中"
		notify(batch.ChildID(episode.Page))
		stage := "视频课件"
		if batch.Input.Kind == "subtitle" {
			stage = "字幕转写"
		}
		inv.emit(map[string]any{"kind": "progress", "data": map[string]any{"key": batchProgressKey(batch, episode.Page), "stage": stage, "message": fmt.Sprintf("正在等待或处理所选课程 %d/%d · P%d %s", index+1, len(items), episode.Page, episode.Title)}})
		result, err := inv.runVideoBatchEpisode(ctx, store, state, batch, episode)
		if ctx.Err() != nil || err == jobs.ErrCancelled {
			return nil, contract.NewError(contract.CodeCancelled, "批次已取消，任务已保留")
		}
		if err != nil {
			item["status"] = "失败"
			item["error"] = err.Error()
		} else {
			item["status"] = "已完成"
			if batch.Input.Kind == "subtitle" {
				item["path"] = result.(map[string]any)["savedPath"]
			} else {
				item["path"] = result.(map[string]any)["path"]
			}
		}
		notify("")
		var application *contract.AppError
		if errors.As(err, &application) && (application.Code == contract.CodeAuthExpired || application.Code == contract.CodeRateLimited) {
			return nil, application
		}
	}
	complete := true
	for _, item := range items {
		if item["status"] != "已完成" {
			complete = false
		}
	}
	if complete {
		if err := videoPptService(state).CompleteBatch(batch.ID); err != nil {
			return nil, videoPptError(err)
		}
	}
	return items, nil
}

func batchProgressKey(batch videoppt.BatchOperation, page int) string {
	prefix := "video-ppt:"
	if batch.Input.Kind == "subtitle" {
		prefix = "subtitle-transcribe:"
	}
	return prefix + batch.ChildID(page)
}
