// Package dialer provides a caching gRPC dialer.
package dialer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/multierr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	"go.viam.com/utils/rpc"
)

// A Dialer is responsible for making connections to gRPC endpoints.
type Dialer interface {
	// DialDirect makes a connection to the given target over standard gRPC with the supplied options.
	DialDirect(ctx context.Context, target string, opts ...grpc.DialOption) (ClientConn, error)

	// DialFunc makes a connection to the given target for the given proto using the given dial function.
	DialFunc(proto string, target string, f func() (ClientConn, error)) (ClientConn, error)

	// Close ensures all connections made are cleanly closed.
	Close() error
}

// A ClientConn is a wrapper around the gRPC client connection interface but ensures
// there is a way to close the connection.
type ClientConn interface {
	grpc.ClientConnInterface
	Close() error
}

type ctxKey int

const (
	ctxKeyDialer = ctxKey(iota)
	ctxKeyResolver
)

// ContextWithDialer attaches a Dialer to the given context.
func ContextWithDialer(ctx context.Context, d Dialer) context.Context {
	return context.WithValue(ctx, ctxKeyDialer, d)
}

// ContextDialer returns a Dialer. It may be nil if the value was never set.
func ContextDialer(ctx context.Context) Dialer {
	dialer := ctx.Value(ctxKeyDialer)
	if dialer == nil {
		return nil
	}
	return dialer.(Dialer)
}

// ContextWithResolver attaches a Resolver to the given context.
func ContextWithResolver(ctx context.Context, r *net.Resolver) context.Context {
	return context.WithValue(ctx, ctxKeyResolver, r)
}

// ContextResolver returns a Resolver. It may be nil if the value was never set.
func ContextResolver(ctx context.Context) *net.Resolver {
	resolver := ctx.Value(ctxKeyResolver)
	if resolver == nil {
		return nil
	}
	return resolver.(*net.Resolver)
}

type cachedDialer struct {
	mu    sync.Mutex // Note(erd): not suitable for highly concurrent usage
	conns map[string]*RefCountedConnWrapper
}

// NewCachedDialer returns a Dialer that returns the same connection if it
// already has been established at a particular target (regardless of the
// options used).
func NewCachedDialer() Dialer {
	return &cachedDialer{conns: map[string]*RefCountedConnWrapper{}}
}

func (cd *cachedDialer) DialDirect(ctx context.Context, target string, opts ...grpc.DialOption) (ClientConn, error) {
	return cd.DialFunc("grpc", target, func() (ClientConn, error) {
		return grpc.DialContext(ctx, target, opts...)
	})
}

func (cd *cachedDialer) DialFunc(proto string, target string, f func() (ClientConn, error)) (ClientConn, error) {
	key := fmt.Sprintf("%s:%s", proto, target)
	cd.mu.Lock()
	c, ok := cd.conns[key]
	cd.mu.Unlock()
	if ok {
		return c.Ref(), nil
	}

	// assume any difference in opts does not matter
	conn, err := f()
	if err != nil {
		return nil, err
	}
	refConn := NewRefCountedConnWrapper(conn, func() {
		cd.mu.Lock()
		delete(cd.conns, key)
		cd.mu.Unlock()
	})
	cd.mu.Lock()
	defer cd.mu.Unlock()

	// someone else might have already connected
	c, ok = cd.conns[key]
	if ok {
		if err := conn.Close(); err != nil {
			return nil, err
		}
		return c.Ref(), nil
	}
	cd.conns[key] = refConn
	return refConn.Ref(), nil
}

func (cd *cachedDialer) Close() error {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	var err error
	for _, c := range cd.conns {
		if closeErr := c.actual.Close(); closeErr != nil && status.Convert(closeErr).Code() != codes.Canceled {
			err = multierr.Combine(err, closeErr)
		}
	}
	return err
}

// NewRefCountedConnWrapper wraps the given connection to be able to be reference counted.
func NewRefCountedConnWrapper(conn ClientConn, onUnref func()) *RefCountedConnWrapper {
	return &RefCountedConnWrapper{utils.NewRefCountedValue(conn), conn, onUnref}
}

// RefCountedConnWrapper wraps a ClientConn to be reference counted.
type RefCountedConnWrapper struct {
	ref     utils.RefCountedValue
	actual  ClientConn
	onUnref func()
}

// Ref returns a new reference to the underlying ClientConn.
func (w *RefCountedConnWrapper) Ref() ClientConn {
	return &ReffedConn{ClientConn: w.ref.Ref().(ClientConn), deref: w.ref.Deref, onUnref: w.onUnref}
}

// A ReffedConn reference counts a ClieentConn and closes it on the last dereference.
type ReffedConn struct {
	ClientConn
	derefOnce sync.Once
	deref     func() bool
	onUnref   func()
}

// Close will deref the reference and if it is the last to do so, will close
// the underlying connection.
func (rc *ReffedConn) Close() error {
	var err error
	rc.derefOnce.Do(func() {
		if unref := rc.deref(); unref {
			if rc.onUnref != nil {
				defer rc.onUnref()
			}
			if closeErr := rc.ClientConn.Close(); closeErr != nil && status.Convert(closeErr).Code() != codes.Canceled {
				err = closeErr
			}
		}
	})
	return err
}

// DialDirectGRPC dials a gRPC server directly.
func DialDirectGRPC(ctx context.Context, address string, insecure bool) (ClientConn, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1 << 24)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: rpc.KeepAliveTime + 5*time.Second, // add a little buffer so as to not annoy the server ping strike system
		}),
	}
	if insecure {
		dialOpts = append(dialOpts, grpc.WithInsecure())
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	}
	if ctxDialer := ContextDialer(ctx); ctxDialer != nil {
		return ctxDialer.DialDirect(ctx, address, dialOpts...)
	}
	return grpc.DialContext(ctx, address, dialOpts...)
}

// DialFunc dials an address for a particular protocol and dial function.
func DialFunc(ctx context.Context, proto string, target string, f func() (ClientConn, error)) (ClientConn, error) {
	if ctxDialer := ContextDialer(ctx); ctxDialer != nil {
		return ctxDialer.DialFunc(proto, target, f)
	}
	return f()
}
