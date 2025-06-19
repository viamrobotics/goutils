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

	// TURNSHostEnvVar is the name of an environment variable used to select
	// at most a single TURN server and set it's protocol to TURNS.
	TURNSHostEnvVar = "TURNS_HOST"

	// TURNTCPEnvVar is the name of an environment variable used to override
	// any configured TURN servers to use TCP instead of UDP.
	TURNTCPEnvVar = "TURN_TCP"

	// TURNPort is the name of an environment variable used to override the port
	// for any configured TURN servers.
	TURNPortEnvVar = "TURN_PORT"
)
