package rpc

import "time"

var (
	// MaxMessageSize is the maximum size a gRPC message can be.
	MaxMessageSize = 1 << 25

	// KeepAliveTime is how often to establish client-side Keepalive pings/expectations.
	KeepAliveTime = 10 * time.Second

	// SocksProxyEnvVar is the name of an environment variable used by SOCKS
	// proxies to indicate the address through which to route all network traffic
	// via SOCKS5.
	SocksProxyEnvVar = "SOCKS_PROXY"

	// OnlySocksProxyEnvVar is the name of an environment variable used if all network
	// traffic should be done through SOCKS5.
	OnlySocksProxyEnvVar = "ONLY_SOCKS_PROXY"

	// TURNURIEnvVar is the name of an environment variable used to select at
	// most one TURN server. This parameter is experimental and may be changed or
	// removed in future versions.
	TURNURIEnvVar = "TURN_URI"

	// TURNPortEnvVar is the name of an environment variable used to override the
	// port used for TURN if a TURN server is configured. This parameter is
	// experimental and may be changed or removed in future versions.
	TURNPortEnvVar = "TURN_PORT"

	// TURNSchemeEnvVar is the name of an environment variable used to override
	// the scheme used for TURN if a TURN server is configured. Must be either
	// turn or turns. This parameter is experimental and may be changed or
	// removed in future versions.
	TURNSchemeEnvVar = "TURN_SCHEME"

	// TURNTransportEnvVar is the name of an environment variable used to
	// override the transport used for TURN if a TURN server is configured. Must
	// be either tcp or udp. This parameter is experimental and may be changed or
	// removed in future versions.
	TURNTransportEnvVar = "TURN_TRANSPORT"
)
