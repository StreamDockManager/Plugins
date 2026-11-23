package observation

import "context"

type refreshKey struct{}

// Refresh requests an authoritative platform read for an explicit user action.
func Refresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, refreshKey{}, true)
}
