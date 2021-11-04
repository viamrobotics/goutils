package rpcwebrtc

import (
	"context"
	"time"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// A ConfigProvider returns time bound WebRTC configurations.
type ConfigProvider interface {
	Config(ctx context.Context) (Config, error)
}

// A Config represents a time bound WebRTC configuration.
type Config struct {
	ICEServers []*webrtcpb.ICEServer
	Expires    time.Time
}
