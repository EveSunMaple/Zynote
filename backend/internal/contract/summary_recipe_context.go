package contract

import "context"

type summaryRecipeVersionsKey struct{}

func WithSummaryRecipeVersions(ctx context.Context, versions map[string]int) context.Context {
	copy := map[string]int{}
	for mode, version := range versions {
		copy[mode] = version
	}
	return context.WithValue(ctx, summaryRecipeVersionsKey{}, copy)
}
func SummaryRecipeVersion(ctx context.Context, mode string) int {
	versions, _ := ctx.Value(summaryRecipeVersionsKey{}).(map[string]int)
	return versions[mode]
}
