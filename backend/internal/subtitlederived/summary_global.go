package subtitlederived

import (
	"context"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryglobal"
	"github.com/EveSunMaple/Zynote/backend/internal/summaryinput"
)

func (service *Service) verifyGlobalSummary(ctx context.Context, task *Task, item *Item, facts *contract.SummaryEvidence, audit AttemptAudit, points []Point) (bool, error) {
	if item.Recipe.ModeRecipe.Version < 3 {
		if facts.GlobalRootSHA256 != "" {
			return false, observerError()
		}
		return false, nil
	}
	if facts.GlobalRootSHA256 == "" || facts.TreeRootSHA256 == "" {
		return false, observerError()
	}
	factory := newSummaryStageFactory(service, task, item)
	var manifest summaryglobal.Manifest
	if summaryglobal.Load(ctx, factory.LoadArtifact, facts.GlobalRootSHA256, &manifest) != nil || manifest.Version != 1 || manifest.SourceSHA256 != item.Source.Hash || manifest.RecipeSHA256 != item.Recipe.Hash || manifest.TreeRoot != facts.TreeRootSHA256 {
		return false, observerError()
	}
	segments, err := documents.ParseSrt(audit.sourceText)
	if err != nil {
		return false, observerError()
	}
	chunks, err := summaryinput.PlanSpans(item.Mode, segments, item.Recipe.ModeRecipe.ChunkLimit)
	if item.Recipe.ModeRecipe.Version == 4 {
		chunks, err = service.validatedSummaryPlan(ctx, *task, item, segments)
	}
	if err != nil {
		return false, observerError()
	}
	check := func(ctx context.Context, ref summaryglobal.StageRef) error {
		return factory.Check(ctx, ref.ID, ref.Format, ref.Purpose, ref.InputSHA256, ref.JSONSHA256)
	}
	stats, err := summaryglobal.AuditRoot(ctx, factory.LoadArtifact, check, item.Source.Hash, item.Recipe.Hash, manifest.TreeRoot, manifest.RootRecord, chunks)
	if stats.UpperUnreviewed != manifest.UpperUnreviewed || stats.UpperUnreviewed != facts.GlobalUpperUnreviewed {
		return false, observerError()
	}
	if len(stats.OrderedReviews) != len(points) {
		return false, observerError()
	}
	for i, point := range points {
		if point.ReviewState != stats.OrderedReviews[i] {
			return false, observerError()
		}
	}
	expectedState := "complete_selected_review"
	if stats.Partial {
		expectedState = "partial"
	}
	if err != nil || stats.Leaves != manifest.Leaves || stats.Leaves != facts.PointCount || stats.Omitted != manifest.Omitted || stats.Unreviewed != manifest.Unreviewed || stats.Stages != manifest.Stages || stats.Partial != manifest.Partial || stats.DispositionSHA256 != manifest.LeafDispositionSHA256 || facts.GlobalState != expectedState || facts.GlobalStages != stats.Stages || facts.GlobalUnreviewed != stats.Unreviewed {
		return false, observerError()
	}
	return stats.Partial, nil
}
