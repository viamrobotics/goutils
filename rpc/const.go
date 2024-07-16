package rpc

import "time"

var (
	// MaxMessageSize is the maximum size a gRPC message can be.
	MaxMessageSize = 1 << 25

	// keepAliveTime is how often to establish Keepalive pings/expectations.
	keepAliveTime = 10 * time.Second
)
