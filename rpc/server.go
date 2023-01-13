package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/edaniels/golog"
	"github.com/edaniels/zeroconf"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

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

	// InstanceNames are the instance names this server claims to be. Typically
	// set via options.
	InstanceNames() []string

	// Start only starts up the internal gRPC server.
	Start() error

	// Serve will externally serve, on the given listener, the
	// all in one handler described by http.Handler.
	Serve(listener net.Listener) error

	// ServeTLS will externally serve, using the given cert/key, the
	// all in one handler described by http.Handler. The provided tlsConfig
	// will be used for any extra TLS settings. If using mutual TLS authentication
	// (see WithTLSAuthHandler), then the tls.Config should have ClientAuth,
	// at a minimum, set to tls.VerifyClientCertIfGiven.
	ServeTLS(listener net.Listener, certFile, keyFile string, tlsConfig *tls.Config) error

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
}

type simpleServer struct {
	rpcpb.UnimplementedAuthServiceServer
	rpcpb.UnimplementedExternalAuthServiceServer
	mu                      sync.RWMutex
	activeBackgroundWorkers sync.WaitGroup
	grpcListener            net.Listener
	grpcServer              *grpc.Server
	grpcWebServer           *grpcweb.WrappedGrpcServer
	grpcGatewayHandler      *runtime.ServeMux
	httpServer              *http.Server
	instanceNames           []string
	webrtcServer            *webrtcServer
	webrtcAnswerers         []*webrtcSignalingAnswerer
	serviceServerCancels    []func()
	serviceServers          []interface{}
	signalingCallQueue      WebRTCCallQueue
	signalingServer         *WebRTCSignalingServer
	authRSAPrivKey          *rsa.PrivateKey
	authRSAPrivKeyKID       string
	internalUUID            string
	internalCreds           Credentials
	tlsAuthHandler          func(ctx context.Context, entities ...string) (interface{}, error)
	authHandlers            map[CredentialsType]AuthHandler
	authToType              CredentialsType
	authToHandler           AuthenticateToHandler
	mdnsServers             []*zeroconf.Server
	exemptMethods           map[string]bool
	tlsConfig               *tls.Config
	firstSeenTLSCertLeaf    *x509.Certificate
	stopped                 bool
	logger                  golog.Logger
}

var errMixedUnauthAndAuth = errors.New("cannot use unauthenticated and auth handlers at same time")

// NewServer returns a new server ready to be started that
// will listen on localhost on a random port unless TLS is turned
// on and authentication is enabled in which case the server will
// listen on all interfaces.
func NewServer(logger golog.Logger, opts ...ServerOption) (Server, error) {
	var sOpts serverOptions
	for _, opt := range opts {
		if err := opt.apply(&sOpts); err != nil {
			return nil, err
		}
	}
	if sOpts.unauthenticated && (len(sOpts.authHandlers) != 0 || sOpts.tlsAuthHandler != nil) {
		return nil, errMixedUnauthAndAuth
	}

	grpcBindAddr := sOpts.bindAddress
	if grpcBindAddr == "" {
		if sOpts.tlsConfig == nil || sOpts.unauthenticated {
			grpcBindAddr = "localhost:0"
		} else {
			grpcBindAddr = ":0"
		}
	}

	grpcListener, err := net.Listen("tcp", grpcBindAddr)
	if err != nil {
		return nil, err
	}

	serverOpts := []grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime: keepAliveTime,
		}),
	}

	var firstSeenTLSCert *tls.Certificate
	if sOpts.tlsConfig != nil {
		if len(sOpts.tlsConfig.Certificates) == 0 {
			if cert, err := sOpts.tlsConfig.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
				firstSeenTLSCert = cert
			} else {
				return nil, errors.New("invalid *tls.Config; expected at least 1 certificate")
			}
		} else {
			firstSeenTLSCert = &sOpts.tlsConfig.Certificates[0]
		}
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(sOpts.tlsConfig)))
	}

	var firstSeenTLSCertLeaf *x509.Certificate
	if firstSeenTLSCert != nil {
		leaf, err := x509.ParseCertificate(firstSeenTLSCert.Certificate[0])
		if err != nil {
			return nil, err
		}
		firstSeenTLSCertLeaf = leaf
	}

	httpServer := &http.Server{
		ReadTimeout:    10 * time.Second,
		MaxHeaderBytes: MaxMessageSize,
	}

	var authRSAPrivKeyThumbprint string
	authRSAPrivKey := sOpts.authRSAPrivateKey
	if !sOpts.unauthenticated {
		if authRSAPrivKey == nil {
			privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
			if err != nil {
				return nil, err
			}
			authRSAPrivKey = privKey
		}

		// create KID from authRSAPrivKey, this is used as the KID in the JWT header. This KID can be useful when more
		// than one KID is accepted.
		authRSAPrivKeyThumbprint, err = RSAPublicKeyThumbprint(&authRSAPrivKey.PublicKey)
		if err != nil {
			return nil, err
		}
	}

	internalCredsKey := make([]byte, 64)
	_, err = rand.Read(internalCredsKey)
	if err != nil {
		return nil, err
	}

	if sOpts.authHandlers == nil {
		sOpts.authHandlers = make(map[CredentialsType]AuthHandler)
	}

	grpcGatewayHandler := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
	)

	server := &simpleServer{
		grpcListener:       grpcListener,
		httpServer:         httpServer,
		grpcGatewayHandler: grpcGatewayHandler,
		authRSAPrivKey:     authRSAPrivKey,
		authRSAPrivKeyKID:  authRSAPrivKeyThumbprint,
		internalUUID:       uuid.NewString(),
		internalCreds: Credentials{
			Type:    credentialsTypeInternal,
			Payload: base64.StdEncoding.EncodeToString(internalCredsKey),
		},
		tlsAuthHandler:       sOpts.tlsAuthHandler,
		authHandlers:         sOpts.authHandlers,
		authToType:           sOpts.authToType,
		authToHandler:        sOpts.authToHandler,
		exemptMethods:        make(map[string]bool),
		tlsConfig:            sOpts.tlsConfig,
		firstSeenTLSCertLeaf: firstSeenTLSCertLeaf,
		logger:               logger,
	}

	grpcLogger := logger.Desugar()
	if !(sOpts.debug || utils.Debug) {
		grpcLogger = grpcLogger.WithOptions(zap.IncreaseLevel(zap.LevelEnablerFunc(zapcore.ErrorLevel.Enabled)))
	}
	if sOpts.unknownStreamDesc != nil {
		serverOpts = append(serverOpts, grpc.UnknownServiceHandler(sOpts.unknownStreamDesc.Handler))
	}
	var unaryInterceptors []grpc.UnaryServerInterceptor
	unaryInterceptors = append(unaryInterceptors,
		grpc_recovery.UnaryServerInterceptor(grpc_recovery.WithRecoveryHandler(
			grpc_recovery.RecoveryHandlerFunc(func(p interface{}) error {
				err := status.Errorf(codes.Internal, "%s", p)
				logger.Errorw("panicked while calling unary server method", "error", errors.WithStack(err))
				return err
			}))),
		grpc_zap.UnaryServerInterceptor(grpcLogger),
		unaryServerCodeInterceptor(),
	)
	unaryAuthIntPos := -1
	if !sOpts.unauthenticated {
		unaryInterceptors = append(unaryInterceptors, server.authUnaryInterceptor)
		unaryAuthIntPos = len(unaryInterceptors) - 1
	}
	if sOpts.unaryInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, func(
			ctx context.Context,
			req interface{},
			info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler,
		) (interface{}, error) {
			if server.exemptMethods[info.FullMethod] {
				return handler(ctx, req)
			}
			return sOpts.unaryInterceptor(ctx, req, info, handler)
		})
	}
	unaryInterceptor := grpc_middleware.ChainUnaryServer(unaryInterceptors...)
	serverOpts = append(serverOpts, grpc.UnaryInterceptor(unaryInterceptor))

	var streamInterceptors []grpc.StreamServerInterceptor
	streamInterceptors = append(streamInterceptors,
		grpc_recovery.StreamServerInterceptor(grpc_recovery.WithRecoveryHandler(
			grpc_recovery.RecoveryHandlerFunc(func(p interface{}) error {
				err := status.Errorf(codes.Internal, "%s", p)
				logger.Errorw("panicked while calling stream server method", "error", errors.WithStack(err))
				return err
			}))),
		grpc_zap.StreamServerInterceptor(grpcLogger),
		streamServerCodeInterceptor(),
	)
	streamAuthIntPos := -1
	if !sOpts.unauthenticated {
		streamInterceptors = append(streamInterceptors, server.authStreamInterceptor)
		streamAuthIntPos = len(streamInterceptors) - 1
	}
	if sOpts.streamInterceptor != nil {
		streamInterceptors = append(streamInterceptors, func(
			srv interface{},
			serverStream grpc.ServerStream,
			info *grpc.StreamServerInfo,
			handler grpc.StreamHandler,
		) error {
			if server.exemptMethods[info.FullMethod] {
				return handler(srv, serverStream)
			}
			return sOpts.streamInterceptor(srv, serverStream, info, handler)
		})
	}
	streamInterceptor := grpc_middleware.ChainStreamServer(streamInterceptors...)
	serverOpts = append(serverOpts, grpc.StreamInterceptor(streamInterceptor))

	if sOpts.statsHandler != nil {
		serverOpts = append(serverOpts, grpc.StatsHandler(sOpts.statsHandler))
	}

	grpcServer := grpc.NewServer(
		serverOpts...,
	)
	reflection.Register(grpcServer)
	grpcWebServer := grpcweb.WrapServer(grpcServer, grpcweb.WithOriginFunc(func(origin string) bool {
		return true
	}))

	server.grpcServer = grpcServer
	server.grpcWebServer = grpcWebServer

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

	if sOpts.authToHandler != nil {
		if err := server.RegisterServiceServer(
			context.Background(),
			&rpcpb.ExternalAuthService_ServiceDesc,
			server,
			rpcpb.RegisterExternalAuthServiceHandlerFromEndpoint,
		); err != nil {
			return nil, err
		}
	}

	var mDNSAddress *net.TCPAddr
	if sOpts.listenerAddress != nil {
		mDNSAddress = sOpts.listenerAddress
	} else {
		var ok bool
		mDNSAddress, ok = grpcListener.Addr().(*net.TCPAddr)
		if !ok {
			return nil, errors.Errorf("expected *net.TCPAddr but got %T", grpcListener.Addr())
		}
	}

	supportedServices := []string{"grpc"}
	if sOpts.webrtcOpts.Enable {
		supportedServices = append(supportedServices, "webrtc")
	}
	instanceNames := sOpts.instanceNames
	if len(instanceNames) == 0 {
		instanceName, err := InstanceNameFromAddress(mDNSAddress.String())
		if err != nil {
			return nil, err
		}
		instanceNames = []string{instanceName}
	}
	server.instanceNames = instanceNames
	if !sOpts.disableMDNS {
		if mDNSAddress.IP.IsLoopback() {
			hostname, err := os.Hostname()
			if err != nil {
				return nil, err
			}
			ifcs, err := net.Interfaces()
			if err != nil {
				return nil, err
			}
			var loopbackIfc net.Interface
			for _, ifc := range ifcs {
				if (ifc.Flags&net.FlagUp) == 0 ||
					(ifc.Flags&net.FlagLoopback) == 0 ||
					(ifc.Flags&net.FlagMulticast) == 0 {
					continue
				}
				loopbackIfc = ifc
				break
			}
			for _, host := range instanceNames {
				hosts := []string{host, strings.ReplaceAll(host, ".", "-")}
				for _, host := range hosts {
					mdnsServer, err := zeroconf.RegisterProxy(
						host,
						"_rpc._tcp",
						"local.",
						mDNSAddress.Port,
						hostname,
						[]string{"127.0.0.1"},
						supportedServices,
						[]net.Interface{loopbackIfc},
					)
					if err != nil {
						return nil, err
					}
					server.mdnsServers = append(server.mdnsServers, mdnsServer)
				}
			}
		} else {
			for _, host := range instanceNames {
				hosts := []string{host, strings.ReplaceAll(host, ".", "-")}
				for _, host := range hosts {
					mdnsServer, err := zeroconf.RegisterDynamic(
						host,
						"_rpc._tcp",
						"local.",
						mDNSAddress.Port,
						supportedServices,
						nil,
					)
					if err != nil {
						return nil, err
					}
					server.mdnsServers = append(server.mdnsServers, mdnsServer)
				}
			}
		}
	}

	if sOpts.webrtcOpts.Enable {
		// TODO(GOUT-11): Handle auth; right now we assume
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

		if sOpts.unknownStreamDesc == nil {
			server.webrtcServer = newWebRTCServerWithInterceptors(
				logger,
				unaryInterceptor,
				streamInterceptor,
			)
		} else {
			server.webrtcServer = newWebRTCServerWithInterceptorsAndUnknownStreamHandler(
				logger,
				unaryInterceptor,
				streamInterceptor,
				sOpts.unknownStreamDesc,
			)
		}
		if sOpts.webrtcOpts.OnPeerAdded != nil {
			server.webrtcServer.onPeerAdded = sOpts.webrtcOpts.OnPeerAdded
		}
		if sOpts.webrtcOpts.OnPeerRemoved != nil {
			server.webrtcServer.onPeerRemoved = sOpts.webrtcOpts.OnPeerRemoved
		}
		reflection.Register(server.webrtcServer)

		config := DefaultWebRTCConfiguration
		if sOpts.webrtcOpts.Config != nil {
			config = *sOpts.webrtcOpts.Config
		}

		externalSignalingHosts := sOpts.webrtcOpts.ExternalSignalingHosts
		internalSignalingHosts := sOpts.webrtcOpts.InternalSignalingHosts
		if len(externalSignalingHosts) == 0 {
			externalSignalingHosts = instanceNames
		}
		if len(internalSignalingHosts) == 0 {
			internalSignalingHosts = instanceNames
		}

		if sOpts.webrtcOpts.ExternalSignalingAddress != "" {
			logger.Infow(
				"will run external signaling answerer",
				"signaling_address", sOpts.webrtcOpts.ExternalSignalingAddress,
				"for_hosts", externalSignalingHosts,
			)
			server.webrtcAnswerers = append(server.webrtcAnswerers, newWebRTCSignalingAnswerer(
				sOpts.webrtcOpts.ExternalSignalingAddress,
				externalSignalingHosts,
				server.webrtcServer,
				sOpts.webrtcOpts.ExternalSignalingDialOpts,
				config,
				logger.Named("external_signaler"),
			))
		} else {
			sOpts.webrtcOpts.EnableInternalSignaling = true
		}

		if sOpts.webrtcOpts.EnableInternalSignaling {
			logger.Debug("will run internal signaling service")
			signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
			server.signalingCallQueue = signalingCallQueue
			server.signalingServer = NewWebRTCSignalingServer(signalingCallQueue, nil, logger, internalSignalingHosts...)
			if err := server.RegisterServiceServer(
				context.Background(),
				&webrtcpb.SignalingService_ServiceDesc,
				server.signalingServer,
				webrtcpb.RegisterSignalingServiceHandlerFromEndpoint,
			); err != nil {
				return nil, err
			}

			address := grpcListener.Addr().String()
			logger.Debugw(
				"will run internal signaling answerer",
				"signaling_address", address,
				"for_hosts", internalSignalingHosts,
			)
			var answererDialOpts []DialOption
			if sOpts.tlsConfig != nil {
				tlsConfig := sOpts.tlsConfig.Clone()
				tlsConfig.ServerName = server.firstSeenTLSCertLeaf.Subject.CommonName
				answererDialOpts = append(answererDialOpts, WithTLSConfig(tlsConfig))
			} else {
				answererDialOpts = append(answererDialOpts, WithInsecure())
			}
			if !sOpts.unauthenticated {
				answererDialOpts = append(answererDialOpts, WithEntityCredentials(server.internalUUID, server.internalCreds))
			}
			server.webrtcAnswerers = append(server.webrtcAnswerers, newWebRTCSignalingAnswerer(
				address,
				internalSignalingHosts,
				server.webrtcServer,
				answererDialOpts,
				config,
				logger.Named("internal_signaler"),
			))
		}
	}

	return server, nil
}

func (ss *simpleServer) InstanceNames() []string {
	return ss.instanceNames
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

	for _, answerer := range ss.webrtcAnswerers {
		answerer.Start()
	}

	errMu.Lock()
	defer errMu.Unlock()
	return err
}

func (ss *simpleServer) Serve(listener net.Listener) error {
	return ss.serveTLS(listener, "", "", nil)
}

func (ss *simpleServer) ServeTLS(listener net.Listener, certFile, keyFile string, tlsConfig *tls.Config) error {
	return ss.serveTLS(listener, certFile, keyFile, tlsConfig)
}

func (ss *simpleServer) serveTLS(listener net.Listener, certFile, keyFile string, tlsConfig *tls.Config) error {
	ss.mu.Lock()
	ss.httpServer.Addr = listener.Addr().String()
	ss.httpServer.Handler = ss
	secure := true
	if certFile == "" && keyFile == "" {
		secure = false
		http2Server, err := utils.NewHTTP2Server()
		if err != nil {
			return err
		}
		ss.httpServer.RegisterOnShutdown(func() {
			utils.UncheckedErrorFunc(http2Server.Close)
		})
		ss.httpServer.Handler = h2c.NewHandler(ss.httpServer.Handler, http2Server.HTTP2)
	}

	var err error
	var errMu sync.Mutex
	ss.activeBackgroundWorkers.Add(1)
	ss.mu.Unlock()
	utils.ManagedGo(func() {
		var serveErr error
		if secure {
			if tlsConfig != nil {
				ss.httpServer.TLSConfig = tlsConfig.Clone()
			}
			serveErr = ss.httpServer.ServeTLS(listener, certFile, keyFile)
		} else {
			serveErr = ss.httpServer.Serve(listener)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errMu.Lock()
			err = multierr.Combine(err, serveErr)
			errMu.Unlock()
		}
	}, ss.activeBackgroundWorkers.Done)
	startErr := ss.Start()
	errMu.Lock()
	err = multierr.Combine(err, startErr)
	errMu.Unlock()
	return err
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
	if ss.signalingServer != nil {
		ss.signalingServer.Close()
	}
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
	for idx, answerer := range ss.webrtcAnswerers {
		ss.logger.Infow("stopping WebRTC answerer", "num", idx)
		answerer.Stop()
		ss.logger.Infow("WebRTC answerer stopped", "num", idx)
	}
	if ss.webrtcServer != nil {
		ss.logger.Info("stopping WebRTC server")
		ss.webrtcServer.Stop()
		ss.logger.Info("WebRTC server stopped")
	}
	for _, mdnsServer := range ss.mdnsServers {
		mdnsServer.Shutdown()
	}
	ss.logger.Info("shutting down HTTP server")
	err = multierr.Combine(err, ss.httpServer.Shutdown(context.Background()))
	ss.logger.Info("HTTP server shut down")
	ss.activeBackgroundWorkers.Wait()
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
		opts := []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize))}
		if ss.tlsConfig == nil {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			tlsConfig := ss.tlsConfig.Clone()
			tlsConfig.ServerName = ss.firstSeenTLSCertLeaf.DNSNames[0]
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
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

// InstanceNameFromAddress returns a suitable instance name given an address.
// If it's empty or an IP address, a new UUID is returned.
func InstanceNameFromAddress(addr string) (string, error) {
	if strings.Contains(addr, ":") {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return "", err
		}
		addr = host
	}
	if net.ParseIP(addr) == nil {
		return addr, nil
	}
	// will use a UUID since we have no better choice
	return uuid.NewString(), nil
}

// PeerConnectionType describes the type of connection of a peer.
type PeerConnectionType uint16

// Known types of peer connections.
const (
	PeerConnectionTypeUnknown = PeerConnectionType(iota)
	PeerConnectionTypeGRPC
	PeerConnectionTypeWebRTC
)

// PeerConnectionInfo details information about a connection.
type PeerConnectionInfo struct {
	ConnectionType PeerConnectionType
	LocalAddress   string
	RemoteAddress  string
}

// PeerConnectionInfoFromContext returns as much information about the connection as can be found
// from the request context.
func PeerConnectionInfoFromContext(ctx context.Context) PeerConnectionInfo {
	if p, ok := peer.FromContext(ctx); ok && p != nil {
		return PeerConnectionInfo{
			ConnectionType: PeerConnectionTypeGRPC,
			RemoteAddress:  p.Addr.String(),
		}
	}
	if pc, ok := ContextPeerConnection(ctx); ok {
		candPair, hasCandPair := webrtcPeerConnCandPair(pc)
		if hasCandPair {
			return PeerConnectionInfo{
				ConnectionType: PeerConnectionTypeWebRTC,
				LocalAddress:   candPair.Local.String(),
				RemoteAddress:  candPair.Remote.String(),
			}
		}
	}
	return PeerConnectionInfo{
		ConnectionType: PeerConnectionTypeUnknown,
	}
}
