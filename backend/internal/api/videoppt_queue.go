package api

import (
	"context"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) createQueuedVideoTask(state *contract.State, path, videoURL, stage, prompt, courseName string, operationIDs ...string) (videoppt.CreatedTask, string, error) {
	return inv.createQueuedVideoTaskRetained(state, path, videoURL, stage, prompt, courseName, false, operationIDs...)
}

func (inv *Invoker) createQueuedVideoTaskRetained(state *contract.State, path, videoURL, stage, prompt, courseName string, retain bool, operationIDs ...string) (videoppt.CreatedTask, string, error) {
	return inv.createQueuedVideoTaskLanguage(state, path, videoURL, stage, prompt, courseName, retain, nil, operationIDs...)
}

func (inv *Invoker) createQueuedVideoTaskLanguage(state *contract.State, path, videoURL, stage, prompt, courseName string, retain bool, requestedLanguage *string, operationIDs ...string) (videoppt.CreatedTask, string, error) {
	var task videoppt.CreatedTask
	language := "zh"
	if requestedLanguage != nil {
		var err error
		language, err = contract.NormalizeASRLanguage(*requestedLanguage)
		if err != nil {
			return task, "", err
		}
	}
	id := ""
	if len(operationIDs) > 0 {
		id = operationIDs[0]
	}
	if path == "" && videoURL == "" && id != "" {
		operation, err := videoPptService(state).Operation(id)
		if err != nil {
			return task, "", videoPptError(err)
		}
		if operation.Stage != stage || (requestedLanguage != nil && effectiveASRLanguage(operation.ASRLanguage) != language) {
			return task, "", contract.InputInvalid("任务阶段不一致")
		}
		task, err = videoPptService(state).CreateOperationTask(id, "", operation)
		return task, operation.URL, err
	}
	operation := videoppt.VideoOperation{RetainPlayback: retain, Version: 1, Stage: stage, Prompt: prompt, CourseName: courseName}
	if language != "zh" {
		operation.ASRLanguage = language
	}
	name := ""
	var sourceSize int64
	if stage == "subtitle" || stage == stageSubtitleStation {
		operation.Page = 1
	}
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return task, "", err
		}
		info, err := os.Lstat(absolute)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return task, "", contract.InputInvalid("所选源文件不可用")
		}
		if retain && (info.Size() <= 0 || info.Size() > 8*1024*1024*1024) {
			return task, "", contract.InputInvalid("留存视频不得超过8GiB")
		}
		sourceSize = info.Size()
		hash, err := videoppt.VideoDigest(absolute)
		if err != nil {
			return task, "", err
		}
		operation.Path = absolute
		operation.SourceHash = hash
		name = filepath.Base(absolute)
	} else {
		videoID, page, _, err := biliTaskName(videoURL, "")
		if err != nil {
			return task, "", err
		}
		normalized, err := mediaBilibili.VideoURL(videoURL)
		if err != nil {
			return task, "", err
		}
		operation.URL = normalized
		name = videoID + "-P" + page
		if stage == "subtitle" || stage == stageSubtitleStation {
			operation.Page = atoiSafe(page)
		}
	}
	if stage == "subtitle" || stage == stageSubtitleStation {
		operation.OutputName = library.FolderName(videoppt.SubtitleTitle(name)) + "-离线转写.srt"
		if stage == stageSubtitleStation {
			operation.OutputName = library.FolderName(videoppt.SubtitleTitle(name)) + "-字幕.srt"
		}
	}
	task, err := videoPptService(state).CreateOperationTask(id, name, operation)
	if err != nil {
		return task, "", err
	}
	if err := videoppt.PersistStageInput(task, stage, operation); err != nil {
		return task, "", videoPptError(err)
	}
	if retain {
		if _, err := videoPptService(state).ReservePlaybackRetention(context.Background(), task.ID, sourceSize); err != nil {
			return task, "", videoPptError(err)
		}
	}
	return task, operation.URL, nil
}

func (inv *Invoker) ensureQueuedVideo(ctx context.Context, store contract.StateStore, state *contract.State, task videoppt.CreatedTask) error {
	inv.projectVideoSource(state, task.ID)
	defer inv.projectVideoSource(state, task.ID)
	operation, err := videoPptService(state).Operation(task.ID)
	if err != nil {
		return videoPptError(err)
	}
	if _, err := os.Stat(task.Path); err == nil {
		if err := videoppt.VerifyVideoReady(task); err != nil {
			// Only local legacy-in-flight copies have an independent source hash.
			if operation.Path == "" {
				return err
			}
			hash, hashErr := videoppt.VideoDigest(task.Path)
			if hashErr != nil || hash != operation.SourceHash {
				return err
			}
			return videoppt.MarkVideoReady(task)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if operation.Path != "" {
		hash, err := videoppt.VideoDigest(operation.Path)
		if err != nil || hash != operation.SourceHash {
			return contract.InputInvalid("原视频已改变，已保留任务，请重新选择来源")
		}
		if err := videoppt.CopyVideoReady(ctx, operation.Path, task.Path); err != nil {
			return err
		}
		copied, err := videoppt.VideoDigest(task.Path)
		if err != nil || copied != operation.SourceHash {
			return contract.InputInvalid("视频副本与来源不一致，已保留任务")
		}
	} else {
		inv.videoProgress("正在下载视频")
		if err := inv.downloadBiliVideo(ctx, store, state, operation.URL, task.Path); err != nil {
			return err
		}
	}
	return videoppt.VerifyVideoReady(task)
}

func (inv *Invoker) projectVideoSource(state *contract.State, id string) {
	service := videoPptService(state)
	operation, err := service.Operation(id)
	if err != nil {
		return
	}
	source, err := service.MediaSource(id)
	if err != nil {
		return
	}
	key := "video-ppt:" + id
	if operation.Stage == "subtitle" || operation.Stage == stageSubtitleStation {
		key = "subtitle-transcribe:" + id
	}
	inv.jobs.SetSource(key, source)
}

func (inv *Invoker) runVideoPptQueued(state *contract.State, task videoppt.CreatedTask, stage string, input any, run func(context.Context) (any, error)) (any, error) {
	return inv.runVideoPptQueuedContext(context.Background(), state, task, stage, input, run)
}

func (inv *Invoker) runVideoPptQueuedContext(ctx context.Context, state *contract.State, task videoppt.CreatedTask, stage string, input any, run func(context.Context) (any, error)) (any, error) {
	if input != nil {
		if _, err := videoPptService(state).RunVideoTask(ctx, task.ID, func(context.Context) (any, error) { return nil, videoppt.PersistStageInput(task, stage, input) }); err != nil {
			return nil, videoPptError(err)
		}
	}
	key := "video-ppt:" + task.ID
	result, err := inv.jobs.RunQueued(ctx, filepath.Join(state.LibraryRoot, "TaskQueue"), task.ID, key, func(ctx context.Context) (any, error) { return videoPptService(state).RunVideoTask(ctx, task.ID, run) })
	return inv.finishJob(key, "", result, err)
}

func (inv *Invoker) prepareQueuedVideo(ctx context.Context, store contract.StateStore, state *contract.State, task videoppt.CreatedTask) (videoppt.PreparedTask, error) {
	if err := inv.ensureQueuedVideo(ctx, store, state, task); err != nil {
		return videoppt.PreparedTask{}, err
	}
	chat, err := inv.videoChatConfigured(store, state, &task, "source")
	if err != nil {
		return videoppt.PreparedTask{}, err
	}
	prepared, err := videoProcessor(state).Prepare(ctx, task, chat, inv.videoProgress)
	if err != nil {
		return videoppt.PreparedTask{}, videoPptError(err)
	}
	return prepared, nil
}

func queuedVideoTask(state *contract.State, id string) (videoppt.CreatedTask, error) {
	service := videoPptService(state)
	task, err := service.VideoTask(id)
	if err != nil {
		return task, err
	}
	if _, err := service.Operation(id); err == nil {
		if err := videoppt.VerifyVideoReady(task); err != nil {
			return task, err
		}
	} else if !os.IsNotExist(err) {
		return task, err
	}
	return task, nil
}

func finishVideoPptExport(state *contract.State, task videoppt.CreatedTask, result videoppt.ExportResult) (any, error) {
	receipt := map[string]any{"path": result.Path, "kept": result.Kept, "removed": result.Removed, "cleaned": false, "cleanupWarning": "已恢复已保存成果；临时文件清理状态请核对任务记录"}
	return finishManagedVideoTask(state, task, receipt, result.Path, true)
}

func finishManagedVideoTask(state *contract.State, task videoppt.CreatedTask, output map[string]any, path string, trackCleanup bool) (any, error) {
	service := videoPptService(state)
	if err := service.CompleteOperation(task, output, path); err != nil {
		return nil, videoPptError(err)
	}
	warning, _ := output["cleanupWarning"].(string)
	if trackCleanup {
		warning = ""
	}
	cleanupErr := service.DiscardCompleted(task.ID)
	if cleanupErr != nil {
		if warning != "" {
			warning += "；"
		}
		warning += cleanupErr.Error()
		if base, ok := output["warning"].(string); ok {
			output["warning"] = base + "；" + cleanupErr.Error()
		}
	}
	if trackCleanup {
		output["cleaned"] = cleanupErr == nil
	}
	output["cleanupWarning"] = warning
	if _, err := service.Operation(task.ID); err == nil {
		if err := service.CompleteOperation(task, output, path); err != nil {
			output["cleanupWarning"] = "成果已保存，但完成记录更新失败：" + err.Error()
		}
	}
	return output, nil
}

func effectiveASRLanguage(value string) string {
	if value == "" {
		return "zh"
	}
	return value
}

const stageSubtitleStation = "subtitle-station"
