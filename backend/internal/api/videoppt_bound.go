package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func (inv *Invoker) handleSummaryFromRetained(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		OperationID string `json:"operationId"`
		Mode        string `json:"mode"`
		CourseID    string `json:"courseId,omitempty"`
		Retention   struct {
			VideoTaskID           string `json:"videoTaskID"`
			ExpectedReceiptSHA256 string `json:"expectedRetentionReceiptSHA256"`
		} `json:"retention"`
	}
	if err := decodePlaybackInput(params, &input); err != nil || !config.IsUUID(input.OperationID) {
		return nil, contract.InputInvalid("留存摘要参数无效")
	}
	if _, ok := videoNoteTitles[input.Mode]; !ok {
		return nil, contract.InputInvalid("摘要模式无效")
	}
	course, err := inv.subtitleCourseName(state, input.CourseID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service := subtitleDerivedService(state)
	if existing, found, err := service.ExistingRetainedOperation(ctx, input.OperationID, input.Mode, course, input.Retention.VideoTaskID, input.Retention.ExpectedReceiptSHA256); found || err != nil {
		if err != nil {
			return nil, videoPptError(err)
		}
		return inv.finishVideoSummary(store, state, existing.ID)
	}
	verified, err := videoPptService(state).RetainedSubtitle(ctx, input.Retention.VideoTaskID, input.Retention.ExpectedReceiptSHA256, course)
	if err != nil {
		return nil, videoPptError(err)
	}
	task, err := service.CreateRetainedOperation(ctx, verified, input.Mode, course, input.OperationID)
	if err != nil {
		return nil, videoPptError(err)
	}
	return inv.finishVideoSummary(store, state, task.ID)
}

func (inv *Invoker) handleBoundPlaybackSource(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		SummaryTaskID               string `json:"summaryTaskID"`
		ItemIndex                   *int   `json:"itemIndex"`
		ExpectedSourceBindingSHA256 string `json:"expectedSourceBindingSHA256"`
	}
	if err := decodePlaybackInput(params, &input); err != nil || input.ItemIndex == nil || *input.ItemIndex != 0 {
		return nil, contract.InputInvalid("摘要播放参数无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := subtitleDerivedService(state).ReadBoundTask(ctx, input.SummaryTaskID)
	if err != nil {
		return nil, videoPptError(err)
	}
	if len(task.Items) != 1 || task.Items[0].SourceBinding == nil || input.ExpectedSourceBindingSHA256 == "" || input.ExpectedSourceBindingSHA256 != task.Items[0].SourceBindingSHA256 {
		return nil, contract.InputInvalid("摘要不含已核播放绑定")
	}
	verified, err := videoPptService(state).ValidateRetainedPlaybackBinding(ctx, *task.Items[0].SourceBinding, task.CourseName)
	if err != nil {
		return nil, videoPptError(err)
	}
	return verified.Declaration(), nil
}
