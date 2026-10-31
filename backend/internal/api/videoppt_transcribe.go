package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

const jobKindVideoSubtitle = "video-subtitle"

func (inv *Invoker) videoProgress(message string) {
	inv.emit(map[string]any{"kind": "progress", "data": inv.jobs.Report(message)})
}

func (inv *Invoker) transcribeVideoTask(
	ctx context.Context,
	state *contract.State,
	task videoppt.CreatedTask,
) (any, error) {
	service := videoPptService(state)
	result, recovered, err := inv.preparedVideoSubtitle(ctx, state, task)
	if err != nil {
		return nil, err
	}
	meta, _, err := service.SubtitleTask(task.ID)
	if err != nil {
		return nil, videoPptError(err)
	}
	courseName := meta.CourseName
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	path, err := videoppt.PublishSubtitle(ctx, task, directory, meta.OutputName, []byte(result.SRT))
	if err != nil {
		return nil, videoPptError(err)
	}
	if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
		return nil, videoPptError(err)
	}
	cleanupWarning := ""
	if recovered {
		cleanupWarning = "已复用保存的转写；未自动清理先前临时音频，请核对后处理"
	}

	if result.Directory != "" {
		if err := result.Cleanup(); err != nil {
			cleanupWarning = "字幕已保存，但临时音频清理失败：" + err.Error()
		}
	}

	warning := "自动转写可能有错，请核对后作为课堂原文使用。"
	if cleanupWarning != "" {
		warning += cleanupWarning
	}
	output := map[string]any{
		"title": library.FolderName(videoppt.SubtitleTitle(task.Name)), "page": meta.Page,
		"source": "本地 Whisper 转写", "warning": warning, "cleanupWarning": cleanupWarning,
		"srt": result.SRT, "count": result.Count, "savedPath": path,
	}
	return finishSubtitleTask(state, task, output, path)
}

func (inv *Invoker) handleVideoSubtitleTranscribeLocal(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID    string `json:"courseId"`
		OperationID string `json:"operationId"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("字幕参数无效")
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	if task, exists, err := inv.existingLocalSubtitle(state, input.OperationID, courseName); exists {
		return inv.runBoundSubtitle(store, state, task, err)
	}

	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title": "选择要转写的本地视频", "extensions": []string{"mp4", "mkv", "mov", "webm"},
		"defaultPath": state.LibraryRoot,
	}, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	task, _, err := inv.createQueuedVideoTask(state, *picked.Path, "", "subtitle", "", courseName, input.OperationID)
	return inv.runBoundSubtitle(store, state, task, err)
}

func (inv *Invoker) runSubtitleQueued(state *contract.State, id string, run func(context.Context) (any, error)) (any, error) {
	return inv.runSubtitleQueuedContext(context.Background(), state, id, run)
}

func (inv *Invoker) runSubtitleQueuedContext(ctx context.Context, state *contract.State, id string, run func(context.Context) (any, error)) (any, error) {
	key := "subtitle-transcribe:" + id
	result, err := inv.jobs.RunQueued(ctx, filepath.Join(state.LibraryRoot, "TaskQueue"), id, key, run)
	return inv.finishJob(key, "", result, err)
}

func (inv *Invoker) handleVideoResumeSubtitle(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	return inv.handleVideoResumeSubtitleContext(context.Background(), store, state, params)
}

func (inv *Invoker) handleVideoResumeSubtitleContext(ctx context.Context, store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("任务编号无效")
	}
	if _, _, err := videoPptService(state).SubtitleTask(input.ID); err != nil {
		return nil, videoPptError(err)
	}
	return inv.runSubtitleQueuedContext(ctx, state, input.ID, func(ctx context.Context) (any, error) {
		service := videoPptService(state)
		meta, directory, err := service.SubtitleTask(input.ID)
		if err != nil {
			return nil, videoPptError(err)
		}
		if meta.Saved != nil && meta.Saved.Path != "" {
			courseDirectory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", meta.CourseName)
			if err != nil {
				return nil, err
			}
			path, err := library.ManagedFile(courseDirectory, meta.Saved.Path)
			if err != nil {
				return nil, err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return nil, contract.Unavailable("字幕文件不可读")
			}
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != meta.Saved.SHA256 {
				return nil, contract.InputInvalid("已保存字幕与任务记录不一致，临时视频已保留")
			}
			segments, err := documents.ParseSrt(string(body))
			if err != nil {
				return nil, contract.InputInvalid(err.Error())
			}
			output := map[string]any{"title": meta.Name, "page": meta.Page, "source": "已保存的字幕", "warning": "已核对原有字幕。", "srt": string(body), "count": len(segments), "savedPath": path, "cleanupWarning": ""}
			return finishSubtitleTask(state, videoppt.CreatedTask{ID: meta.ID, Name: meta.Name, Dir: directory}, output, path)
		}
		if operation, err := service.Operation(input.ID); err == nil {
			if operation.Stage == "subtitle-station" {
				return inv.stationSubtitle(ctx, store, state, videoppt.CreatedTask{ID: meta.ID, Name: meta.Name, Dir: directory, Path: filepath.Join(directory, "video.mp4")})
			}
			if err := inv.ensureQueuedVideo(ctx, store, state, videoppt.CreatedTask{ID: meta.ID, Name: meta.Name, Dir: directory, Path: filepath.Join(directory, "video.mp4")}); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, videoPptError(err)
		}

		video := filepath.Join(directory, "video.mp4")
		if info, err := os.Stat(video); err != nil || !info.Mode().IsRegular() {
			return nil, contract.InputInvalid("本地视频副本尚未准备完成，请重新选择原视频")
		}
		if _, err := os.Stat(filepath.Join(directory, "video-ready.json")); err == nil {
			if err := videoppt.VerifyVideoReady(videoppt.CreatedTask{Dir: directory, Path: video}); err != nil {
				return nil, videoPptError(err)
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		return inv.transcribeVideoTask(ctx, state, videoppt.CreatedTask{
			ID: meta.ID, Name: meta.Name, Dir: directory, Path: video,
		})
	})
}

func (inv *Invoker) handleVideoSubtitleTranscribe(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL         string `json:"url"`
		CourseID    string `json:"courseId"`
		OperationID string `json:"operationId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || (input.URL == "" && input.OperationID == "") {
		return nil, contract.InputInvalid("视频网址无效")
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	task, _, err := inv.createQueuedVideoTask(state, "", input.URL, "subtitle", "", courseName, input.OperationID)
	return inv.runBoundSubtitle(store, state, task, err)
}

func atoiSafe(value string) int {
	number := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0
		}
		number = number*10 + int(char-'0')
	}
	return number
}
