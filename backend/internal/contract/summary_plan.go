package contract

import "context"

type SummaryPlanPolicy struct {
	PlanPolicy                        string
	HaloBudget                        int
	BoundaryPromptSHA256              string
	PlanSHA256                        string
	BoundaryStages, BoundaryFallbacks int
}
type summaryPlanKey struct{}
type summaryPlanValue struct {
	Policy SummaryPlanPolicy
	Chunks any
}

func WithSummaryPlan(ctx context.Context, policy SummaryPlanPolicy, chunks any) context.Context {
	return context.WithValue(ctx, summaryPlanKey{}, summaryPlanValue{policy, chunks})
}
func SummaryPlanFrom(ctx context.Context) (SummaryPlanPolicy, any) {
	value, _ := ctx.Value(summaryPlanKey{}).(summaryPlanValue)
	return value.Policy, value.Chunks
}

type summaryPlanPoliciesKey struct{}

func WithSummaryPlanPolicies(ctx context.Context, policies map[string]SummaryPlanPolicy) context.Context {
	copy := map[string]SummaryPlanPolicy{}
	for k, v := range policies {
		copy[k] = v
	}
	return context.WithValue(ctx, summaryPlanPoliciesKey{}, copy)
}
func SummaryPlanPolicyFrom(ctx context.Context, mode string) (SummaryPlanPolicy, bool) {
	policies, _ := ctx.Value(summaryPlanPoliciesKey{}).(map[string]SummaryPlanPolicy)
	value, ok := policies[mode]
	return value, ok
}
