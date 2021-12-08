package rpc

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"fmt"

	"github.com/golang-jwt/jwt/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An AuthHandler is responsible for authenticating an RPC connection. That means
// that if the idea of multiple entities can be involved in one connection, that
// this is not a suitable abstraction to use.
type AuthHandler interface {
	// Authenticate returns nil if the given payload is valid authentication material.
	Authenticate(ctx context.Context, entity, payload string) error

	// VerifyEntity verifies that this handler is allowed to authenticate the given entity.
	VerifyEntity(ctx context.Context, entity string) error
}

// TokenVerificationKeyProvider allows an AuthHandler to supply a key needed to peform
// verification of a JWT. This is helpful when the server itself is not responsible
// for authentication. For example, this could be for a central auth server
// with untrusted peers using a public key to verify JWTs.
type TokenVerificationKeyProvider interface {
	// TokenVerificationKey returns the key needed to do JWT verification.
	TokenVerificationKey(token *jwt.Token) (interface{}, error)
}

var (
	errInvalidCredentials           = status.Error(codes.Unauthenticated, "invalid credentials")
	errSessionEntityHandlerMismatch = status.Error(codes.Unauthenticated, "session entity/auth handler mismatch")
)

// MakeFuncAuthHandler encapsulates AuthHandler functionality to a set of functions.
func MakeFuncAuthHandler(
	auth func(ctx context.Context, entity, payload string) error,
	verify func(ctx context.Context, entity string) error,
) AuthHandler {
	return funcAuthHandler{auth: auth, verify: verify}
}

type funcAuthHandler struct {
	auth   func(ctx context.Context, entity, payload string) error
	verify func(ctx context.Context, entity string) error
}

// Authenticate checks if the given entity and payload are what it expects. It returns
// an error otherwise.
func (h funcAuthHandler) Authenticate(ctx context.Context, entity, payload string) error {
	return h.auth(ctx, entity, payload)
}

// VerifyEntity checks if the given entity is handled by this handler.
func (h funcAuthHandler) VerifyEntity(ctx context.Context, entity string) error {
	return h.verify(ctx, entity)
}

// WithTokenVerificationKeyProvider returns an AuthHandler that can also provide keys for JWT verification.
// Note: This function MUST do checks on the token signing method for security purposes.
func WithTokenVerificationKeyProvider(handler AuthHandler, keyFunc func(token *jwt.Token) (interface{}, error)) AuthHandler {
	return keyFuncAuthHandler{AuthHandler: handler, keyFunc: keyFunc}
}

type keyFuncAuthHandler struct {
	AuthHandler
	keyFunc func(token *jwt.Token) (interface{}, error)
}

func (h keyFuncAuthHandler) TokenVerificationKey(token *jwt.Token) (interface{}, error) {
	return h.keyFunc(token)
}

// WithPublicKeyProvider returns an AuthHandler that provides a public key for JWT verification.
func WithPublicKeyProvider(handler AuthHandler, pubKey *rsa.PublicKey) AuthHandler {
	return WithTokenVerificationKeyProvider(handler, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}

		return pubKey, nil
	})
}

// MakeSimpleAuthHandler returns a simple auth handler that handles multiple entities
// sharing one payload. This is useful for setting up local/internal authentication with a
// shared key. This is NOT secure for usage over networks exposed to the public internet.
// For that, use a more sophisticated handler with at least one key per entity.
func MakeSimpleAuthHandler(forEntities []string, expectedPayload string) AuthHandler {
	return MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
		var entityChecked bool
		for _, checkEntity := range forEntities {
			if subtle.ConstantTimeCompare([]byte(entity), []byte(checkEntity)) == 1 {
				entityChecked = true
				break
			}
		}
		if entityChecked && subtle.ConstantTimeCompare([]byte(payload), []byte(expectedPayload)) == 1 {
			return nil
		}
		return errInvalidCredentials
	}, func(ctx context.Context, entity string) error {
		for _, checkEntity := range forEntities {
			if subtle.ConstantTimeCompare([]byte(entity), []byte(checkEntity)) == 1 {
				return nil
			}
		}
		return errSessionEntityHandlerMismatch
	})
}

// CredentialsType signifies a means of representing a credential. For example,
// an API key.
type CredentialsType string

const (
	credentialsTypeInternal = CredentialsType("__internal")
	// CredentialsTypeAPIKey is intended for by external users, human and computer.
	CredentialsTypeAPIKey = CredentialsType("api-key")
)

// Credentials packages up both a type of credential along with its payload which
// is formatted specific to the type.
type Credentials struct {
	Type    CredentialsType
	Payload string
}
