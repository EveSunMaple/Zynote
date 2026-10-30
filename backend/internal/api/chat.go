package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func (inv *Invoker) emit(payload map[string]any) {
	if inv.notifier != nil {
		_ = inv.notifier.Notify("event", payload)
	}
}

func (inv *Invoker) runJob(key string, run func(context.Context) (any, error)) (any, error) {
	lectureID := strings.TrimPrefix(key, "lecture:")
	if lectureID == key || !config.IsUUID(lectureID) {
		lectureID = ""
	}
	result, err := inv.jobs.Run(key, lectureID, run)
	event := map[string]any{"key": key, "state": "finished"}
	if lectureID != "" {
		event["lectureId"] = lectureID
	}
	if err != nil {
		message := err.Error()
		if appErr, ok := err.(*contract.AppError); ok {
			message = appErr.Message
		}
		event["error"] = message
		data := map[string]any{"stage": "任务停止", "message": message, "error": true}
		if lectureID != "" {
			data["lectureId"] = lectureID
		}
		inv.emit(map[string]any{"kind": "progress", "data": data})
	}
	inv.emit(map[string]any{"kind": "job", "data": event})
	return result, err
}

func (inv *Invoker) handleCancel(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	if state != nil {
		tasks := inv.zhiyunTaskStore(state)
		if rows, err := tasks.List(); err == nil {
			for _, task := range rows {
				if task.State != zhiyunTaskQueued && task.State != zhiyunTaskRunning && task.State != zhiyunTaskCancelling {
					continue
				}
				updated, err := tasks.Update(task.ID, func(value *zhiyunTask) {
					value.State = zhiyunTaskCancelled
					value.Stage = "已取消"
					value.Error = ""
				})
				if err == nil {
					inv.emitZhiyunTask(updated)
				}
			}
		}
	}
	inv.jobs.CancelAll()
	return true, nil
}

func (inv *Invoker) handleJobCurrent(_ contract.StateStore, _ *contract.State, _ json.RawMessage) (any, error) {
	return inv.jobs.Current(), nil
}

func (inv *Invoker) handleChat(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID  string  `json:"courseId"`
		LectureID *string `json:"lectureId"`
		SessionID *string `json:"sessionId"`
		Content   string  `json:"content"`
		RequestID string  `json:"requestId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	if len(input.Content) < 1 || len([]rune(input.Content)) > 12000 || strings.TrimSpace(input.RequestID) == "" {
		return nil, contract.InputInvalid("消息内容无效")
	}
	lectureID := input.LectureID
	if lectureID != nil {
		trimmed := strings.TrimSpace(*lectureID)
		if trimmed == "" {
			lectureID = nil
		} else if !config.IsUUID(trimmed) {
			return nil, contract.InputInvalid("课次标识无效")
		} else {
			lectureID = &trimmed
		}
	}
	sessionID := input.SessionID
	if sessionID != nil {
		trimmed := strings.TrimSpace(*sessionID)
		if trimmed == "" {
			sessionID = nil
		} else if !config.IsUUID(trimmed) {
			return nil, contract.InputInvalid("会话标识无效")
		} else {
			sessionID = &trimmed
		}
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	if err := write.AppendMessage(input.CourseID, lectureID, "user", input.Content, sessionID); err != nil {
		write.Close()
		return nil, err
	}
	write.Close()
	identity, err := config.ReadIdentity(state.ConfigRoot)
	if err != nil {
		return nil, err
	}
	profile, err := config.ActiveProfile(state.ConfigRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	key := "chat:" + input.CourseID
	result, err := inv.runJob(key, func(ctx context.Context) (any, error) {
		client := ai.New(ai.Profile{
			BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
			APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
		})
		return client.Chat(ctx, []ai.Message{
			{Role: "system", Content: config.IdentityPrompt(identity)},
			{Role: "user", Content: input.Content},
		}, func(text string) {
			inv.emit(map[string]any{"kind": "chat", "requestId": input.RequestID, "text": text})
		})
	})
	if err != nil {
		return nil, err
	}
	reply, _ := result.(string)
	write, err = openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.AppendMessage(input.CourseID, lectureID, "assistant", reply, sessionID); err != nil {
		return nil, err
	}
	return reply, nil
}
