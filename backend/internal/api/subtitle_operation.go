package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/asr"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) runBoundSubtitle(store contract.StateStore, state *contract.State, task videoppt.CreatedTask, createErr error) (any, error) {
	return inv.runBoundSubtitleContext(context.Background(), store, state, task, createErr)
}

func (inv *Invoker) runBoundSubtitleContext(ctx context.Context, store contract.StateStore, state *contract.State, task videoppt.CreatedTask, createErr error) (any, error) {
	if createErr != nil {
		var completed *videoppt.CompletedOperationError
		if errors.As(createErr, &completed) {
			var result map[string]any
			if err := json.Unmarshal(completed.Result, &result); err != nil {
				return nil, contract.Unavailable("已完成任务记录无效")
			}
			return result, nil
		}
		return nil, videoPptError(createErr)
	}
	params, _ := json.Marshal(map[string]string{"id": task.ID})
	return inv.handleVideoResumeSubtitleContext(ctx, store, state, params)
}

func (inv *Invoker) existingLocalSubtitle(state *contract.State, id, courseName string, retain bool, languages ...*string) (videoppt.CreatedTask, bool, error) {
	var task videoppt.CreatedTask
	var language *string
	if len(languages) > 0 {
		language = languages[0]
	}
	if id == "" {
		return task, false, nil
	}
	if !config.IsUUID(id) {
		return task, true, contract.InputInvalid("任务编号无效")
	}
	service := videoPptService(state)
	operation, err := service.Operation(id)
	if err != nil {
		if _, statErr := os.Lstat(filepath.Join(service.Root, id)); os.IsNotExist(statErr) {
			return task, false, nil
		}
		return task, true, videoPptError(err)
	}
	if operation.Stage != "subtitle" || operation.Path == "" || operation.URL != "" || operation.CourseName != courseName || operation.RetainPlayback != retain || (language != nil && effectiveASRLanguage(operation.ASRLanguage) != effectiveASRLanguage(*language)) {
		return task, true, contract.InputInvalid("任务编号已绑定其他来源或课程")
	}
	task, _, err = inv.createQueuedVideoTaskLanguage(state, "", "", "subtitle", "", courseName, retain, language, id)
	return task, true, err
}

func (inv *Invoker) preparedVideoSubtitle(ctx context.Context, state *contract.State, task videoppt.CreatedTask) (asr.Result, bool, error) {
	meta, _, err := videoPptService(state).SubtitleTask(task.ID)
	if err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	language := effectiveASRLanguage(meta.ASRLanguage)
	prepared, found, err := videoppt.LoadPreparedSubtitle(task)
	if err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	if found {
		if prepared.Version != 2 && language != "zh" {
			return asr.Result{}, true, contract.InputInvalid("旧转写检查点缺少语言凭据，请保留任务")
		}
		if prepared.Version == 2 && prepared.Provenance.RequestedLanguage != language {
			return asr.Result{}, true, contract.InputInvalid("已保存转写语言与当前任务不一致，请保留任务")
		}
		segments, err := documents.ParseSrt(prepared.SRT)
		if err != nil || len(segments) != prepared.Count {
			return asr.Result{}, true, contract.InputInvalid("已保存转写内容与记录不一致")
		}
		return asr.Result{SRT: prepared.SRT, Count: prepared.Count, SourceSHA256: prepared.VideoSHA256, SRTSHA256: prepared.SRTSHA256, Provenance: prepared.Provenance}, true, nil
	}
	if videoPptService(state).HasRetentionCandidate(task.ID) {
		return asr.Result{}, false, contract.Unavailable("留存候选已存在但转写检查点缺失，请保留任务，未重复转写")
	}
	result, err := asr.Pipeline{
		Resources: state.ResourcesRoot, DataRoot: state.DataRoot,
		Platform:            media.CurrentMediaPlatform(),
		AllowSystemFallback: !state.Packaged || os.Getenv("ZYNOTE_SYSTEM_MEDIA") == "1",
		Call: func(ctx context.Context, method string, params any, timeoutMS int) (json.RawMessage, error) {
			if inv.asr == nil {
				return nil, contract.Unavailable("本地字幕模型不可用")
			}
			return inv.asr.CallContext(ctx, method, params, timeoutMS)
		},
		Progress: inv.videoProgress,
	}.Transcribe(ctx, task.Path, language)
	if err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	if err := videoppt.SavePreparedSubtitleWithProvenance(task, result.SRT, result.Count, result.SourceSHA256, result.SRTSHA256, result.Provenance); err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	return result, false, nil
}

func finishSubtitleTask(state *contract.State, task videoppt.CreatedTask, output map[string]any, path string) (any, error) {
	return finishSubtitleTaskContext(context.Background(), state, task, output, path)
}

func finishSubtitleTaskContext(ctx context.Context, state *contract.State, task videoppt.CreatedTask, output map[string]any, path string) (any, error) {
	service := videoPptService(state)
	operation, err := service.Operation(task.ID)
	if err != nil && !os.IsNotExist(err) {
		return nil, videoPptError(err)
	}
	if err != nil || !operation.RetainPlayback {
		return finishManagedVideoTask(state, task, output, path, false)
	}
	// Legacy saved-only recovery preserves output/media but cannot fabricate a
	// pre-publication candidate or receipt. A later explicit release remains possible.
	if !service.HasRetentionCandidate(task.ID) {
		output["playbackUnavailable"] = true
		output["cleanupWarning"] = "字幕已保存，但缺少发布前留存候选；视频保留，未创建播放绑定"
	} else {
		binding, err := service.CompletePlaybackRetention(ctx, task.ID, path)
		if err != nil {
			return nil, videoPptError(err)
		}
		output["playbackBinding"] = binding
	}
	if err := service.CompleteOperation(task, output, path); err != nil {
		return nil, videoPptError(err)
	}
	return output, nil
}
