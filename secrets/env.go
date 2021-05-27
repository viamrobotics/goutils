package secrets

import (
	"context"
	"os"
)

type EnvSecretSource struct {
}

func (src *EnvSecretSource) Get(ctx context.Context, name string) (string, error) {
	secret, ok := os.LookupEnv(name)
	if !ok {
		return "", ErrSecretNotFound
	}
	return secret, nil
}

func (src *EnvSecretSource) Type() SecretSourceType {
	return SecretSourceTypeEnv
}
