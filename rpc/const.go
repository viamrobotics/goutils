package rpc

import "time"

const (
	// keepAliveTime is how often to establish Keepalive pings/expectations.
	keepAliveTime = 10 * time.Second
)

// MaxMessageSize is the maximum size a gRPC message can be.
var MaxMessageSize = 1 << 25
