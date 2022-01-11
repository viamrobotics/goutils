package rpc

import "crypto/tls"

// dialOptions configure a Dial call. dialOptions are set by the DialOption
// values passed to Dial.
type dialOptions struct {
	// insecure determines if the RPC connection is TLS based.
	insecure bool

	// tlsConfig is the TLS config to use for any secured connections.
	tlsConfig *tls.Config

	authEntity string

	// creds are used to authenticate the request. These are orthogonal to insecure,
	// however it's strongly recommended to be on a secure connection when transmitting
	// credentials.
	creds Credentials

	// webrtcOpts control how WebRTC is utilized in a dial attempt.
	webrtcOpts DialWebRTCOptions

	externalAuthAddr string

	// debug is helpful to turn on when the library isn't working quite right.
	// It will output much more logs.
	debug bool
}

// DialOption configures how we set up the connection.
// Cribbed from https://github.com/grpc/grpc-go/blob/aff571cc86e6e7e740130dbbb32a9741558db805/dialoptions.go#L41
type DialOption interface {
	apply(*dialOptions)
}

// funcDialOption wraps a function that modifies dialOptions into an
// implementation of the DialOption interface.
type funcDialOption struct {
	f func(*dialOptions)
}

func (fdo *funcDialOption) apply(do *dialOptions) {
	fdo.f(do)
}

func newFuncDialOption(f func(*dialOptions)) *funcDialOption {
	return &funcDialOption{
		f: f,
	}
}

// WithInsecure returns a DialOption which disables transport security for this
// ClientConn. Note that transport security is required unless WithInsecure is
// set.
func WithInsecure() DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.insecure = true
	})
}

// WithCredentials returns a DialOption which sets the credentials to use for
// authenticating the request. The associated entity is assumed to be the
// address of the server. This is mutually exclusive with
// WithEntityCredentials.
func WithCredentials(creds Credentials) DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.creds = creds
	})
}

// WithEntityCredentials returns a DialOption which sets the entity credentials
// to use for authenticating the request. This is mutually exclusive with
// WithCredentials.
func WithEntityCredentials(entity string, creds Credentials) DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.authEntity = entity
		o.creds = creds
	})
}

// WithExternalAuth returns a DialOption which sets the address to use
// to perform authentication. Authentication done in this manner will never
// have the dialed address be authenticated against but instead have access
// tokens sent directly to it.
// Note: When making a gRPC connection to the given address, the same
// dial options are used. That means if the external address is secured,
// so must the internal target.
func WithExternalAuth(addr string) DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.externalAuthAddr = addr
	})
}

// WithWebRTCOptions returns a DialOption which sets the WebRTC options
// to use if the dialer tries to establish a WebRTC connection.
func WithWebRTCOptions(webrtcOpts DialWebRTCOptions) DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.webrtcOpts = webrtcOpts
	})
}

// WithDialDebug returns a DialOption which informs the client to be in a
// debug mode as much as possible.
func WithDialDebug() DialOption {
	return newFuncDialOption(func(o *dialOptions) {
		o.debug = true
	})
}
