package api

import (
	"context"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func (inv *Invoker) createQueuedVideoTask(state *contract.State, path, videoURL, stage, prompt, courseName string) (videoppt.CreatedTask, string, error) {
	var task videoppt.CreatedTask
	var err error
	normalized := ""
	if path != "" {
		task, err = videoProcessor(state).CreateTask(path)
	} else {
		videoID, page, _, nameErr := biliTaskName(videoURL, "")
		if nameErr != nil {
			return task, "", nameErr
		}
		normalized, err = mediaBilibili.VideoURL(videoURL)
		if err != nil {
			return task, "", err
		}
		task, err = videoPptService(state).CreateBlankTask(videoID+"-P"+page, map[string]any{"url": normalized})
	}
	if err != nil {
		return task, "", videoPptError(err)
	}
	if err := videoppt.PersistStageInput(task, stage, map[string]any{"url": normalized, "prompt": prompt, "courseName": courseName}); err != nil {
		return task, "", videoPptError(err)
	}
	return task, normalized, nil
}

func (inv *Invoker) runVideoPptQueued(state *contract.State, task videoppt.CreatedTask, stage string, input any, run func(context.Context) (any, error)) (any, error) {
	if input != nil {
		if err := videoppt.PersistStageInput(task, stage, input); err != nil {
			return nil, videoPptError(err)
		}
	}
	key := "video-ppt:" + task.ID
	result, err := inv.jobs.RunQueued(context.Background(), filepath.Join(state.LibraryRoot, "TaskQueue"), task.ID, key, run)
	return inv.finishJob(key, "", result, err)
}
