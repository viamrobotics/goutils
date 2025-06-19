package rpc

import "time"

var (
	// MaxMessageSize is the maximum size a gRPC message can be.
	MaxMessageSize = 1 << 25

	// keepAliveTime is how often to establish Keepalive pings/expectations.
	keepAliveTime = 10 * time.Second

	// SocksProxyEnvVar is the name of an environment variable used by SOCKS
	// proxies to indicate the address through which to route all network traffic
	// via SOCKS5.
	SocksProxyEnvVar = "SOCKS_PROXY"

	// OnlySocksProxyEnvVar is the name of an environment variable used if all network
	// traffic should be done through SOCKS5.
	OnlySocksProxyEnvVar = "ONLY_SOCKS_PROXY"

	// TURNSOverrideEnvVar is the name of an environment variable used override
	// TURN to TURNS for any configured TURN servers.
	TURNSOverrideEnvVar = "TURNS_OVERRIDE"

	// TURNHostEnvVar is the name of an environment variable used to select
	// at most a single TURN server and set it's protocol to TURNS.
	TURNHostEnvVar = "TURN_HOST"

	// TURNTCPEnvVar is the name of an environment variable used to override
	// any configured TURN servers to use TCP instead of UDP.
	TURNTCPEnvVar = "TURN_TCP"

	// TURNPortEnvVar is the name of an environment variable used to override the port
	// for any configured TURN servers.
	TURNPortEnvVar = "TURN_PORT"
)
