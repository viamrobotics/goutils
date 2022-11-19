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
	ctxKeyAuthUniqueID
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
// to exist in auth middleware and should not be saved unless the confidentiality of the
// claims is not important.
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
// it panics if there is none set. This value is opaque and therefore should not be inspected
// beyond equality checks.
func MustContextAuthEntity(ctx context.Context) interface{} {
	authEntity, err := contextAuthEntity(ctx)
	if err != nil {
		panic(err)
	}
	return authEntity
}

// ContextWithAuthUniqueID attaches a unique ID for an authenticated entity to the given context.
func ContextWithAuthUniqueID(ctx context.Context, authUniqueID interface{}) context.Context {
	return context.WithValue(ctx, ctxKeyAuthUniqueID, authUniqueID)
}

// ContextAuthUniqueID returns the unique ID for the entity associated with this authentication context.
func ContextAuthUniqueID(ctx context.Context) (string, bool) {
	authUniqueID, ok := ctx.Value(ctxKeyAuthUniqueID).(string)
	if !ok || authUniqueID == "" {
		return "", false
	}
	return authUniqueID, true
}

// MustContextAuthUniqueID returns the unique ID for the entity associated with this authentication context;
// it panics if there is none set.
func MustContextAuthUniqueID(ctx context.Context) string {
	authUniqueID, has := ContextAuthUniqueID(ctx)
	if !has {
		panic(errors.New("no auth unique ID present"))
	}
	return authUniqueID
}
