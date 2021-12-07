package rpc

import "time"

const (
	// KeepAliveTime is how often to establish Keepalive pings/expectations.
	KeepAliveTime = 10 * time.Second
)

// MaxMessageSize is the maximum size a gRPC message can be.
var MaxMessageSize = 1 << 25
