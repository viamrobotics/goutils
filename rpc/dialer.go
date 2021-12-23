package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/edaniels/golog"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
)

// A Dialer is responsible for making connections to gRPC endpoints.
type Dialer interface {
	// DialDirect makes a connection to the given target over standard gRPC with the supplied options.
	DialDirect(ctx context.Context, target string, onClose func() error, opts ...grpc.DialOption) (conn ClientConn, cached bool, err error)

	// DialFunc makes a connection to the given target for the given proto using the given dial function.
	DialFunc(proto string, target string, f func() (ClientConn, func() error, error)) (conn ClientConn, cached bool, err error)

	// Close ensures all connections made are cleanly closed.
	Close() error
}

// A ClientConn is a wrapper around the gRPC client connection interface but ensures
// there is a way to close the connection.
type ClientConn interface {
	grpc.ClientConnInterface
	Close() error
}

type cachedDialer struct {
	mu    sync.Mutex // Note(erd): not suitable for highly concurrent usage
	conns map[string]*refCountedConnWrapper
}

// NewCachedDialer returns a Dialer that returns the same connection if it
// already has been established at a particular target (regardless of the
// options used).
func NewCachedDialer() Dialer {
	return &cachedDialer{conns: map[string]*refCountedConnWrapper{}}
}

func (cd *cachedDialer) DialDirect(
	ctx context.Context,
	target string,
	onClose func() error,
	opts ...grpc.DialOption,
) (ClientConn, bool, error) {
	return cd.DialFunc("grpc", target, func() (ClientConn, func() error, error) {
		conn, err := grpc.DialContext(ctx, target, opts...)
		if err != nil {
			return nil, nil, err
		}
		return conn, onClose, nil
	})
}

func (cd *cachedDialer) DialFunc(proto string, target string, f func() (ClientConn, func() error, error)) (ClientConn, bool, error) {
	key := fmt.Sprintf("%s:%s", proto, target)
	cd.mu.Lock()
	c, ok := cd.conns[key]
	cd.mu.Unlock()
	if ok {
		return c.Ref(), true, nil
	}

	// assume any difference in opts does not matter
	conn, onClose, err := f()
	if err != nil {
		return nil, false, err
	}
	conn = wrapClientConnWithCloseFunc(conn, onClose)
	refConn := newRefCountedConnWrapper(conn, func() {
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
			return nil, false, err
		}
		return c.Ref(), true, nil
	}
	cd.conns[key] = refConn
	return refConn.Ref(), false, nil
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

// newRefCountedConnWrapper wraps the given connection to be able to be reference counted.
func newRefCountedConnWrapper(conn ClientConn, onUnref func()) *refCountedConnWrapper {
	return &refCountedConnWrapper{utils.NewRefCountedValue(conn), conn, onUnref}
}

// refCountedConnWrapper wraps a ClientConn to be reference counted.
type refCountedConnWrapper struct {
	ref     utils.RefCountedValue
	actual  ClientConn
	onUnref func()
}

// Ref returns a new reference to the underlying ClientConn.
func (w *refCountedConnWrapper) Ref() ClientConn {
	return &reffedConn{ClientConn: w.ref.Ref().(ClientConn), deref: w.ref.Deref, onUnref: w.onUnref}
}

// A reffedConn reference counts a ClieentConn and closes it on the last dereference.
type reffedConn struct {
	ClientConn
	derefOnce sync.Once
	deref     func() bool
	onUnref   func()
}

// Close will deref the reference and if it is the last to do so, will close
// the underlying connection.
func (rc *reffedConn) Close() error {
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

// dialDirectGRPC dials a gRPC server directly.
func dialDirectGRPC(ctx context.Context, address string, dOpts *dialOptions, logger golog.Logger) (ClientConn, bool, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: keepAliveTime + 5*time.Second, // add a little buffer so as to not annoy the server ping strike system
		}),
	}
	if dOpts.insecure {
		dialOpts = append(dialOpts, grpc.WithInsecure())
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})))
	}

	grpcLogger := logger.Desugar()
	if !(dOpts.debug || utils.Debug) {
		grpcLogger = grpcLogger.WithOptions(zap.IncreaseLevel(zap.LevelEnablerFunc(zapcore.ErrorLevel.Enabled)))
	}
	var unaryInterceptors []grpc.UnaryClientInterceptor
	unaryInterceptors = append(unaryInterceptors, grpc_zap.UnaryClientInterceptor(grpcLogger))
	unaryInterceptor := grpc_middleware.ChainUnaryClient(unaryInterceptors...)
	dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(unaryInterceptor))

	var streamInterceptors []grpc.StreamClientInterceptor
	streamInterceptors = append(streamInterceptors, grpc_zap.StreamClientInterceptor(grpcLogger))
	streamInterceptor := grpc_middleware.ChainStreamClient(streamInterceptors...)
	dialOpts = append(dialOpts, grpc.WithStreamInterceptor(streamInterceptor))

	var connPtr *ClientConn
	var closeCredsFunc func() error
	if dOpts.creds.Type != "" {
		rpcCreds := &perRPCJWTCredentials{
			entity: dOpts.authEntity,
			creds:  dOpts.creds,
		}
		if dOpts.externalAuthAddr != "" {
			dialOptsCopy := *dOpts
			dialOptsCopy.externalAuthAddr = ""
			dialOptsCopy.creds = Credentials{}
			externalConn, _, err := dialDirectGRPC(ctx, dOpts.externalAuthAddr, &dialOptsCopy, logger)
			if err != nil {
				return nil, false, err
			}
			closeCredsFunc = externalConn.Close
			rpcCreds.conn = externalConn
		} else {
			connPtr = &rpcCreds.conn
		}
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(rpcCreds))
	}
	var conn ClientConn
	var cached bool
	var err error
	if ctxDialer := contextDialer(ctx); ctxDialer != nil {
		conn, cached, err = ctxDialer.DialDirect(ctx, address, closeCredsFunc, dialOpts...)
	} else {
		conn, err = grpc.DialContext(ctx, address, dialOpts...)
		if err == nil && closeCredsFunc != nil {
			conn = wrapClientConnWithCloseFunc(conn, closeCredsFunc)
		}
	}
	if err != nil {
		if closeCredsFunc != nil {
			err = multierr.Combine(err, closeCredsFunc())
		}
		return nil, false, err
	}
	if connPtr != nil {
		*connPtr = conn
	}
	return conn, cached, err
}

func wrapClientConnWithCloseFunc(conn ClientConn, closeFunc func() error) ClientConn {
	return &clientConnWithCloseFunc{ClientConn: conn, closeFunc: closeFunc}
}

type clientConnWithCloseFunc struct {
	ClientConn
	closeFunc func() error
}

func (cc *clientConnWithCloseFunc) Close() (err error) {
	defer func() {
		if cc.closeFunc == nil {
			return
		}
		err = multierr.Combine(err, cc.closeFunc())
	}()
	return cc.ClientConn.Close()
}

// dialFunc dials an address for a particular protocol and dial function.
func dialFunc(ctx context.Context, proto string, target string, f func() (ClientConn, error)) (ClientConn, bool, error) {
	if ctxDialer := contextDialer(ctx); ctxDialer != nil {
		return ctxDialer.DialFunc(proto, target, func() (ClientConn, func() error, error) {
			conn, err := f()
			return conn, nil, err
		})
	}
	conn, err := f()
	return conn, false, err
}

type perRPCJWTCredentials struct {
	mu          sync.RWMutex
	conn        ClientConn
	entity      string
	creds       Credentials
	accessToken string
}

// TODO(https://github.com/viamrobotics/goutils/issues/13): handle expiration.
func (creds *perRPCJWTCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	for _, uriVal := range uri {
		if strings.HasSuffix(uriVal, "/proto.rpc.v1.AuthService") {
			//nolint:nilnil
			return nil, nil
		}
	}
	creds.mu.RLock()
	accessToken := creds.accessToken
	creds.mu.RUnlock()
	if accessToken == "" {
		creds.mu.Lock()
		defer creds.mu.Unlock()
		accessToken = creds.accessToken
		if accessToken == "" {
			authClient := rpcpb.NewAuthServiceClient(creds.conn)
			resp, err := authClient.Authenticate(ctx, &rpcpb.AuthenticateRequest{
				Entity: creds.entity,
				Credentials: &rpcpb.Credentials{
					Type:    string(creds.creds.Type),
					Payload: creds.creds.Payload,
				},
			})
			if err != nil {
				return nil, err
			}
			accessToken = resp.AccessToken
			creds.accessToken = accessToken
		}
	}

	return map[string]string{"Authorization": "Bearer " + accessToken}, nil
}

func (creds *perRPCJWTCredentials) RequireTransportSecurity() bool {
	return false
}
