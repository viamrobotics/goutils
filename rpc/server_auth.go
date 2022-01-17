package rpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v4"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	rpcpb "go.viam.com/utils/proto/rpc/v1"
)

func (ss *simpleServer) authHandler(forType CredentialsType) (AuthHandler, error) {
	handler, ok := ss.authHandlers[forType]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "no auth handler for %q", forType)
	}
	return handler, nil
}

const (
	metadataFieldAuthorization     = "authorization"
	authorizationValuePrefixBearer = "Bearer "
)

// JWTClaims extends jwt.RegisteredClaims with information about the credentials as well
// as authentication metadata.
type JWTClaims struct {
	jwt.RegisteredClaims
	CredentialsType CredentialsType   `json:"rpc_creds_type,omitempty"`
	AuthMetadata    map[string]string `json:"rpc_auth_md,omitempty"`
}

func (ss *simpleServer) Authenticate(ctx context.Context, req *rpcpb.AuthenticateRequest) (*rpcpb.AuthenticateResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("expected metadata")
	}
	if len(md[metadataFieldAuthorization]) != 0 {
		return nil, status.Error(codes.InvalidArgument, "already authenticated; cannot re-authenticate")
	}
	handler, err := ss.authHandler(CredentialsType(req.Credentials.Type))
	if err != nil {
		return nil, err
	}
	authMD, err := handler.Authenticate(ctx, req.Entity, req.Credentials.Payload)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.PermissionDenied, "failed to authenticate: %s", err.Error())
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{req.Entity},
		},
		CredentialsType: CredentialsType(req.Credentials.Type),
		AuthMetadata:    authMD,
		// TODO(https://github.com/viamrobotics/goutils/issues/10): expiration
		// TODO(https://github.com/viamrobotics/goutils/issues/11): refresh token
		// TODO(https://github.com/viamrobotics/goutils/issues/14): more complete info
	})

	tokenString, err := token.SignedString(ss.authRSAPrivKey)
	if err != nil {
		ss.logger.Errorw("failed to sign JWT", "error", err)
		return nil, status.Error(codes.PermissionDenied, "failed to authenticate")
	}

	return &rpcpb.AuthenticateResponse{
		AccessToken: tokenString,
	}, nil
}

func (ss *simpleServer) AuthenticateTo(ctx context.Context, req *rpcpb.AuthenticateToRequest) (*rpcpb.AuthenticateToResponse, error) {
	audience, err := ss.authToHandler(ctx, req.Entity)
	if err != nil {
		return nil, err
	}
	if audience == "" {
		audience = req.Entity
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{audience},
		},
		CredentialsType: ss.authToType,
		// TODO(https://github.com/viamrobotics/goutils/issues/10): expiration
		// TODO(https://github.com/viamrobotics/goutils/issues/11): refresh token
		// TODO(https://github.com/viamrobotics/goutils/issues/14): more complete info
	})

	tokenString, err := token.SignedString(ss.authRSAPrivKey)
	if err != nil {
		ss.logger.Errorw("failed to sign JWT", "error", err)
		return nil, status.Error(codes.PermissionDenied, "failed to authenticate")
	}

	return &rpcpb.AuthenticateToResponse{
		AccessToken: tokenString,
	}, nil
}

func (ss *simpleServer) authUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	if !ss.exemptMethods[info.FullMethod] {
		authEntity, err := ss.ensureAuthed(ctx)
		if err != nil {
			return nil, err
		}
		ctx = ContextWithAuthEntity(ctx, authEntity)
	}
	return handler(ctx, req)
}

func (ss *simpleServer) authStreamInterceptor(
	srv interface{},
	serverStream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if !ss.exemptMethods[info.FullMethod] {
		authEntity, err := ss.ensureAuthed(serverStream.Context())
		if err != nil {
			return err
		}
		ctx := ContextWithAuthEntity(serverStream.Context(), authEntity)
		serverStream = ctxWrappedServerStream{serverStream, ctx}
	}
	return handler(srv, serverStream)
}

type ctxWrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (wrapped ctxWrappedServerStream) Context() context.Context {
	return wrapped.ctx
}

func tokenFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	authHeader := md.Get(metadataFieldAuthorization)
	if len(authHeader) != 1 {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	if !strings.HasPrefix(authHeader[0], authorizationValuePrefixBearer) {
		return "", status.Errorf(codes.Unauthenticated, "expected Authorization: %s", authorizationValuePrefixBearer)
	}
	return strings.TrimPrefix(authHeader[0], authorizationValuePrefixBearer), nil
}

func (ss *simpleServer) ensureAuthed(ctx context.Context) (interface{}, error) {
	tokenString, err := tokenFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var claims JWTClaims
	var handler AuthHandler
	if _, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
		var err error
		handler, err = ss.authHandler(claims.CredentialsType)
		if err != nil {
			return nil, err
		}

		if provider, ok := handler.(TokenVerificationKeyProvider); ok {
			return provider.TokenVerificationKey(token)
		}

		// signed internally
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}

		return &ss.authRSAPrivKey.PublicKey, nil
	}); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "unauthenticated: %s", err)
	}
	if len(claims.Audience) == 0 {
		return nil, errors.New("invalid jwt claims; no audience")
	}

	if claims.AuthMetadata != nil {
		ctx = contextWithAuthMetadata(ctx, claims.AuthMetadata)
	}

	return handler.VerifyEntity(ctx, claims.Audience[0])
}
