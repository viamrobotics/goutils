package web

import (
	"context"
	"net/http"
)

// JWTSessionManager handles validating jwt sessions.
type JWTSessionManager interface {
	IsValid(ctx context.Context, r *http.Request) bool
	Save(ctx context.Context, r *http.Request)
	Invalidate(ctx context.Context, r *http.Request)
}
