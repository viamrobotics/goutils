package utils

import (
	"context"
	"io"
)

// ContextCloser is a Closer with a context argument.
type ContextCloser interface {
	Close(ctx context.Context) error
}

// ContextCloserFunc implements a ContextCloser with a function.
type ContextCloserFunc func(ctx context.Context) error

// Close calls the closer function.
func (c ContextCloserFunc) Close(ctx context.Context) error {
	return c(ctx)
}

// TryClose attempts to close the target if it implements
// the right interface.
func TryClose(ctx context.Context, target interface{}) error {
	switch t := target.(type) {
	case io.Closer:
		return t.Close()
	case ContextCloser:
		return t.Close(ctx)
	case interface{ Close() }:
		t.Close()
		return nil
	default:
		return nil
	}
}

// ReadBytes ensures that all bytes requested to be read
// are read into a slice unless an error occurs. If the reader
// never returns the amount of bytes requested, this will block
// until the given context is done.
func ReadBytes(ctx context.Context, r io.Reader, toRead int) ([]byte, error) {
	buf := make([]byte, toRead)
	pos := 0

	for pos < toRead {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		n, err := r.Read(buf[pos:])
		if err != nil {
			return nil, err
		}
		pos += n
	}
	return buf, nil
}
