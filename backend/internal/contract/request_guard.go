package contract

import "context"

// PhysicalRequestGuard is scoped to the stable job, across retries and JSON
// repairs. An unconfirmed dispatched attempt must remain held after cancellation.
// Bytes are local resource guards; these interfaces make no token/billing claim.
type PhysicalRequestGuard interface {
	Reserve(context.Context, string, int64) (PhysicalRequestAttempt, error)
}

type PhysicalRequestAttempt interface {
	Dispatch(context.Context) error
	Finish(context.Context, bool) error
}

type physicalRequestGuardKey struct{}

func WithPhysicalRequestGuard(ctx context.Context, guard PhysicalRequestGuard) context.Context {
	return context.WithValue(ctx, physicalRequestGuardKey{}, guard)
}

func PhysicalRequestGuardFrom(ctx context.Context) PhysicalRequestGuard {
	guard, _ := ctx.Value(physicalRequestGuardKey{}).(PhysicalRequestGuard)
	return guard
}
