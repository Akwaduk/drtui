package pluginapi

import "context"

type Invocation struct {
	RunID          string
	StepInstanceID string
	IdempotencyKey string
	Attempt        uint16
}

type invocationContextKey struct{}

func WithInvocation(ctx context.Context, invocation Invocation) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, invocation)
}

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	invocation, ok := ctx.Value(invocationContextKey{}).(Invocation)
	return invocation, ok
}
