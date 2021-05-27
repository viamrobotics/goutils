package secrets

import (
	"context"
	"os"
)

type EnvSource struct {
}

func (src *EnvSource) Get(ctx context.Context, name string) (string, error) {
	secret, ok := os.LookupEnv(name)
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

func (src *EnvSource) Type() SourceType {
	return SourceTypeEnv
}
