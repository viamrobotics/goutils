package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	grpc_recovery "github.com/grpc-ecosystem/go-grpc-middleware/recovery"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

const generatedRSAKeyBits = 4096

// A Server provides a convenient way to get a gRPC server up and running
// with HTTP facilities.
type Server interface {
	// InternalAddr returns the address from the listener used for
	// gRPC communications. It may be the same listener the server
	// was constructed with.
	InternalAddr() net.Addr

	// Start only starts up the internal gRPC server.
	Start() error

	// Serve will externally serve, on the given listener, the
	// all in one handler described by http.Handler.
	Serve(listener net.Listener) (err error)

	// Stop stops the internal gRPC and the HTTP server if it
	// was started.
	Stop() error

	// RegisterServiceServer associates a service description with
	// its implementation along with any gateway handlers.
	RegisterServiceServer(
		ctx context.Context,
		svcDesc *grpc.ServiceDesc,
		svcServer interface{},
		svcHandlers ...RegisterServiceHandlerFromEndpointFunc,
	) error

	// GatewayHandler returns a handler for gateway based gRPC requests.
	// See: https://github.com/grpc-ecosystem/grpc-gateway
	GatewayHandler() http.Handler

	// GRPCHandler returns a handler for standard grpc/grpc-web requests which
	// expect to be served from a root path.
	GRPCHandler() http.Handler

	// http.Handler implemented here is an all-in-one handler for any kind of gRPC traffic.
	// This is useful in a scenario where all gRPC is served from the root path due to
	// limitations of normal gRPC being served from a non-root path.
	http.Handler

	// SignalingAddr returns the WebRTC signaling address in use.
	SignalingAddr() string

	// SignalingHosts returns the hosts WebRTC communications are happening on behalf of.
	SignalingHosts() []string
}

type simpleServer struct {
	rpcpb.UnimplementedAuthServiceServer
	mu                   sync.RWMutex
	grpcListener         net.Listener
	grpcServer           *grpc.Server
	grpcWebServer        *grpcweb.WrappedGrpcServer
	grpcGatewayHandler   *runtime.ServeMux
	httpServer           *http.Server
	webrtcServer         *webrtcServer
	webrtcAnswerer       *webrtcSignalingAnswerer
	signalingAddr        string
	signalingHosts       []string
	serviceServerCancels []func()
	serviceServers       []interface{}
	signalingCallQueue   WebRTCCallQueue
	secure               bool
	authRSAPrivKey       *rsa.PrivateKey
	internalUUID         string
	internalCreds        Credentials
	authHandlers         map[CredentialsType]AuthHandler
	exemptMethods        map[string]bool
	stopped              bool
	logger               golog.Logger
}

// newWithListener returns a new server ready to be started that
// will listen on the given listener.
func newWithListener(
	grpcListener net.Listener,
	logger golog.Logger,
	opts ...ServerOption,
) (Server, error) {
	var sOpts serverOptions
	for _, opt := range opts {
		if err := opt.apply(&sOpts); err != nil {
			return nil, err
		}
	}
	serverOpts := []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: keepAliveTime,
		}),
	}

	httpServer := &http.Server{
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: maxMessageSize,
	}

	authRSAPrivKey := sOpts.authRSAPrivateKey
	if !sOpts.unauthenticated && authRSAPrivKey == nil {
		privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
		if err != nil {
			return nil, err
		}
		authRSAPrivKey = privKey
	}

	internalCredsKey := make([]byte, 64)
	_, err := rand.Read(internalCredsKey)
	if err != nil {
		return nil, err
	}

	if sOpts.authHandlers == nil {
		sOpts.authHandlers = make(map[CredentialsType]AuthHandler)
	}

	grpcGatewayHandler := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.HTTPBodyMarshaler{JSONPB}))
	server := &simpleServer{
		grpcListener:       grpcListener,
		httpServer:         httpServer,
		grpcGatewayHandler: grpcGatewayHandler,
		authRSAPrivKey:     authRSAPrivKey,
		internalUUID:       uuid.NewString(),
		internalCreds: Credentials{
			Type:    credentialsTypeInternal,
			Payload: base64.StdEncoding.EncodeToString(internalCredsKey),
		},
		authHandlers:  sOpts.authHandlers,
		exemptMethods: make(map[string]bool),
		logger:        logger,
	}

	grpcLogger := logger.Desugar()
	if !(sOpts.debug || utils.Debug) {
		grpcLogger = grpcLogger.WithOptions(zap.IncreaseLevel(zap.LevelEnablerFunc(zapcore.ErrorLevel.Enabled)))
	}
	var unaryInterceptors []grpc.UnaryServerInterceptor
	unaryInterceptors = append(unaryInterceptors,
		grpc_recovery.UnaryServerInterceptor(),
		grpc_zap.UnaryServerInterceptor(grpcLogger),
		unaryServerCodeInterceptor(),
	)
	unaryAuthIntPos := -1
	if !sOpts.unauthenticated {
		unaryInterceptors = append(unaryInterceptors, server.authUnaryInterceptor)
		unaryAuthIntPos = len(unaryInterceptors) - 1
	}
	if sOpts.unaryInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, sOpts.unaryInterceptor)
	}
	unaryInterceptor := grpc_middleware.ChainUnaryServer(unaryInterceptors...)
	serverOpts = append(serverOpts, grpc.UnaryInterceptor(unaryInterceptor))

	var streamInterceptors []grpc.StreamServerInterceptor
	streamInterceptors = append(streamInterceptors,
		grpc_recovery.StreamServerInterceptor(),
		grpc_zap.StreamServerInterceptor(grpcLogger),
		streamServerCodeInterceptor(),
	)
	streamAuthIntPos := -1
	if !sOpts.unauthenticated {
		streamInterceptors = append(streamInterceptors, server.authStreamInterceptor)
		streamAuthIntPos = len(streamInterceptors) - 1
	}
	if sOpts.streamInterceptor != nil {
		streamInterceptors = append(streamInterceptors, sOpts.streamInterceptor)
	}
	streamInterceptor := grpc_middleware.ChainStreamServer(streamInterceptors...)
	serverOpts = append(serverOpts, grpc.StreamInterceptor(streamInterceptor))

	grpcServer := grpc.NewServer(
		serverOpts...,
	)
	reflection.Register(grpcServer)
	grpcWebServer := grpcweb.WrapServer(grpcServer, grpcweb.WithOriginFunc(func(origin string) bool {
		return true
	}))

	server.grpcServer = grpcServer
	server.grpcWebServer = grpcWebServer

	if sOpts.webrtcOpts.Enable && sOpts.webrtcOpts.ExternalSignalingAddress == "" {
		logger.Info("will run internal signaling service")
		signalingCallQueue := NewMemoryWebRTCCallQueue()
		server.signalingCallQueue = signalingCallQueue
		if err := server.RegisterServiceServer(
			context.Background(),
			&webrtcpb.SignalingService_ServiceDesc,
			NewWebRTCSignalingServer(signalingCallQueue, nil),
			webrtcpb.RegisterSignalingServiceHandlerFromEndpoint,
		); err != nil {
			return nil, err
		}
	}

	if !sOpts.unauthenticated {
		if err := server.RegisterServiceServer(
			context.Background(),
			&rpcpb.AuthService_ServiceDesc,
			server,
			rpcpb.RegisterAuthServiceHandlerFromEndpoint,
		); err != nil {
			return nil, err
		}
		server.authHandlers[credentialsTypeInternal] = MakeSimpleAuthHandler(
			[]string{server.internalUUID}, server.internalCreds.Payload)
		// Update this if the proto method or path changes
		server.exemptMethods["/proto.rpc.v1.AuthService/Authenticate"] = true
	}

	if sOpts.webrtcOpts.Enable {
		// TODO(https://github.com/viamrobotics/goutils/issues/12): Handle auth; right now we assume
		// successful auth to the signaler implies that auth should be allowed here, which is not 100%
		// true.
		webrtcUnaryInterceptors := make([]grpc.UnaryServerInterceptor, 0, len(unaryInterceptors))
		webrtcStreamInterceptors := make([]grpc.StreamServerInterceptor, 0, len(streamInterceptors))
		for idx, interceptor := range unaryInterceptors {
			if idx == unaryAuthIntPos {
				continue
			}
			webrtcUnaryInterceptors = append(webrtcUnaryInterceptors, interceptor)
		}
		for idx, interceptor := range streamInterceptors {
			if idx == streamAuthIntPos {
				continue
			}
			webrtcStreamInterceptors = append(webrtcStreamInterceptors, interceptor)
		}
		unaryInterceptor := grpc_middleware.ChainUnaryServer(webrtcUnaryInterceptors...)
		streamInterceptor := grpc_middleware.ChainStreamServer(webrtcStreamInterceptors...)

		server.webrtcServer = newWebRTCServerWithInterceptors(
			logger,
			unaryInterceptor,
			streamInterceptor,
		)
		address := sOpts.webrtcOpts.ExternalSignalingAddress
		answererDialOptsCopy := make([]DialOption, len(sOpts.webrtcOpts.ExternalSignalingDialOpts))
		copy(answererDialOptsCopy, sOpts.webrtcOpts.ExternalSignalingDialOpts)
		if address == "" {
			//nolint:makezero
			answererDialOptsCopy = append(answererDialOptsCopy, WithInsecure())
			address = grpcListener.Addr().String()
			if !sOpts.unauthenticated {
				//nolint:makezero
				answererDialOptsCopy = append(answererDialOptsCopy, WithEntityCredentials(server.internalUUID, server.internalCreds))
			}
		}
		server.signalingAddr = address
		signalingHosts := sOpts.webrtcOpts.SignalingHosts
		if len(signalingHosts) == 0 {
			signalingHosts = []string{"local"}
		}
		server.signalingHosts = signalingHosts
		logger.Infow("will run signaling answerer", "signaling_address", address, "for_hosts", signalingHosts)

		config := DefaultWebRTCConfiguration
		if sOpts.webrtcOpts.Config != nil {
			config = *sOpts.webrtcOpts.Config
		}

		server.webrtcAnswerer = newWebRTCSignalingAnswerer(
			address,
			signalingHosts,
			server.webrtcServer,
			answererDialOptsCopy,
			config,
			logger,
		)
	}

	return server, nil
}

// NewServer returns a new server ready to be started that
// will listen on some random port bound to localhost.
func NewServer(logger golog.Logger, opts ...ServerOption) (Server, error) {
	grpcListener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}

	return newWithListener(grpcListener, logger, opts...)
}

type requestType int

const (
	requestTypeNone requestType = iota
	requestTypeGRPC
	requestTypeGRPCWeb
)

func (ss *simpleServer) getRequestType(r *http.Request) requestType {
	if ss.grpcWebServer.IsAcceptableGrpcCorsRequest(r) || ss.grpcWebServer.IsGrpcWebRequest(r) {
		return requestTypeGRPCWeb
	} else if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		return requestTypeGRPC
	}
	return requestTypeNone
}

func requestWithHost(r *http.Request) *http.Request {
	if r.Host == "" {
		return r
	}
	host := strings.Split(r.Host, ":")[0]
	return r.WithContext(contextWithHost(r.Context(), host))
}

func (ss *simpleServer) GatewayHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ss.grpcGatewayHandler.ServeHTTP(w, requestWithHost(r))
	})
}

func (ss *simpleServer) GRPCHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = requestWithHost(r)
		switch ss.getRequestType(r) {
		case requestTypeGRPC:
			ss.grpcServer.ServeHTTP(w, r)
		case requestTypeGRPCWeb:
			ss.grpcWebServer.ServeHTTP(w, r)
		case requestTypeNone:
			fallthrough
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
}

// ServeHTTP is an all-in-one handler for any kind of gRPC traffic. This is useful
// in a scenario where all gRPC is served from the root path due to limitations of normal
// gRPC being served from a non-root path.
func (ss *simpleServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = requestWithHost(r)
	switch ss.getRequestType(r) {
	case requestTypeGRPC:
		ss.grpcServer.ServeHTTP(w, r)
	case requestTypeGRPCWeb:
		ss.grpcWebServer.ServeHTTP(w, r)
	case requestTypeNone:
		fallthrough
	default:
		ss.grpcGatewayHandler.ServeHTTP(w, r)
	}
}

func (ss *simpleServer) InternalAddr() net.Addr {
	return ss.grpcListener.Addr()
}

func (ss *simpleServer) Start() error {
	var err error
	var errMu sync.Mutex
	utils.PanicCapturingGo(func() {
		if serveErr := ss.grpcServer.Serve(ss.grpcListener); serveErr != nil {
			errMu.Lock()
			err = multierr.Combine(err, serveErr)
			errMu.Unlock()
		}
	})

	if ss.webrtcAnswerer == nil {
		return nil
	}

	ss.webrtcAnswerer.Start()

	errMu.Lock()
	defer errMu.Unlock()
	return err
}

func (ss *simpleServer) Serve(listener net.Listener) error {
	var handler http.Handler = ss
	if !ss.secure {
		http2Server, err := utils.NewHTTP2Server()
		if err != nil {
			return err
		}
		ss.httpServer.RegisterOnShutdown(func() {
			utils.UncheckedErrorFunc(http2Server.Close)
		})
		ss.httpServer.Addr = listener.Addr().String()
		handler = h2c.NewHandler(ss, http2Server.HTTP2)
	}
	ss.httpServer.Addr = listener.Addr().String()
	ss.httpServer.Handler = handler
	var err error
	var errMu sync.Mutex
	utils.ManagedGo(func() {
		if serveErr := ss.httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errMu.Lock()
			err = multierr.Combine(err, serveErr)
			errMu.Unlock()
		}
	}, nil)
	startErr := ss.Start()
	errMu.Lock()
	err = multierr.Combine(err, startErr)
	errMu.Unlock()
	return err
}

func (ss *simpleServer) SignalingAddr() string {
	return ss.signalingAddr
}

func (ss *simpleServer) SignalingHosts() []string {
	return ss.signalingHosts
}

func (ss *simpleServer) Stop() error {
	ss.mu.Lock()
	if ss.stopped {
		ss.mu.Unlock()
		return nil
	}
	ss.stopped = true
	ss.mu.Unlock()
	var err error
	if ss.signalingCallQueue != nil {
		err = multierr.Combine(err, ss.signalingCallQueue.Close())
	}
	ss.logger.Info("stopping server")
	defer ss.grpcServer.Stop()
	ss.logger.Info("canceling service servers for gateway")
	for _, cancel := range ss.serviceServerCancels {
		cancel()
	}
	ss.logger.Info("service servers for gateway canceled")
	ss.logger.Info("closing service servers")
	for _, srv := range ss.serviceServers {
		err = multierr.Combine(err, utils.TryClose(context.Background(), srv))
	}
	ss.logger.Info("service servers closed")
	if ss.webrtcAnswerer != nil {
		ss.logger.Info("stopping WebRTC answerer")
		ss.webrtcAnswerer.Stop()
		ss.logger.Info("WebRTC answerer stopped")
	}
	if ss.webrtcServer != nil {
		ss.logger.Info("stopping WebRTC server")
		ss.webrtcServer.Stop()
		ss.logger.Info("WebRTC server stopped")
	}
	ss.logger.Info("shutting down HTTP server")
	err = multierr.Combine(err, ss.httpServer.Shutdown(context.Background()))
	ss.logger.Info("HTTP server shut down")
	ss.logger.Info("stopped cleanly")
	return err
}

// A RegisterServiceHandlerFromEndpointFunc is a means to have a service attach itself to a gRPC gateway mux.
type RegisterServiceHandlerFromEndpointFunc func(
	ctx context.Context,
	mux *runtime.ServeMux,
	endpoint string,
	opts []grpc.DialOption,
) (err error)

func (ss *simpleServer) RegisterServiceServer(
	ctx context.Context,
	svcDesc *grpc.ServiceDesc,
	svcServer interface{},
	svcHandlers ...RegisterServiceHandlerFromEndpointFunc,
) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	stopCtx, stopCancel := context.WithCancel(ctx)
	ss.serviceServerCancels = append(ss.serviceServerCancels, stopCancel)
	ss.serviceServers = append(ss.serviceServers, svcServer)
	ss.grpcServer.RegisterService(svcDesc, svcServer)
	if ss.webrtcServer != nil {
		ss.webrtcServer.RegisterService(svcDesc, svcServer)
	}
	if len(svcHandlers) != 0 {
		addr := ss.grpcListener.Addr().String()
		opts := []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize))}
		if !ss.secure {
			opts = append(opts, grpc.WithInsecure())
		}
		for _, h := range svcHandlers {
			if err := h(stopCtx, ss.grpcGatewayHandler, addr, opts); err != nil {
				return err
			}
		}
	}
	return nil
}

func unaryServerCodeInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if s := status.FromContextError(err); s != nil {
			return nil, s.Err()
		}
		return nil, err
	}
}

func streamServerCodeInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, stream)
		if err == nil {
			return nil
		}
		if _, ok := status.FromError(err); ok {
			return err
		}
		if s := status.FromContextError(err); s != nil {
			return s.Err()
		}
		return err
	}
}
