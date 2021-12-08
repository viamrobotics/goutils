package rpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-errors/errors"
	"github.com/golang-jwt/jwt/v4"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	rpcpb "go.viam.com/utils/proto/rpc/v1"
)

func (ss *simpleServer) authHandler(forType CredentialsType) (AuthHandler, error) {
	handler, ok := ss.authHandlers[forType]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "no way to authenticate with %q", forType)
	}
	return handler, nil
}

const (
	metadataFieldAuthorization     = "authorization"
	authorizationValuePrefixBearer = "Bearer "
)

type rpcClaims struct {
	jwt.RegisteredClaims
	CredentialsType CredentialsType `json:"rpc_creds_type,omitempty"`
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
	if err := handler.Authenticate(ctx, req.Entity, req.Credentials.Payload); err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.PermissionDenied, "failed to authenticate: %s", err.Error())
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, rpcClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{req.Entity},
		},
		CredentialsType: CredentialsType(req.Credentials.Type),
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

func (ss *simpleServer) authUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if !ss.exemptMethods[info.FullMethod] {
		if err := ss.ensureAuthed(ctx); err != nil {
			return nil, err
		}
	}
	return handler(ctx, req)
}

func (ss *simpleServer) authStreamInterceptor(srv interface{}, serverStream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if !ss.exemptMethods[info.FullMethod] {
		if err := ss.ensureAuthed(serverStream.Context()); err != nil {
			return err
		}
	}
	return handler(srv, serverStream)
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

func (ss *simpleServer) ensureAuthed(ctx context.Context) error {
	tokenString, err := tokenFromContext(ctx)
	if err != nil {
		return err
	}

	var claims rpcClaims
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
		return status.Errorf(codes.Unauthenticated, "unauthenticated: %s", err)
	}
	if len(claims.Audience) == 0 {
		return errors.New("invalid jwt claims; no audience")
	}

	return handler.VerifyEntity(ctx, claims.Audience[0])
}
