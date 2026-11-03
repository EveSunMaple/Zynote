package subtitlederived

import (
	"context"
	"math"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type SummaryResult struct {
	Markdown string
	Points   []Point
	Facts    *contract.SummaryEvidence
}
type ReceivedSummary struct {
	Version  int                       `json:"version"`
	Markdown string                    `json:"markdown"`
	Points   []Point                   `json:"points"`
	Facts    *contract.SummaryEvidence `json:"facts,omitempty"`
}

func canonicalDerivedPoints(points []Point) ([]contract.ValidatedSummaryPoint, error) {
	rows := make([]contract.ValidatedSummaryPoint, 0, len(points))
	for _, point := range points {
		if math.IsNaN(point.Cue) || math.IsInf(point.Cue, 0) || point.Cue < 0 || point.Cue != math.Trunc(point.Cue) || point.Cue > 20000 {
			return nil, observerError()
		}
		rows = append(rows, contract.ValidatedSummaryPoint{Cue: int(point.Cue), Quote: point.Quote, Point: point.Point, Topic: point.Topic, Timestamp: point.Timestamp})
	}
	return rows, nil
}
func (service *Service) verifyPointEvidence(ctx context.Context, task *Task, item *Item, points []Point, facts *contract.SummaryEvidence) error {
	return service.verifyPointEvidenceAudit(ctx, task, item, points, facts, nil)
}
func (service *Service) verifyPointEvidenceAudit(ctx context.Context, task *Task, item *Item, points []Point, facts *contract.SummaryEvidence, checked *AttemptAudit) error {
	if facts == nil || facts.Version != 1 || facts.SourceSHA256 != item.Source.Hash || item.Recipe == nil || facts.ItemRecipeHash != item.Recipe.Hash || facts.PointCount != len(points) || item.AttemptEvidence == nil || (item.AttemptEvidence.Version != 2 && item.AttemptEvidence.Version != 3) || facts.ValidatedHead != (contract.GenerationEventRef{Version: item.AttemptEvidence.Version, Sequence: item.AttemptEvidence.EventCount, SHA256: item.AttemptEvidence.HeadSHA256}) {
		return observerError()
	}
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 {
		return observerError()
	}
	var audit AttemptAudit
	if checked != nil {
		audit = *checked
	} else {
		audit = service.auditAttemptEvidence(ctx, *task, index, true)
	}
	if audit.State != "complete" || len(audit.validated) != audit.TotalChunks {
		return observerError()
	}
	rows, err := canonicalDerivedPoints(points)
	if err != nil {
		return err
	}
	hash, err := contract.SummaryPointsDigest(rows)
	if err != nil || hash != facts.FinalPointsSHA256 {
		return observerError()
	}
	offset := 0
	for _, chunk := range audit.validated {
		start := offset
		// Preserve exactly the original per-chunk verified order, including
		// repeated cue IDs. No sorting or deduplication is introduced here.
		if chunk.PointCount < 0 || offset+chunk.PointCount > len(rows) {
			return observerError()
		}
		for offset < start+chunk.PointCount {
			if rows[offset].Cue < chunk.Start || rows[offset].Cue >= chunk.Start+chunk.CueCount {
				return observerError()
			}
			offset++
		}
		digest, err := contract.SummaryPointsDigest(rows[start:offset])
		if err != nil || offset-start != chunk.PointCount || digest != chunk.PointsSHA256 {
			return observerError()
		}
	}
	if offset != len(rows) {
		return observerError()
	}
	if err := service.verifySummaryTree(ctx, task, item, rows, facts, audit); err != nil {
		return err
	}
	globalPartial, err := service.verifyGlobalSummary(ctx, task, item, facts, audit, points)
	if err != nil {
		return err
	}
	if facts.Partial != (audit.InsufficientChunks > 0 || globalPartial) {
		return observerError()
	}
	return nil
}
func (service *Service) saveReceived(ctx context.Context, task *Task, item *Item, result SummaryResult) error {
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 {
		return observerError()
	}
	var facts *contract.SummaryEvidence
	if result.Facts != nil {
		factsCopy := *result.Facts
		facts = &factsCopy
	}
	received := &ReceivedSummary{Version: 1, Markdown: result.Markdown, Points: append([]Point(nil), result.Points...), Facts: facts}
	candidate := *task
	candidate.Items = append([]Item(nil), task.Items...)
	candidate.Items[index].Received = received
	candidate.Items[index].GenerationState = "received"
	if err := service.save(candidate); err != nil {
		return contract.ObservationFailure(err)
	}
	item.Received = received
	item.GenerationState = "received"
	return service.prepareReceived(ctx, task, item)
}
func (service *Service) prepareReceived(ctx context.Context, task *Task, item *Item) error {
	received := item.Received
	if received == nil || received.Version != 1 {
		return observerError()
	}
	if err := service.verifyPointEvidence(ctx, task, item, received.Points, received.Facts); err != nil {
		return err
	}
	index := -1
	for i := range task.Items {
		if &task.Items[i] == item {
			index = i
			break
		}
	}
	if index < 0 {
		return observerError()
	}
	facts := *received.Facts
	prepared := &Prepared{Markdown: received.Markdown, Points: append([]Point(nil), received.Points...), Evidence: &facts}
	candidate := *task
	candidate.Items = append([]Item(nil), task.Items...)
	candidate.Items[index].Prepared = prepared
	candidate.Items[index].Received = nil
	candidate.Items[index].GenerationState = "prepared"
	candidate.Items[index].SummaryEvidence = &facts
	if err := service.save(candidate); err != nil {
		return contract.ObservationFailure(err)
	}
	item.Prepared = prepared
	item.Received = nil
	item.GenerationState = "prepared"
	item.SummaryEvidence = &facts
	return nil
}
