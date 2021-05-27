package secrets

import (
	"context"
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("secret not found")

type Source interface {
	Get(ctx context.Context, name string) (string, error)
	Type() SourceType
}

type SourceType string

const (
	SourceTypeEnv = "env"
	SourceTypeGCP = "gcp"
)

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

// helper for initalizers where you just want to fail and don't want to have to do error checking
func GetOrPanic(ctx context.Context, source Source, name string) string {
	v, err := source.Get(ctx, name)
	if err != nil {
		panic(err)
	}
	return v
}
