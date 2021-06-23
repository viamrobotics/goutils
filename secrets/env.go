package secrets

import (
	"context"
	"os"
)

// EnvSource provides secrets via environment variables.
type EnvSource struct{}

// Get looks up the given name as an environment variable.
func (src *EnvSource) Get(ctx context.Context, name string) (string, error) {
	secret, ok := os.LookupEnv(name)
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

// Type returns the type of this source (environment).
func (src *EnvSource) Type() SourceType {
	return SourceTypeEnv
}

// Close does nothing.
func (src *EnvSource) Close() error {
	return nil
}
