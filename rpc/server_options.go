package rpc

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"net"

	"github.com/pion/webrtc/v3"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

// serverOptions change the runtime behavior of the server.
type serverOptions struct {
	bindAddress       string
	listenerAddress   *net.TCPAddr
	tlsConfig         *tls.Config
	webrtcOpts        WebRTCServerOptions
	unaryInterceptor  grpc.UnaryServerInterceptor
	streamInterceptor grpc.StreamServerInterceptor

	// instanceNames are the name of this server and will be used
	// to report itself over mDNS.
	instanceNames []string

	// unauthenticated determines if requests should be authenticated.
	unauthenticated bool

	// authRSAPrivateKey is used to sign JWTs for authentication
	authRSAPrivateKey *rsa.PrivateKey

	// debug is helpful to turn on when the library isn't working quite right.
	// It will output much more logs.
	debug bool

	tlsAuthHandler func(ctx context.Context, entities ...string) (interface{}, error)
	authHandlers   map[CredentialsType]AuthHandler

	authToType    CredentialsType
	authToHandler AuthenticateToHandler
	disableMDNS   bool

	// stats monitoring on the connections.
	statsHandler stats.Handler

	unknownStreamDesc *grpc.StreamDesc
}

// WebRTCServerOptions control how WebRTC is utilized in a server.
type WebRTCServerOptions struct {
	// Enable controls if WebRTC should be turned on. It is disabled
	// by default since signaling has the potential to open up random
	// ports on the host which may not be expected.
	Enable bool

	// ExternalSignalingDialOpts are the options used to dial the external signaler.
	ExternalSignalingDialOpts []DialOption

	// ExternalSignalingAddress specifies where the WebRTC signaling
	// answerer should connect to and "listen" from. If it is empty,
	// it will connect to the server's internal address acting as
	// an answerer for itself.
	ExternalSignalingAddress string

	// EnableInternalSignaling specifies whether an internal signaling answerer
	// should be started up. This is useful if you want to have a fallback
	// server if the external cannot be reached. It is started up by default
	// if ExternalSignalingAddress is unset.
	EnableInternalSignaling bool

	// ExternalSignalingHosts specifies what hosts are being listened for when answering
	// externally.
	ExternalSignalingHosts []string

	// InternalSignalingHosts specifies what hosts are being listened for when answering
	// internally.
	InternalSignalingHosts []string

	// Config is the WebRTC specific configuration (i.e. ICE settings)
	Config *webrtc.Configuration
}

// A ServerOption changes the runtime behavior of the server.
// Cribbed from https://github.com/grpc/grpc-go/blob/aff571cc86e6e7e740130dbbb32a9741558db805/dialoptions.go#L41
type ServerOption interface {
	apply(*serverOptions) error
}

// funcServerOption wraps a function that modifies serverOptions into an
// implementation of the ServerOption interface.
type funcServerOption struct {
	f func(*serverOptions) error
}

func (fdo *funcServerOption) apply(do *serverOptions) error {
	return fdo.f(do)
}

func newFuncServerOption(f func(*serverOptions) error) *funcServerOption {
	return &funcServerOption{
		f: f,
	}
}

// WithInternalBindAddress returns a ServerOption which sets the bind address
// for the gRPC listener. If unset, the address is localhost on a
// random port unless TLS is turned on and authentication is enabled
// in which case the server will bind to all interfaces.
func WithInternalBindAddress(address string) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.bindAddress = address
		return nil
	})
}

// WithExternalListenerAddress returns a ServerOption which sets the listener address
// if the server is going to be served via its handlers and not internally.
// This is only helpful for mDNS broadcasting. If the server has TLS enabled
// internally (see WithInternalTLSConfig), then the internal listener will
// bind everywhere and this option may not be needed.
func WithExternalListenerAddress(address *net.TCPAddr) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.listenerAddress = address
		return nil
	})
}

// WithInternalTLSConfig returns a ServerOption which sets the TLS config
// for the internal listener. This is needed to have mutual TLS authentication
// work (see WithTLSAuthHandler). When using ServeTLS on the server, which serves
// from an external listener, with mutual TLS authentication, you will want to pass
// its own tls.Config with ClientAuth, at a minimum, set to tls.VerifyClientCertIfGiven.
func WithInternalTLSConfig(config *tls.Config) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.tlsConfig = config.Clone()
		if o.tlsConfig.ClientAuth == 0 {
			o.tlsConfig.ClientAuth = tls.VersionTLS12
		}
		o.tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		return nil
	})
}

// WithWebRTCServerOptions returns a ServerOption which sets the WebRTC options
// to use if the server sets up serving WebRTC connections.
func WithWebRTCServerOptions(webrtcOpts WebRTCServerOptions) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.webrtcOpts = webrtcOpts
		return nil
	})
}

// WithUnaryServerInterceptor returns a ServerOption that sets a interceptor for
// all unary grpc methods registered. It will run after authentication and prior
// to the registered method.
func WithUnaryServerInterceptor(unaryInterceptor grpc.UnaryServerInterceptor) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.unaryInterceptor = unaryInterceptor
		return nil
	})
}

// WithStreamServerInterceptor returns a ServerOption that sets a interceptor for
// all stream grpc methods registered. It will run after authentication and prior
// to the registered method.
func WithStreamServerInterceptor(streamInterceptor grpc.StreamServerInterceptor) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.streamInterceptor = streamInterceptor
		return nil
	})
}

// WithInstanceNames returns a ServerOption which sets the names for this
// server instance. These names will be used for mDNS service discovery to
// report the server itself. If unset the value is the address set by
// WithExternalListenerAddress, WithInternalBindAddress, or the localhost and random port address,
// in preference order from left to right.
func WithInstanceNames(names ...string) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.instanceNames = names
		return nil
	})
}

// WithUnauthenticated returns a ServerOption which turns off all authentication
// to the server's endpoints.
func WithUnauthenticated() ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.unauthenticated = true
		return nil
	})
}

// WithAuthRSAPrivateKey returns a ServerOption which sets the private key to
// use for signed JWTs.
func WithAuthRSAPrivateKey(authRSAPrivateKey *rsa.PrivateKey) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.authRSAPrivateKey = authRSAPrivateKey
		return nil
	})
}

// WithDebug returns a ServerOption which informs the server to be in a
// debug mode as much as possible.
func WithDebug() ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.debug = true
		return nil
	})
}

// WithTLSAuthHandler returns a ServerOption which when TLS info is available to a connection, it will
// authenticate the given entities in the event that no other authentication has been established via
// the standard auth handler. Optionally, verifyEntity may be specified which can do further entity
// checking and return return opaque info about the entity that will be bound to the context accessible
// via ContextAuthEntity.
func WithTLSAuthHandler(entities []string, verifyEntity func(ctx context.Context, entities ...string) (interface{}, error)) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		entityChecker := MakeEntitiesChecker(entities)
		o.tlsAuthHandler = func(ctx context.Context, recvEntities ...string) (interface{}, error) {
			if err := entityChecker(ctx, recvEntities...); err != nil {
				return nil, errNotTLSAuthed
			}
			if verifyEntity == nil {
				return recvEntities, nil
			}
			return verifyEntity(ctx, recvEntities...)
		}
		return nil
	})
}

// WithAuthHandler returns a ServerOption which adds an auth handler associated
// to the given type to use for authentication requests.
func WithAuthHandler(forType CredentialsType, handler AuthHandler) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		if forType == credentialsTypeInternal {
			return errors.Errorf("cannot use %q externally", forType)
		}
		if forType == "" {
			return errors.New("type cannot be empty")
		}
		if _, ok := o.authHandlers[forType]; ok {
			return errors.Errorf("%q already has a registered handler", forType)
		}
		if o.authHandlers == nil {
			o.authHandlers = make(map[CredentialsType]AuthHandler)
		}
		o.authHandlers[forType] = handler

		return nil
	})
}

// WithAuthenticateToHandler returns a ServerOption which adds an authentication
// handler designed to allow the caller to authenticate itself to some other entity.
// This is useful when externally authenticating as one entity for the purpose of
// getting access to another entity. Only one handler can exist and the forType
// parameter will be the type associated with the JWT made for the authenticated to entity.
// This can technically be used internal to the same server to "assume" the identity of
// another entity but is not intended for such usage.
func WithAuthenticateToHandler(forType CredentialsType, handler AuthenticateToHandler) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		if forType == credentialsTypeInternal {
			return errors.Errorf("cannot use %q externally", forType)
		}
		if forType == "" {
			return errors.New("type cannot be empty")
		}
		o.authToType = forType
		o.authToHandler = handler

		return nil
	})
}

// WithDisableMulticastDNS returns a ServerOption which disables
// using mDNS to broadcast how to connect to this host.
func WithDisableMulticastDNS() ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.disableMDNS = true
		return nil
	})
}

// WithUnknownServiceHandler returns a ServerOption that allows for adding a custom
// unknown service handler. The provided method is a bidi-streaming RPC service
// handler that will be invoked instead of returning the "unimplemented" gRPC
// error whenever a request is received for an unregistered service or method.
// The handling function and stream interceptor (if set) have full access to
// the ServerStream, including its Context.
// See grpc#WithUnknownServiceHandler.
func WithUnknownServiceHandler(streamHandler grpc.StreamHandler) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.unknownStreamDesc = &grpc.StreamDesc{
			StreamName:    "unknown_service_handler",
			Handler:       streamHandler,
			ClientStreams: true,
			ServerStreams: true,
		}
		return nil
	})
}

// WithStatsHandler returns a ServerOption which sets the the stats handler on the
// DialOption that specifies the stats handler for all the RPCs and underlying network
// connections.
func WithStatsHandler(handler stats.Handler) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
		o.statsHandler = handler
		return nil
	})
}
