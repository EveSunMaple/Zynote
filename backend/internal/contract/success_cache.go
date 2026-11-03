package contract

import "context"

// SuccessCache stores validated local results independently of ordered progress.
// Adoption records a new execution owner without inventing provider calls.
type SuccessCache interface {
	Save(context.Context, string, string, []byte) error
	Adopt(context.Context, string, string) ([]byte, bool, error)
}
type successCacheKey struct{}

func WithSuccessCache(ctx context.Context, cache SuccessCache) context.Context {
	return context.WithValue(ctx, successCacheKey{}, cache)
}
func SuccessCacheFrom(ctx context.Context) SuccessCache {
	cache, _ := ctx.Value(successCacheKey{}).(SuccessCache)
	return cache
}
