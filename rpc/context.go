package rpc

import (
	"context"
	"net"

	"github.com/pion/webrtc/v3"
)

type ctxKey int

const (
	ctxKeyHost = ctxKey(iota)
	ctxKeyDialer
	ctxKeyResolver
	ctxKeyPeerConnection
)

// contextWithHost attaches a host name to the given context.
func contextWithHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, ctxKeyHost, host)
}

// contextHost returns a host name. It may be nil if the value was never set.
func contextHost(ctx context.Context) string {
	host := ctx.Value(ctxKeyHost)
	if host == nil {
		return ""
	}
	return host.(string)
}

// ContextWithDialer attaches a Dialer to the given context.
func ContextWithDialer(ctx context.Context, d Dialer) context.Context {
	return context.WithValue(ctx, ctxKeyDialer, d)
}

// contextDialer returns a Dialer. It may be nil if the value was never set.
func contextDialer(ctx context.Context) Dialer {
	dialer := ctx.Value(ctxKeyDialer)
	if dialer == nil {
		return nil
	}
	return dialer.(Dialer)
}

// contextWithResolver attaches a Resolver to the given context.
func contextWithResolver(ctx context.Context, r *net.Resolver) context.Context {
	return context.WithValue(ctx, ctxKeyResolver, r)
}

// contextResolver returns a Resolver. It may be nil if the value was never set.
func contextResolver(ctx context.Context) *net.Resolver {
	resolver := ctx.Value(ctxKeyResolver)
	if resolver == nil {
		return nil
	}
	return resolver.(*net.Resolver)
}

// contextWithPeerConnection attaches a peer connection to the given context.
func contextWithPeerConnection(ctx context.Context, pc *webrtc.PeerConnection) context.Context {
	return context.WithValue(ctx, ctxKeyPeerConnection, pc)
}

// ContextPeerConnection returns a peer connection, if set.
func ContextPeerConnection(ctx context.Context) (*webrtc.PeerConnection, bool) {
	pc := ctx.Value(ctxKeyPeerConnection)
	if pc == nil {
		return nil, false
	}
	return pc.(*webrtc.PeerConnection), true
}
