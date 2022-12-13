package rpc

import (
	"context"
	"crypto/rsa"
	//nolint:gosec // using for fingerprint
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"errors"
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
	// Optional authentication metadata can be returned to be used in future requests
	// via ContextAuthMetadata.
	Authenticate(ctx context.Context, entity, payload string) (map[string]string, error)

	// VerifyEntity verifies that this handler is allowed to authenticate the given entity.
	// The handler can optionally return opaque info about the entity that will be bound to the
	// context accessible via ContextAuthEntity.
	VerifyEntity(ctx context.Context, entity string) (interface{}, error)
}

// An AuthenticateToHandler determines if the given entity should be allowed to be
// authenticated to by the calling entity, accessible via MustContextAuthEntity.
// The returned auth metadata will be present in ContextAuthMetadata.
type AuthenticateToHandler func(ctx context.Context, entity string) (map[string]string, error)

// TokenVerificationKeyProvider allows an AuthHandler to supply a key needed to peform
// verification of a JWT. This is helpful when the server itself is not responsible
// for authentication. For example, this could be for a central auth server
// with untrusted peers using a public key to verify JWTs.
type TokenVerificationKeyProvider interface {
	// TokenVerificationKey returns the key needed to do JWT verification.
	TokenVerificationKey(token *jwt.Token) (interface{}, error)
}

// Claims is an interface that all custom claims must implement to be supported
// by the rpc system.
type Claims interface {
	// Ensure we meet the jwt.Claims interface, return error if claims are invalid. Claims
	// are validated before entity checks,
	jwt.Claims

	// Subject returns the subject associated with the claims.
	Subject() string

	// Entity must return the "entity" making the request to the rpc system from the jwt claims
	// presented. Returns an error if entity is missing. Should not preform any entity checks.
	Entity() (string, error)

	// CredentialsType returns the rpc CredentialsType based on the jwt claims.
	CredentialsType() CredentialsType

	// Metadata returns the rpc auth metadata based on the jwt claims.
	Metadata() map[string]string
}

// TokenCustomClaimProvider allows an AuthHandler to supply a key needed to peform
// verification of a JWT. This is helpful when the server itself is not responsible
// for authentication. For example, this could be for a central auth server
// with untrusted peers using a public key to verify JWTs.
type TokenCustomClaimProvider interface {
	// CreateClaims returns the claim interface
	CreateClaims() Claims
}

var (
	errInvalidCredentials = status.Error(codes.Unauthenticated, "invalid credentials")
	errCannotAuthEntity   = status.Error(codes.Unauthenticated, "cannot authenticate entity")
)

// MakeFuncAuthHandler encapsulates AuthHandler functionality to a set of functions.
func MakeFuncAuthHandler(
	auth func(ctx context.Context, entity, payload string) (map[string]string, error),
	verify func(ctx context.Context, entity string) (interface{}, error),
) AuthHandler {
	return funcAuthHandler{auth: auth, verify: verify}
}

type funcAuthHandler struct {
	auth   func(ctx context.Context, entity, payload string) (map[string]string, error)
	verify func(ctx context.Context, entity string) (interface{}, error)
}

// Authenticate checks if the given entity and payload are what it expects. It returns
// an error otherwise.
func (h funcAuthHandler) Authenticate(ctx context.Context, entity, payload string) (map[string]string, error) {
	return h.auth(ctx, entity, payload)
}

// VerifyEntity checks if the given entity is handled by this handler.
func (h funcAuthHandler) VerifyEntity(ctx context.Context, entity string) (interface{}, error) {
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

// WithTokenCustomClaimProvider returns an AuthHandler that returns a custom claim type.
func WithTokenCustomClaimProvider(handler AuthHandler, claimFunc func() Claims) AuthHandler {
	return customClaimAuthHandler{AuthHandler: handler, claimFunc: claimFunc}
}

type customClaimAuthHandler struct {
	AuthHandler
	claimFunc func() Claims
}

func (h customClaimAuthHandler) CreateClaims() Claims {
	return h.claimFunc()
}

// MakeSimpleVerifyEntity returns a VerifyEntity function to be used in an AuthHandler that
// only verifies a list of entities for a single match and the returned auth entity is the
// entity name itself.
func MakeSimpleVerifyEntity(forEntities []string) func(ctx context.Context, entity string) (interface{}, error) {
	entityChecker := MakeEntitiesChecker(forEntities)
	return func(ctx context.Context, entity string) (interface{}, error) {
		return entity, entityChecker(ctx, entity)
	}
}

// WithPublicKeyProvider returns an AuthHandler that provides a public key for JWT verification
// that only can verify entities.
func WithPublicKeyProvider(
	verifyEntity func(ctx context.Context, entity string) (interface{}, error),
	pubKey *rsa.PublicKey,
) AuthHandler {
	handler := MakeFuncAuthHandler(
		func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return nil, status.Error(codes.InvalidArgument, "go auth externally")
		},
		verifyEntity,
	)
	return WithTokenVerificationKeyProvider(
		handler,
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}

			return pubKey, nil
		},
	)
}

// MakeSimpleAuthHandler returns a simple auth handler that handles multiple entities
// sharing one payload. This is useful for setting up local/internal authentication with a
// shared key. This is NOT secure for usage over networks exposed to the public internet.
// For that, use a more sophisticated handler with at least one key per entity.
func MakeSimpleAuthHandler(forEntities []string, expectedPayload string) AuthHandler {
	return MakeSimpleMultiAuthHandler(forEntities, []string{expectedPayload})
}

// MakeSimpleMultiAuthHandler returns a simple auth handler that handles multiple entities
// sharing multiple possible payloads. This is useful for rolling keys.
func MakeSimpleMultiAuthHandler(forEntities, expectedPayloads []string) AuthHandler {
	if len(expectedPayloads) == 0 {
		panic("expected at least one payload")
	}
	entityChecker := MakeEntitiesChecker(forEntities)
	return MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
		if err := entityChecker(ctx, entity); err != nil {
			if errors.Is(err, errCannotAuthEntity) {
				return nil, errInvalidCredentials
			}
			return nil, err
		}

		payloadB := []byte(payload)
		for _, expectedPayload := range expectedPayloads {
			if subtle.ConstantTimeCompare(payloadB, []byte(expectedPayload)) == 1 {
				return map[string]string{}, nil
			}
		}
		return nil, errInvalidCredentials
	}, func(ctx context.Context, entity string) (interface{}, error) {
		return entity, entityChecker(ctx, entity)
	})
}

// MakeEntitiesChecker checks a list of entities against a given one for use in VerifyEntity.
func MakeEntitiesChecker(forEntities []string) func(ctx context.Context, entities ...string) error {
	return func(ctx context.Context, entities ...string) error {
		for _, recvEntity := range entities {
			for _, checkEntity := range forEntities {
				if subtle.ConstantTimeCompare([]byte(recvEntity), []byte(checkEntity)) == 1 {
					return nil
				}
			}
		}
		return errCannotAuthEntity
	}
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
	Type    CredentialsType `json:"type"`
	Payload string          `json:"payload"`
}

// RSAPublicKeyThumbprint returns SHA1 of the public key's modulus Base64 URL encoded without padding.
func RSAPublicKeyThumbprint(key *rsa.PublicKey) (string, error) {
	//nolint:gosec // using for fingerprint
	thumbPrint := sha1.New()
	_, err := thumbPrint.Write(key.N.Bytes())
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(thumbPrint.Sum(nil)), nil
}
