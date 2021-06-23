package secrets

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotFound is used when a secret cannot be found by its name.
var ErrNotFound = errors.New("secret not found")

// A Source provides the means to look up secrets.
type Source interface {
	Get(ctx context.Context, name string) (string, error)
	Type() SourceType
	Close() error
}

// SourceType identifies the type of secret source.
type SourceType string

// The set of source types.
const (
	SourceTypeEnv = "env"
	SourceTypeGCP = "gcp"
)

// NewSource returns a new source of the given type. Right now configuration details
// are derived via the given context but in the future there should probably be a config
// passed in.
func NewSource(ctx context.Context, sourceType SourceType) (Source, error) {
	switch sourceType {
	case SourceTypeGCP:
		return NewGCPSource(ctx)
	case "", SourceTypeEnv:
		return &EnvSource{}, nil
	default:
		return nil, fmt.Errorf("unknown secret source type %q", sourceType)
	}
}

// MustGet gets the secret of the given name and panics if any error occurs.
func MustGet(ctx context.Context, source Source, name string) string {
	v, err := source.Get(ctx, name)
	if err != nil {
		panic(err)
	}
	return v
}
