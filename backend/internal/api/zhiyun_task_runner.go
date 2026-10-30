package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func (inv *Invoker) emitZhiyunTask(task *zhiyunTask) {
	if task == nil {
		return
	}
	inv.emit(map[string]any{"kind": "zhiyun-task", "data": task})
}

func (inv *Invoker) updateZhiyunTask(tasks *zhiyunTaskStore, id string, apply func(*zhiyunTask)) *zhiyunTask {
	updated, err := tasks.Update(id, apply)
	if err == nil {
		inv.emitZhiyunTask(updated)
	}
	return updated
}

func (inv *Invoker) startZhiyunTask(store contract.StateStore, state *contract.State, input zhiyunExportInput, title string) (*zhiyunTask, error) {
	tasks := inv.zhiyunTaskStore(state)
	task, err := tasks.Create(input, title)
	if err != nil {
		return nil, contract.Unavailable("智云任务记录无法保存")
	}
	inv.emitZhiyunTask(task)
	go inv.runZhiyunTask(store, state, input, task.ID)
	return task, nil
}

func (inv *Invoker) runZhiyunTask(store contract.StateStore, state *contract.State, input zhiyunExportInput, id string) {
	tasks := inv.zhiyunTaskStore(state)
	result, err := inv.jobs.Run("zhiyun:"+id, "", func(ctx context.Context) (any, error) {
		current, readErr := tasks.Get(id)
		if readErr != nil {
			return nil, readErr
		}
		if current.State == zhiyunTaskCancelled || current.State == zhiyunTaskCancelling {
			return nil, jobs.ErrCancelled
		}
		inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
			task.State = zhiyunTaskRunning
			task.Stage = "正在准备导出"
		})
		return inv.exportZhiyun(ctx, store, state, input, func(message string) {
			if strings.TrimSpace(message) == "" {
				return
			}
			inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
				task.Stage = message
			})
		}, func(courseID, lectureID string) {
			inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
				task.ResultCourseID = courseID
				task.LectureID = lectureID
			})
		})
	})
	if err != nil {
		if errors.Is(err, jobs.ErrCancelled) {
			inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
				task.State = zhiyunTaskCancelled
				task.Stage = "已取消"
				task.Error = ""
			})
			return
		}
		message := err.Error()
		if appErr, ok := err.(*contract.AppError); ok {
			message = appErr.Message
		}
		inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
			task.State = zhiyunTaskFailed
			task.Stage = "导出失败"
			task.Error = message
		})
		return
	}
	resultMap, _ := result.(map[string]any)
	inv.updateZhiyunTask(tasks, id, func(task *zhiyunTask) {
		task.State = zhiyunTaskCompleted
		task.Stage = "导出完成"
		task.Error = ""
		task.OutputPath = storage.ToStringValue(resultMap["path"])
		task.LectureID = storage.ToStringValue(resultMap["lectureId"])
		task.ResultCourseID = storage.ToStringValue(resultMap["courseId"])
		task.Warning = storage.ToStringValue(resultMap["warning"])
	})
}

func (inv *Invoker) handleZhiyunTasks(state *contract.State) (any, error) {
	return inv.zhiyunTaskStore(state).List()
}

func (inv *Invoker) handleZhiyunTaskCancel(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !validZhiyunTaskID(input.ID) {
		return nil, contract.InputInvalid("智云任务编号无效")
	}
	tasks := inv.zhiyunTaskStore(state)
	task, err := tasks.Get(input.ID)
	if err != nil {
		return nil, err
	}
	if task.State == zhiyunTaskCompleted || task.State == zhiyunTaskFailed ||
		task.State == zhiyunTaskCancelled || task.State == zhiyunTaskInterrupted {
		return task, nil
	}
	updated, err := tasks.Update(input.ID, func(value *zhiyunTask) {
		if value.State == zhiyunTaskQueued {
			value.State = zhiyunTaskCancelled
			value.Stage = "已取消"
			return
		}
		value.State = zhiyunTaskCancelling
		value.Stage = "正在取消"
	})
	if err != nil {
		return nil, err
	}
	inv.jobs.Cancel("zhiyun:" + input.ID)
	if latest, readErr := tasks.Get(input.ID); readErr == nil {
		updated = latest
	}
	inv.emitZhiyunTask(updated)
	return updated, nil
}

func (inv *Invoker) handleZhiyunTaskRetry(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !validZhiyunTaskID(input.ID) {
		return nil, contract.InputInvalid("智云任务编号无效")
	}
	tasks := inv.zhiyunTaskStore(state)
	task, err := tasks.Get(input.ID)
	if err != nil {
		return nil, err
	}
	if task.State == zhiyunTaskQueued || task.State == zhiyunTaskRunning || task.State == zhiyunTaskCancelling {
		return nil, contract.InputInvalid("任务仍在进行，请先取消或等待完成")
	}
	exportInput := zhiyunExportInput{
		Kind: task.Kind, CourseID: task.CourseID, SubID: task.SubID,
		CourseName: task.CourseName, LocalCourseID: task.LocalCourseID, Title: task.Title,
	}
	updated, err := tasks.Update(input.ID, func(value *zhiyunTask) {
		value.State = zhiyunTaskQueued
		value.Stage = "准备重试"
		value.Error = ""
		value.Warning = ""
		value.OutputPath = ""
		value.LectureID = ""
		value.ResultCourseID = ""
	})
	if err != nil {
		return nil, err
	}
	inv.emitZhiyunTask(updated)
	go inv.runZhiyunTask(store, state, exportInput, input.ID)
	return updated, nil
}

func zhiyunTaskTitle(input zhiyunExportInput) string {
	if title := strings.TrimSpace(input.Title); title != "" {
		return title
	}
	kind := "语音识别原文"
	if input.Kind == "ppt" {
		kind = "PPT"
	}
	if name := strings.TrimSpace(input.CourseName); name != "" {
		return fmt.Sprintf("%s · %s", name, kind)
	}
	return "智云" + kind
}
