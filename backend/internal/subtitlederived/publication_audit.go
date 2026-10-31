package subtitlederived

import (
	"context"
	"errors"
	"time"
)

// This is a journal declaration. It makes no claim about current file bytes.
type PublicationRecord struct {
	State           string `json:"state"`
	PlanSHA256      string `json:"planSHA256,omitempty"`
	ReceiptSHA256   string `json:"receiptSHA256,omitempty"`
	PointCount      int    `json:"pointCount,omitempty"`
	MDPositionScope string `json:"mdPositionScope"`
}

func RecordedPublication(item Item, outputVersion int) *PublicationRecord {
	if item.PublicationPlan == nil && item.PublicationReceipt == nil {
		return nil
	}
	value := &PublicationRecord{State: "unsupported", MDPositionScope: "none"}
	if outputVersion == 2 {
		value.MDPositionScope = "anchor-appendix"
	} else if outputVersion != 0 {
		return value
	}
	if (item.PublicationPlan != nil && item.PublicationPlan.Version != 1) || (item.PublicationReceipt != nil && item.PublicationReceipt.Version != 1) {
		return value
	}
	value.State = "invalid"
	if item.PublicationPlan == nil || !recipeHashPattern.MatchString(item.PublicationPlan.SHA256) {
		return value
	}
	value.PlanSHA256 = item.PublicationPlan.SHA256
	if item.PublicationReceipt != nil {
		if !recipeHashPattern.MatchString(item.PublicationReceipt.SHA256) {
			return value
		}
		value.ReceiptSHA256 = item.PublicationReceipt.SHA256
		value.State = "recorded"
	} else {
		value.State = "pending"
	}
	if item.SummaryEvidence != nil && item.SummaryEvidence.PointCount >= 0 && item.SummaryEvidence.PointCount <= 280000 {
		value.PointCount = item.SummaryEvidence.PointCount
	}
	return value
}

type PublicationAudit struct {
	State             string `json:"state"`
	Reason            string `json:"reason,omitempty"`
	AttemptedAt       string `json:"attemptedAt"`
	CheckedAt         string `json:"checkedAt,omitempty"`
	TaskID            string `json:"taskID"`
	ItemIndex         int    `json:"itemIndex"`
	PlanSHA256        string `json:"planSHA256,omitempty"`
	ReceiptSHA256     string `json:"receiptSHA256,omitempty"`
	SourceSHA256      string `json:"sourceSHA256,omitempty"`
	CheckedFiles      int    `json:"checkedFiles"`
	VerifiedPoints    int    `json:"verifiedPoints"`
	MDPositionScope   string `json:"mdPositionScope"`
	JSONPositionScope string `json:"jsonPositionScope,omitempty"`
	RequestChainState string `json:"requestChainState"`
}

func (service *Service) AuditPublication(ctx context.Context, task Task, index int) (result PublicationAudit) {
	result = PublicationAudit{State: "unknown", Reason: "no-receipt", AttemptedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskID: task.ID, ItemIndex: index, MDPositionScope: "none", RequestChainState: "unknown"}
	defer func() {
		if err := ctx.Err(); err != nil {
			result.State = "unknown"
			result.Reason = "cancelled"
			if errors.Is(err, context.DeadlineExceeded) {
				result.Reason = "deadline"
			}
			result.CheckedAt = ""
			result.CheckedFiles = 0
			result.VerifiedPoints = 0
		}
	}()
	if ctx.Err() != nil {
		return result
	}
	if !validUUID(task.ID) || index < 0 || index >= len(task.Items) || len(task.Items) > 4000 {
		result.State = "invalid"
		result.Reason = "scope-invalid"
		return result
	}
	item := &task.Items[index]
	record := RecordedPublication(*item, task.OutputVersion)
	if record == nil {
		return result
	}
	result.PlanSHA256 = record.PlanSHA256
	result.ReceiptSHA256 = record.ReceiptSHA256
	result.MDPositionScope = record.MDPositionScope
	if record.State == "unsupported" {
		result.Reason = "unsupported"
		return result
	}
	if record.State == "invalid" {
		result.State = "invalid"
		result.Reason = "reference-invalid"
		return result
	}
	if record.State == "pending" {
		result.State = "incomplete"
		result.Reason = "publication-pending"
		return result
	}
	// Exactly one full request-chain traversal. The collected immutable facts
	// are passed through plan/point validation, rather than audited again there.
	checked := service.auditAttemptEvidence(ctx, task, index, true)
	result.RequestChainState = checked.State
	if checked.State != "complete" {
		result.State = checked.State
		result.Reason = "request-chain-invalid"
		if checked.State == "unknown" {
			result.Reason = "request-chain-unverifiable"
			if checked.Reason == "source-unverifiable" {
				result.Reason = "source-unavailable"
			}
			if checked.Reason == "protocol-unsupported" || checked.Reason == "policy-unsupported" {
				result.Reason = "unsupported"
			}
		}
		if checked.State == "incomplete" {
			result.Reason = "request-chain-incomplete"
		}
		return result
	}
	if service.verifyPublication(ctx, &task, index, checked.sourceText, &checked) != nil {
		result.State = "invalid"
		result.Reason = "publication-invalid"
		return result
	}
	result.State = "complete"
	result.Reason = ""
	result.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	result.SourceSHA256 = item.Source.Hash
	result.CheckedFiles = len(item.Outputs)
	result.VerifiedPoints = item.SummaryEvidence.PointCount
	result.JSONPositionScope = "point-array"
	return result
}
