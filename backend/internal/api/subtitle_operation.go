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

func (inv *Invoker) existingLocalSubtitle(state *contract.State, id, courseName string) (videoppt.CreatedTask, bool, error) {
	var task videoppt.CreatedTask
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
	if operation.Stage != "subtitle" || operation.Path == "" || operation.URL != "" || operation.CourseName != courseName {
		return task, true, contract.InputInvalid("任务编号已绑定其他来源或课程")
	}
	task, _, err = inv.createQueuedVideoTask(state, "", "", "subtitle", "", courseName, id)
	return task, true, err
}

func (inv *Invoker) preparedVideoSubtitle(ctx context.Context, state *contract.State, task videoppt.CreatedTask) (asr.Result, bool, error) {
	prepared, found, err := videoppt.LoadPreparedSubtitle(task)
	if err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	if found {
		segments, err := documents.ParseSrt(prepared.SRT)
		if err != nil || len(segments) != prepared.Count {
			return asr.Result{}, true, contract.InputInvalid("已保存转写内容与记录不一致")
		}
		return asr.Result{SRT: prepared.SRT, Count: prepared.Count}, true, nil
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
	}.Transcribe(ctx, task.Path, "zh")
	if err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	if err := videoppt.SavePreparedSubtitle(task, result.SRT, result.Count); err != nil {
		return asr.Result{}, false, videoPptError(err)
	}
	return result, false, nil
}

func finishSubtitleTask(state *contract.State, task videoppt.CreatedTask, output map[string]any, path string) (any, error) {
	return finishManagedVideoTask(state, task, output, path, false)
}
