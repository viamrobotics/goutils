package rpc

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/edaniels/golog"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
)

// create a new TLS config with the default options for RPC.
func newDefaultTLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// A Dialer is responsible for making connections to gRPC endpoints.
type Dialer interface {
	// DialDirect makes a connection to the given target over standard gRPC with the supplied options.
	DialDirect(
		ctx context.Context,
		target string,
		keyExtra string,
		onClose func() error,
		opts ...grpc.DialOption,
	) (conn ClientConn, cached bool, err error)

	// DialFunc makes a connection to the given target for the given proto using the given dial function.
	DialFunc(
		proto string,
		target string,
		keyExtra string,
		dialNew func() (ClientConn, func() error, error),
	) (conn ClientConn, cached bool, err error)

	// Close ensures all connections made are cleanly closed.
	Close() error
}

// A ClientConn is a wrapper around the gRPC client connection interface but ensures
// there is a way to close the connection.
type ClientConn interface {
	grpc.ClientConnInterface
	Close() error
}

// A ClientConnAuthenticator supports instructing a connection to authenticate now.
type ClientConnAuthenticator interface {
	ClientConn
	Authenticate(ctx context.Context) (string, error)
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
	keyExtra string,
	onClose func() error,
	opts ...grpc.DialOption,
) (ClientConn, bool, error) {
	return cd.DialFunc("grpc", target, keyExtra, func() (ClientConn, func() error, error) {
		conn, err := grpc.DialContext(ctx, target, opts...)
		if err != nil {
			return nil, nil, err
		}
		return conn, onClose, nil
	})
}

func (cd *cachedDialer) DialFunc(
	proto string,
	target string,
	keyExtra string,
	dialNew func() (ClientConn, func() error, error),
) (ClientConn, bool, error) {
	key := fmt.Sprintf("%s:%s:%s", proto, target, keyExtra)
	cd.mu.Lock()
	c, ok := cd.conns[key]
	cd.mu.Unlock()
	if ok {
		return c.Ref(), true, nil
	}

	// assume any difference in opts does not matter
	conn, onClose, err := dialNew()
	if err != nil {
		return nil, false, err
	}
	conn = wrapClientConnWithCloseFunc(conn, onClose)
	refConn := newRefCountedConnWrapper(proto, conn, func() {
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
	// need a copy of cd.conns as we can't hold the lock, since .Close() fires the onUnref() set (above) in DialFunc()
	// that uses the same lock and directly modifies cd.conns when the dialer is reused at different layers (e.g. auth and multi)
	var conns []*refCountedConnWrapper
	for _, c := range cd.conns {
		conns = append(conns, c)
	}
	cd.mu.Unlock()
	var err error
	for _, c := range conns {
		if closeErr := c.actual.Close(); closeErr != nil && status.Convert(closeErr).Code() != codes.Canceled {
			err = multierr.Combine(err, closeErr)
		}
	}
	return err
}

// newRefCountedConnWrapper wraps the given connection to be able to be reference counted.
func newRefCountedConnWrapper(proto string, conn ClientConn, onUnref func()) *refCountedConnWrapper {
	return &refCountedConnWrapper{proto, utils.NewRefCountedValue(conn), conn, onUnref}
}

// refCountedConnWrapper wraps a ClientConn to be reference counted.
type refCountedConnWrapper struct {
	proto   string
	ref     utils.RefCountedValue
	actual  ClientConn
	onUnref func()
}

// Ref returns a new reference to the underlying ClientConn.
func (w *refCountedConnWrapper) Ref() ClientConn {
	return &reffedConn{ClientConn: w.ref.Ref().(ClientConn), proto: w.proto, deref: w.ref.Deref, onUnref: w.onUnref}
}

// A reffedConn reference counts a ClieentConn and closes it on the last dereference.
type reffedConn struct {
	ClientConn
	proto     string
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
			if utils.Debug {
				golog.Global().Debugw("close referenced conn", "proto", rc.proto)
			}
			if closeErr := rc.ClientConn.Close(); closeErr != nil && status.Convert(closeErr).Code() != codes.Canceled {
				err = closeErr
			}
		}
	})
	return err
}

// ErrInsecureWithCredentials is sent when a dial attempt is made to an address where either the insecure
// option or insecure downgrade with credentials options are not set.
var ErrInsecureWithCredentials = errors.New("requested address is insecure and will not send credentials")

// DialDirectGRPC dials a gRPC server directly.
func DialDirectGRPC(ctx context.Context, address string, logger golog.Logger, opts ...DialOption) (ClientConn, error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt.apply(&dOpts)
	}
	dOpts.webrtcOpts.Disable = true
	dOpts.mdnsOptions.Disable = true

	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	return dialInner(ctx, address, logger, &dOpts)
}

// dialDirectGRPC dials a gRPC server directly.
func dialDirectGRPC(ctx context.Context, address string, dOpts *dialOptions, logger golog.Logger) (ClientConn, bool, error) {
	dialOpts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time: keepAliveTime + 5*time.Second, // add a little buffer so as to not annoy the server ping strike system
		}),
	}
	if dOpts.insecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		tlsConfig := dOpts.tlsConfig
		if tlsConfig == nil {
			tlsConfig = newDefaultTLSConfig()
		}

		var downgrade bool
		if dOpts.allowInsecureDowngrade || dOpts.allowInsecureWithCredsDowngrade {
			var dialer tls.Dialer
			dialer.Config = tlsConfig
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				// will use TLS
				utils.UncheckedError(conn.Close())
			} else if strings.Contains(err.Error(), "tls: first record does not look like a TLS handshake") {
				// unfortunately there's no explicit error value for this, so we do a string check
				hasLocalCreds := dOpts.creds.Type != "" && dOpts.externalAuthAddr == ""
				if dOpts.creds.Type == "" || !hasLocalCreds || dOpts.allowInsecureWithCredsDowngrade {
					logger.Warnw("downgrading from TLS to plaintext", "address", address, "with_credentials", hasLocalCreds)
					downgrade = true
				} else if hasLocalCreds {
					return nil, false, ErrInsecureWithCredentials
				}
			}
		}
		if downgrade {
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		}
	}

	if dOpts.statsHandler != nil {
		dialOpts = append(dialOpts, grpc.WithStatsHandler(dOpts.statsHandler))
	}

	grpcLogger := logger.Desugar()
	if !(dOpts.debug || utils.Debug) {
		grpcLogger = grpcLogger.WithOptions(zap.IncreaseLevel(zap.LevelEnablerFunc(zapcore.ErrorLevel.Enabled)))
	}
	var unaryInterceptors []grpc.UnaryClientInterceptor
	unaryInterceptors = append(unaryInterceptors, grpc_zap.UnaryClientInterceptor(grpcLogger))
	if dOpts.unaryInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, dOpts.unaryInterceptor)
	}
	unaryInterceptor := grpc_middleware.ChainUnaryClient(unaryInterceptors...)
	dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(unaryInterceptor))

	var streamInterceptors []grpc.StreamClientInterceptor
	streamInterceptors = append(streamInterceptors, grpc_zap.StreamClientInterceptor(grpcLogger))
	if dOpts.streamInterceptor != nil {
		streamInterceptors = append(streamInterceptors, dOpts.streamInterceptor)
	}
	streamInterceptor := grpc_middleware.ChainStreamClient(streamInterceptors...)
	dialOpts = append(dialOpts, grpc.WithStreamInterceptor(streamInterceptor))

	var connPtr *ClientConn
	var closeCredsFunc func() error
	var rpcCreds *perRPCJWTCredentials

	if dOpts.authMaterial != "" {
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(&staticPerRPCJWTCredentials{dOpts.authMaterial}))
	} else if dOpts.creds.Type != "" || dOpts.externalAuthMaterial != "" {
		rpcCreds = &perRPCJWTCredentials{
			entity: dOpts.authEntity,
			creds:  dOpts.creds,
			debug:  dOpts.debug,
			logger: logger,
			// Note: don't set dialOptsCopy.authMaterial below as perRPCJWTCredentials will know to use
			// its externalAccessToken to authenticateTo. This will result in both a connection level authorization
			// added as well as an authorization header added from perRPCJWTCredentials, resulting in a failure.
			externalAuthMaterial: dOpts.externalAuthMaterial,
		}
		if dOpts.debug {
			logger.Debugw("will eventually authenticate as entity", "entity", dOpts.authEntity)
		}
		if dOpts.externalAuthAddr != "" {
			if dOpts.debug && dOpts.externalAuthToEntity != "" {
				logger.Debugw("will eventually externally authenticate to entity", "entity", dOpts.externalAuthToEntity)
			}
			if dOpts.debug {
				logger.Debugw("dialing direct for external auth", "address", dOpts.externalAuthAddr)
			}
			dialOptsCopy := *dOpts
			dialOptsCopy.insecure = dOpts.externalAuthInsecure
			dialOptsCopy.externalAuthAddr = ""
			dialOptsCopy.externalAuthMaterial = ""
			dialOptsCopy.creds = Credentials{}
			dialOptsCopy.authEntity = ""

			// reset the tls config that is used for the external Auth Service.
			dialOptsCopy.tlsConfig = newDefaultTLSConfig()

			externalConn, externalCached, err := dialDirectGRPC(ctx, dOpts.externalAuthAddr, &dialOptsCopy, logger)
			if err != nil {
				return nil, false, err
			}

			if dOpts.debug {
				if externalCached {
					logger.Debugw("connected directly for external auth (cached)", "address", dOpts.externalAuthAddr)
				} else {
					logger.Debugw("connected directly for external auth", "address", dOpts.externalAuthAddr)
				}
			}
			closeCredsFunc = externalConn.Close
			rpcCreds.conn = externalConn
			rpcCreds.externalAuthToEntity = dOpts.externalAuthToEntity
		} else {
			connPtr = &rpcCreds.conn
		}
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(rpcCreds))
	}

	var conn ClientConn
	var cached bool
	var err error
	if ctxDialer := contextDialer(ctx); ctxDialer != nil {
		conn, cached, err = ctxDialer.DialDirect(ctx, address, buildKeyExtra(dOpts), closeCredsFunc, dialOpts...)
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
	if rpcCreds != nil {
		conn = clientConnRPCAuthenticator{conn, rpcCreds}
	}
	return conn, cached, err
}

// buildKeyExtra hashes options to only cache when authentication material
// is the same between dials. That means any time a new way that differs
// authentication based on options is introduced, this function should
// also be updated.
func buildKeyExtra(opts *dialOptions) string {
	hasher := fnv.New128a()
	if opts.authEntity != "" {
		hasher.Write([]byte(opts.authEntity))
	}
	if opts.creds.Type != "" {
		hasher.Write([]byte(opts.creds.Type))
	}
	if opts.creds.Payload != "" {
		hasher.Write([]byte(opts.creds.Payload))
	}
	if opts.externalAuthAddr != "" {
		hasher.Write([]byte(opts.externalAuthAddr))
	}
	if opts.externalAuthToEntity != "" {
		hasher.Write([]byte(opts.externalAuthToEntity))
	}
	if opts.externalAuthMaterial != "" {
		hasher.Write([]byte(opts.externalAuthMaterial))
	}
	if opts.webrtcOpts.SignalingServerAddress != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingServerAddress))
	}
	if opts.webrtcOpts.SignalingExternalAuthAddress != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingExternalAuthAddress))
	}
	if opts.webrtcOpts.SignalingExternalAuthToEntity != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingExternalAuthToEntity))
	}
	if opts.webrtcOpts.SignalingExternalAuthAuthMaterial != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingExternalAuthAuthMaterial))
	}
	if opts.webrtcOpts.SignalingCreds.Type != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingCreds.Type))
	}
	if opts.webrtcOpts.SignalingCreds.Payload != "" {
		hasher.Write([]byte(opts.webrtcOpts.SignalingCreds.Payload))
	}
	return hex.EncodeToString(hasher.Sum(nil))
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
func dialFunc(
	ctx context.Context,
	proto string,
	target string,
	keyExtra string,
	f func() (ClientConn, error),
) (ClientConn, bool, error) {
	if ctxDialer := contextDialer(ctx); ctxDialer != nil {
		return ctxDialer.DialFunc(proto, target, keyExtra, func() (ClientConn, func() error, error) {
			conn, err := f()
			return conn, nil, err
		})
	}
	conn, err := f()
	return conn, false, err
}

type clientConnRPCAuthenticator struct {
	ClientConn
	rpcCreds *perRPCJWTCredentials
}

func (cc clientConnRPCAuthenticator) Authenticate(ctx context.Context) (string, error) {
	return cc.rpcCreds.authenticate(ctx)
}

type staticPerRPCJWTCredentials struct {
	authMaterial string
}

func (creds *staticPerRPCJWTCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	for _, uriVal := range uri {
		if strings.HasSuffix(uriVal, "/proto.rpc.v1.AuthService") {
			//nolint:nilnil
			return nil, nil
		}
	}

	return map[string]string{"Authorization": "Bearer " + creds.authMaterial}, nil
}

func (creds *staticPerRPCJWTCredentials) RequireTransportSecurity() bool {
	return false
}

type perRPCJWTCredentials struct {
	mu                   sync.RWMutex
	conn                 ClientConn
	entity               string
	externalAuthToEntity string
	creds                Credentials
	accessToken          string
	// The static external auth material used against the AuthenticateTo request to obtain final accessToken
	externalAuthMaterial string

	debug  bool
	logger golog.Logger
}

// TODO(GOUT-10): handle expiration.
func (creds *perRPCJWTCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	for _, uriVal := range uri {
		if strings.HasSuffix(uriVal, "/proto.rpc.v1.AuthService") {
			//nolint:nilnil
			return nil, nil
		}
	}
	accessToken, err := creds.authenticate(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]string{"Authorization": "Bearer " + accessToken}, nil
}

func (creds *perRPCJWTCredentials) authenticate(ctx context.Context) (string, error) {
	creds.mu.RLock()
	accessToken := creds.accessToken
	creds.mu.RUnlock()
	if accessToken == "" {
		creds.mu.Lock()
		defer creds.mu.Unlock()
		accessToken = creds.accessToken
		if accessToken == "" {
			// skip authenticate call when a static access token for the external auth is used.
			if creds.externalAuthMaterial == "" {
				if creds.debug {
					creds.logger.Debugw("authenticating as entity", "entity", creds.entity)
				}
				authClient := rpcpb.NewAuthServiceClient(creds.conn)

				// Check external auth creds...
				resp, err := authClient.Authenticate(ctx, &rpcpb.AuthenticateRequest{
					Entity: creds.entity,
					Credentials: &rpcpb.Credentials{
						Type:    string(creds.creds.Type),
						Payload: creds.creds.Payload,
					},
				})
				if err != nil {
					return "", err
				}
				accessToken = resp.AccessToken
			} else {
				accessToken = creds.externalAuthMaterial
			}

			// now perform external auth
			if creds.externalAuthToEntity == "" {
				if creds.debug {
					creds.logger.Debug("not external auth for an entity; done")
				}
				creds.accessToken = accessToken
			} else {
				if creds.debug {
					creds.logger.Debugw("authenticating to external entity", "entity", creds.externalAuthToEntity)
				}
				// now perform external auth
				md := make(metadata.MD)
				bearer := fmt.Sprintf("Bearer %s", accessToken)
				md.Set("authorization", bearer)
				externalCtx := metadata.NewOutgoingContext(ctx, md)

				externalAuthClient := rpcpb.NewExternalAuthServiceClient(creds.conn)
				externalResp, err := externalAuthClient.AuthenticateTo(externalCtx, &rpcpb.AuthenticateToRequest{
					Entity: creds.externalAuthToEntity,
				})
				if err != nil {
					return "", err
				}

				if creds.debug {
					creds.logger.Debugw("external auth done", "auth_to", creds.externalAuthToEntity)
				}

				accessToken = externalResp.AccessToken
				creds.accessToken = externalResp.AccessToken
			}
		}
	}

	return accessToken, nil
}

func (creds *perRPCJWTCredentials) RequireTransportSecurity() bool {
	return false
}
