package contract

import "context"

// SummaryStageFactory opens an independent immutable audit stream. It cannot
// impersonate extraction or reset the stable physical-request budget.
type SummaryStageFactory interface {
	Open(context.Context, string, int, string, string) (context.Context, error)
	SaveArtifact(context.Context, []byte) (string, error)
	LoadArtifact(context.Context, string) ([]byte, error)
	Check(context.Context, string, GenerationEventRef, string, string, string) error
}
type summaryStageKey struct{}
type summaryStageAttemptKey struct{}

func WithSummaryStageAttempt(ctx context.Context, attempted bool) context.Context {
	return context.WithValue(ctx, summaryStageAttemptKey{}, attempted)
}
func SummaryStageAttempted(ctx context.Context) bool {
	value, _ := ctx.Value(summaryStageAttemptKey{}).(bool)
	return value
}

func WithSummaryStages(ctx context.Context, factory SummaryStageFactory) context.Context {
	return context.WithValue(ctx, summaryStageKey{}, factory)
}
func SummaryStagesFrom(ctx context.Context) SummaryStageFactory {
	factory, _ := ctx.Value(summaryStageKey{}).(SummaryStageFactory)
	return factory
}
