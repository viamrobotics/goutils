// Package rpcwebrtc providers client/server functionality for gRPC serviced over
// WebRTC data channels. The work is adapted from https://github.com/jsmouret/grpc-over-webrtc.
package rpcwebrtc

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/edaniels/golog"
	gwebrtc "github.com/edaniels/gostream/webrtc"
	"github.com/pion/webrtc/v3"
	"go.uber.org/multierr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	"go.viam.com/utils/rpc/dialer"
)

// ErrNoSignaler happens if a gRPC request is made on a server that does not support
// signaling for WebRTC.
var ErrNoSignaler = errors.New("no signaler present")

// Options control how WebRTC is utilized in a dial attempt.
type Options struct {
	// Insecure determines if the WebRTC connection is DTLS based.
	Insecure bool

	// Signaling server specifies the signaling server to
	// contact on behalf of this client for WebRTC communications.
	SignalingServer string

	// Config is the WebRTC specific configuration (i.e. ICE settings)
	Config *webrtc.Configuration
}

// Dial connects to the signaling service at the given address and attempts to establish
// a WebRTC connection with the corresponding peer reflected in the address.
func Dial(ctx context.Context, address string, opts Options, logger golog.Logger) (ch *ClientChannel, err error) {
	var host string
	if u, err := url.Parse(address); err == nil {
		address = u.Host
		host = u.Query().Get("host")
	}
	dialCtx, timeoutCancel := context.WithTimeout(ctx, 20*time.Second)
	defer timeoutCancel()

	logger.Debugw("connecting to signaling server", "address", address)

	conn, err := dialer.DialDirectGRPC(dialCtx, address, opts.Insecure)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = multierr.Combine(err, conn.Close())
	}()

	logger.Debug("connected")

	signalingClient := webrtcpb.NewSignalingServiceClient(conn)

	config := DefaultWebRTCConfiguration
	if opts.Config != nil {
		config = *opts.Config
	}
	pc, dc, err := newPeerConnectionForClient(ctx, config, logger)
	if err != nil {
		return nil, err
	}
	var successful bool
	defer func() {
		if !successful {
			err = multierr.Combine(err, pc.Close())
		}
	}()

	encodedSDP, err := gwebrtc.EncodeSDP(pc.LocalDescription())
	if err != nil {
		return nil, err
	}

	md := metadata.New(map[string]string{RPCHostMetadataField: host})
	callCtx := metadata.NewOutgoingContext(ctx, md)
	answerResp, err := signalingClient.Call(callCtx, &webrtcpb.CallRequest{Sdp: encodedSDP})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.Unimplemented {
			return nil, ErrNoSignaler
		}
		return nil, err
	}

	answer := webrtc.SessionDescription{}
	if err := gwebrtc.DecodeSDP(answerResp.Sdp, &answer); err != nil {
		return nil, err
	}

	err = pc.SetRemoteDescription(answer)
	if err != nil {
		return nil, err
	}

	clientCh := NewClientChannel(pc, dc, logger)

	select {
	case <-ctx.Done():
		return nil, multierr.Combine(ctx.Err(), clientCh.Close())
	case <-clientCh.Ready():
	}
	successful = true
	return clientCh, nil
}
