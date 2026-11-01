package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlederived"
)

func (inv *Invoker) withPublicationAudit(run func() (any, error)) (any, error) {
	inv.publicationAuditOnce.Do(func() { inv.publicationAuditGate = make(chan struct{}, 1) })
	select {
	case inv.publicationAuditGate <- struct{}{}:
		defer func() { <-inv.publicationAuditGate }()
		return run()
	default:
		return nil, contract.NewError(contract.CodeBusy, "另一项成果引用正在核验，请稍后重试")
	}
}

type publicationAuditInput struct {
	ID                    string `json:"id"`
	ItemIndex             int    `json:"itemIndex"`
	ExpectedPlanSHA256    string `json:"expectedPlanSHA256"`
	ExpectedReceiptSHA256 string `json:"expectedReceiptSHA256"`
}

func (inv *Invoker) handlePublicationAudit(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var parsed struct {
		ID                    string `json:"id"`
		ItemIndex             *int   `json:"itemIndex"`
		ExpectedPlanSHA256    string `json:"expectedPlanSHA256"`
		ExpectedReceiptSHA256 string `json:"expectedReceiptSHA256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&parsed) != nil || decoder.Decode(&struct{}{}) != io.EOF || parsed.ItemIndex == nil || *parsed.ItemIndex < 0 || *parsed.ItemIndex >= 4000 || len(parsed.ExpectedPlanSHA256) > 64 || len(parsed.ExpectedReceiptSHA256) > 64 {
		return nil, contract.InputInvalid("成果核验参数无效")
	}
	input := publicationAuditInput{ID: parsed.ID, ItemIndex: *parsed.ItemIndex, ExpectedPlanSHA256: parsed.ExpectedPlanSHA256, ExpectedReceiptSHA256: parsed.ExpectedReceiptSHA256}
	return inv.withPublicationAudit(func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return inv.readPublicationAudit(ctx, state, input)
	})
}
func (inv *Invoker) readPublicationAudit(ctx context.Context, state *contract.State, input publicationAuditInput) (any, error) {
	service := subtitleDerivedService(state)
	task, err := service.Read(input.ID)
	if err != nil || input.ItemIndex < 0 || input.ItemIndex >= len(task.Items) {
		return nil, contract.InputInvalid("成果核验任务无效")
	}
	record := subtitlederived.RecordedPublication(task.Items[input.ItemIndex], task.OutputVersion)
	plan, receipt := "", ""
	if record != nil {
		plan = record.PlanSHA256
		receipt = record.ReceiptSHA256
	}
	if plan != input.ExpectedPlanSHA256 || receipt != input.ExpectedReceiptSHA256 {
		return subtitlederived.PublicationAudit{State: "unknown", Reason: "stale-record", TaskID: task.ID, ItemIndex: input.ItemIndex, AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano), MDPositionScope: "none", RequestChainState: "unknown"}, nil
	}
	result := service.AuditPublication(ctx, task, input.ItemIndex)
	if result.State == "complete" {
		current, err := service.Read(input.ID)
		var latest *subtitlederived.PublicationRecord
		if err == nil && input.ItemIndex < len(current.Items) {
			latest = subtitlederived.RecordedPublication(current.Items[input.ItemIndex], current.OutputVersion)
		}
		if latest == nil || latest.PlanSHA256 != plan || latest.ReceiptSHA256 != receipt {
			result.State = "unknown"
			result.Reason = "stale-record"
			result.CheckedAt = ""
			result.VerifiedPoints = 0
			result.CheckedFiles = 0
		}
	}
	if ctx.Err() != nil {
		result.State = "unknown"
		result.Reason = "cancelled"
		if ctx.Err() == context.DeadlineExceeded {
			result.Reason = "deadline"
		}
		result.CheckedAt = ""
		result.VerifiedPoints = 0
		result.CheckedFiles = 0
	}
	return result, nil
}
