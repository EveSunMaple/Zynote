package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/asr"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) videoProgress(message string) {
	inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
		"stage": "视频提取 PPT", "message": message,
	}})
}

func (inv *Invoker) transcribeVideoTask(
	ctx context.Context,
	state *contract.State,
	task videoppt.CreatedTask,
) (any, error) {
	service := videoPptService(state)
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
		return nil, contract.Unavailable(err.Error())
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
	path, err := library.ReserveFile(directory, meta.OutputName)
	if err != nil {
		return nil, err
	}
	cleanupWarning := ""
	if err := os.WriteFile(path, []byte(result.SRT), 0o644); err != nil {
		_ = os.Remove(path)
		return nil, contract.Unavailable("保存字幕后校验失败")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != result.SRT {
		_ = os.Remove(path)
		return nil, contract.Unavailable("保存字幕后校验失败")
	}
	if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
		_ = os.Remove(path)
		return nil, videoPptError(err)
	}
	if err := result.Cleanup(); err != nil {
		cleanupWarning = "字幕已保存，但临时音频清理失败：" + err.Error()
	}
	if err := service.DiscardCompleted(task.ID); err != nil {
		if cleanupWarning == "" {
			cleanupWarning = err.Error()
		} else {
			cleanupWarning += "；" + err.Error()
		}
	}
	warning := "自动转写可能有错，请核对后作为课堂原文使用。"
	if cleanupWarning != "" {
		warning += cleanupWarning
	}
	return map[string]any{
		"title": library.FolderName(videoppt.SubtitleTitle(task.Name)), "page": meta.Page,
		"source": "本地 Whisper 转写", "warning": warning, "cleanupWarning": cleanupWarning,
		"srt": result.SRT, "count": result.Count, "savedPath": path,
	}, nil
}

func (inv *Invoker) handleVideoSubtitleTranscribeLocal(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("字幕参数无效")
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
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
	return inv.runJob("video-subtitle", func(ctx context.Context) (any, error) {
		inv.videoProgress("正在复制本地视频用于字幕转写；原文件保持不变")
		outputName := library.FolderName(videoppt.SubtitleTitle(filepath.Base(*picked.Path))) + "-离线转写.srt"
		task, err := videoPptService(state).CreateSubtitleTask(*picked.Path, outputName, courseName, 1)
		if err != nil {
			return nil, videoPptError(err)
		}
		return inv.transcribeVideoTask(ctx, state, task)
	})
}

func (inv *Invoker) handleVideoResumeSubtitle(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !strings.HasPrefix(input.ID, "") || input.ID == "" {
		return nil, contract.InputInvalid("任务编号无效")
	}
	return inv.runJob("video-subtitle", func(ctx context.Context) (any, error) {
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
			if err := service.DiscardCompleted(meta.ID); err != nil {
				return nil, videoPptError(err)
			}
			return map[string]any{
				"title": meta.Name, "page": meta.Page, "source": "已保存的字幕",
				"warning": "已核对原有字幕并清理本次临时视频。", "srt": string(body),
				"count": len(segments), "savedPath": path, "cleanupWarning": "",
			}, nil
		}
		video := filepath.Join(directory, "video.mp4")
		if info, err := os.Stat(video); err != nil || !info.Mode().IsRegular() {
			return nil, contract.InputInvalid("本地视频副本尚未准备完成，请重新选择原视频")
		}
		return inv.transcribeVideoTask(ctx, state, videoppt.CreatedTask{
			ID: meta.ID, Name: meta.Name, Dir: directory, Path: video,
		})
	})
}

func (inv *Invoker) handleVideoSubtitleTranscribe(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL      string `json:"url"`
		CourseID string `json:"courseId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid("视频网址无效")
	}
	videoID, page, _, err := biliTaskName(input.URL, "")
	if err != nil {
		return nil, err
	}
	courseName, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	normalized, err := bilibili.VideoURL(input.URL)
	if err != nil {
		return nil, err
	}
	return inv.runJob("video-subtitle", func(ctx context.Context) (any, error) {
		inv.videoProgress("正在下载视频用于离线字幕转写")
		task, err := videoPptService(state).CreateBlankTask(videoID+"-P"+page+"-字幕转写", map[string]any{
			"kind": "subtitle", "outputName": videoID + "-P" + page + "-离线转写.srt",
			"courseName": courseName, "url": normalized, "page": atoiSafe(page),
		})
		if err != nil {
			return nil, videoPptError(err)
		}
		if err := inv.downloadBiliVideo(store, ctx, normalized, task.Path); err != nil {
			return nil, err
		}
		return inv.transcribeVideoTask(ctx, state, task)
	})
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
