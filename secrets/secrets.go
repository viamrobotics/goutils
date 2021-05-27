package secrets

import (
	"context"
	"errors"
	"fmt"
)

var ErrSecretNotFound = errors.New("secret not found")

type SecretSource interface {
	Get(ctx context.Context, name string) (string, error)
	Type() SecretSourceType
}

type SecretSourceType string

const (
	SecretSourceTypeEnv = "env"
	SecretSourceTypeGCP = "gcp"
)

func NewSecretSource(ctx context.Context, sourceType SecretSourceType) (SecretSource, error) {
	switch sourceType {
	case SecretSourceTypeGCP:
		return NewGCPSecrets(ctx)
	case "", SecretSourceTypeEnv:
		return &EnvSecretSource{}, nil
	default:
		return nil, fmt.Errorf("unknown secret source type %q", sourceType)
	}
}

// helper for initalizers where you just want to fail and don't want to have to do error checking
func GetSecretOrPanic(ctx context.Context, source SecretSource, name string) string {
	v, err := source.Get(ctx, name)
	if err != nil {
		panic(err)
	}
	return v
}
