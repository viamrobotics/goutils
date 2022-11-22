package rpc

import (
	"context"
	"errors"

	"github.com/pion/webrtc/v3"
)

type ctxKey int

const (
	ctxKeyHost = ctxKey(iota)
	ctxKeyDialer
	ctxKeyPeerConnection
	ctxKeyAuthEntity
	ctxKeyAuthClaims // all jwt claims
	ctxKeyAuthSubject
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

// contextWithAuthClaims attaches authentication jwt claims to the given context.
func contextWithAuthClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, ctxKeyAuthClaims, claims)
}

// ContextAuthClaims returns authentication jwt claims. This context value is only expected
// to exist in auth middleware. If the claims are possibly confidential (e.g. from a JOSE),
// care should be taken to expose only safe, public claims.
func ContextAuthClaims(ctx context.Context) Claims {
	claims := ctx.Value(ctxKeyAuthClaims)
	if claims == nil {
		return nil
	}
	return claims.(Claims)
}

// ContextWithAuthEntity attaches authentication metadata to the given context.
func ContextWithAuthEntity(ctx context.Context, authEntity interface{}) context.Context {
	return context.WithValue(ctx, ctxKeyAuthEntity, authEntity)
}

// contextAuthEntity returns the authentication entity associated with this context.
func contextAuthEntity(ctx context.Context) (interface{}, error) {
	authEntity := ctx.Value(ctxKeyAuthEntity)
	if authEntity == nil {
		return nil, errors.New("no auth entity")
	}
	return authEntity, nil
}

// MustContextAuthEntity returns the authentication entity associated with this context;
// it panics if there is none set. This value is specific to the handler used and should
// be etype checked.
func MustContextAuthEntity(ctx context.Context) interface{} {
	authEntity, err := contextAuthEntity(ctx)
	if err != nil {
		panic(err)
	}
	return authEntity
}

// ContextWithAuthSubject attaches a subject (e.g. a user) for an authenticated context to the given context.
func ContextWithAuthSubject(ctx context.Context, authSubject string) context.Context {
	return context.WithValue(ctx, ctxKeyAuthSubject, authSubject)
}

// ContextAuthSubject returns the subject (e.g. a user) associated with this authentication context.
func ContextAuthSubject(ctx context.Context) (string, bool) {
	authSubject, ok := ctx.Value(ctxKeyAuthSubject).(string)
	if !ok || authSubject == "" {
		return "", false
	}
	return authSubject, true
}

// MustContextAuthSubject returns the subject associated with this authentication context;
// it panics if there is none set.
func MustContextAuthSubject(ctx context.Context) string {
	authSubject, has := ContextAuthSubject(ctx)
	if !has {
		panic(errors.New("no auth subject present"))
	}
	return authSubject
}
