package rpc

import (
	"crypto/rsa"

	"github.com/pion/webrtc/v3"
	"github.com/pkg/errors"

	"google.golang.org/grpc"
)

// serverOptions change the runtime behavior of the server.
type serverOptions struct {
	webrtcOpts        WebRTCServerOptions
	unaryInterceptor  grpc.UnaryServerInterceptor
	streamInterceptor grpc.StreamServerInterceptor

	// unauthenticated determines if requests should be authenticated.
	unauthenticated bool

	// authRSAPrivateKey is used to sign JWTs for authentication
	authRSAPrivateKey *rsa.PrivateKey

	// debug is helpful to turn on when the library isn't working quite right.
	// It will output much more logs.
	debug bool

	authHandlers map[CredentialsType]AuthHandler
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

	// SignalingHost specifies what host is being listened for.
	SignalingHost string

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

// WithAuthHandler returns a ServerOption which adds an auth handler associated
// to the given type to use for authentication requests.
func WithAuthHandler(forType CredentialsType, handler AuthHandler) ServerOption {
	return newFuncServerOption(func(o *serverOptions) error {
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
