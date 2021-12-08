package rpc

import "time"

const (
	// keepAliveTime is how often to establish Keepalive pings/expectations.
	keepAliveTime = 10 * time.Second
)

// maxMessageSize is the maximum size a gRPC message can be.
var maxMessageSize = 1 << 25
