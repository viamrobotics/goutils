package rpc

import "context"

type ctxKey int

const ctxKeyHost = ctxKey(iota)

// ContextWithHost attaches a host name to the given context.
func ContextWithHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, ctxKeyHost, host)
}

// ContextHost returns a host name. It may be nil if the value was never set.
func ContextHost(ctx context.Context) string {
	host := ctx.Value(ctxKeyHost)
	if host == nil {
		return ""
	}
	return host.(string)
}
