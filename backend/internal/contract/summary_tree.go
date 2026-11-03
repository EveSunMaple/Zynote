package contract

import "context"

type SummaryTreeEvidence struct {
	RootSHA256, LeafSetSHA256, DispositionSHA256 string
	Generation, Epoch                            int64
	Leaves, Pages, Nodes                         int
}
type SummaryTree interface {
	Append(context.Context, int, []ValidatedSummaryPoint) error
	Finish(context.Context, func(ValidatedSummaryPoint) error) (SummaryTreeEvidence, error)
}
type summaryTreeKey struct{}

func WithSummaryTree(ctx context.Context, tree SummaryTree) context.Context {
	return context.WithValue(ctx, summaryTreeKey{}, tree)
}
func SummaryTreeFrom(ctx context.Context) SummaryTree {
	tree, _ := ctx.Value(summaryTreeKey{}).(SummaryTree)
	return tree
}
