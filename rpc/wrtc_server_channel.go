package rpc

import (
	"context"
	"strings"
	"sync"

	"github.com/viamrobotics/webrtc/v3"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

// A webrtcServerChannel reflects the server end of a gRPC connection serviced over
// a WebRTC data channel.
type webrtcServerChannel struct {
	*webrtcBaseChannel
	mu sync.Mutex
	// entityInfo is the authenticated entity applied to every request context on this
	// channel. It is derived once at construction from the signaler-forwarded caller
	// identity (or the coarse audience approximation when none was forwarded), since both
	// inputs are fixed for the channel's lifetime.
	entityInfo EntityInfo
	server     *webrtcServer
	streams    map[uint64]*webrtcServerStream
}

// newWebRTCServerChannel wraps the given WebRTC data channel to be used as the server end
// of a gRPC connection.
func newWebRTCServerChannel(
	server *webrtcServer,
	peerConn *webrtc.PeerConnection,
	dataChannel *webrtc.DataChannel,
	authAudience []string,
	caller AuthenticatedCaller,
	logger utils.ZapCompatibleLogger,
) *webrtcServerChannel {
	base := newBaseChannel(
		server.workers.Context(),
		peerConn,
		dataChannel,
		server,
		nil,
		logger,
	)
	// Build the auth entity the auth interceptor would have set (that interceptor is not in
	// the WebRTC chain) from the caller identity the (trusted) signaler extracted and
	// forwarded. Fall back to the coarse audience approximation when the caller was
	// unauthenticated (no identity forwarded).
	entityInfo := EntityInfo{Entity: strings.Join(authAudience, ":")}
	if caller.Entity != "" {
		entityInfo.Entity = caller.Entity
	}
	if caller.Metadata != nil {
		entityInfo.AuthMetadata = caller.Metadata
	}

	ch := &webrtcServerChannel{
		entityInfo:        entityInfo,
		webrtcBaseChannel: base,
		server:            server,
		streams:           make(map[uint64]*webrtcServerStream),
	}
	dataChannel.OnMessage(ch.onChannelMessage)
	return ch
}

func (ch *webrtcServerChannel) writeHeaders(stream *webrtcpb.Stream, headers *webrtcpb.ResponseHeaders) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Response{
		Stream: stream,
		Type: &webrtcpb.Response_Headers{
			Headers: headers,
		},
	})
}

func (ch *webrtcServerChannel) writeMessage(stream *webrtcpb.Stream, msg *webrtcpb.ResponseMessage) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Response{
		Stream: stream,
		Type: &webrtcpb.Response_Message{
			Message: msg,
		},
	})
}

func (ch *webrtcServerChannel) writeTrailers(stream *webrtcpb.Stream, trailers *webrtcpb.ResponseTrailers) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Response{
		Stream: stream,
		Type: &webrtcpb.Response_Trailers{
			Trailers: trailers,
		},
	})
}

func (ch *webrtcServerChannel) removeStreamByID(id uint64) {
	ch.mu.Lock()
	delete(ch.streams, id)
	ch.mu.Unlock()
}

func (ch *webrtcServerChannel) onChannelMessage(msg webrtc.DataChannelMessage) {
	req := &webrtcpb.Request{}
	err := proto.Unmarshal(msg.Data, req)
	if err != nil {
		ch.webrtcBaseChannel.logger.Errorw("error unmarshaling message; discarding", "error", err)
		return
	}
	stream := req.GetStream()
	if stream == nil {
		ch.webrtcBaseChannel.logger.Error("no stream, discard request")
		return
	}

	id := stream.GetId()

	ch.mu.Lock()
	serverStream, ok := ch.streams[id]
	if !ok {
		// peek headers for timeout
		headers, ok := req.GetType().(*webrtcpb.Request_Headers)
		if !ok || headers.Headers == nil {
			ch.webrtcBaseChannel.logger.Debugf("expected headers as first message but got %T, discard request", req.GetType())
			ch.mu.Unlock()
			return
		}

		handlerCtx := metadata.NewIncomingContext(ch.ctx, metadataFromProto(headers.Headers.GetMetadata()))
		timeout := headers.Headers.GetTimeout().AsDuration()
		var cancelCtx func()
		if timeout == 0 {
			handlerCtx, cancelCtx = context.WithCancel(handlerCtx)
		} else {
			handlerCtx, cancelCtx = context.WithTimeout(handlerCtx, timeout)
		}
		handlerCtx = ContextWithPeerConnection(handlerCtx, ch.peerConn)

		// TODO(GOUT-11): Handle auth; right now we assume successful auth to the signaler
		// implies that auth should be allowed here, which is not 100% true. Apply the auth
		// entity derived once at channel construction from the signaler-forwarded token.
		handlerCtx = ContextWithAuthEntity(handlerCtx, ch.entityInfo)

		if sh := ch.server.statsHandler; sh != nil {
			handlerCtx = sh.TagRPC(handlerCtx, &stats.RPCTagInfo{FullMethodName: headers.Headers.GetMethod()})
			sh.HandleRPC(handlerCtx, &stats.InHeader{})
		}

		logger := utils.AddFieldsToLogger(ch.webrtcBaseChannel.logger, "id", id)
		serverStream = newWebRTCServerStream(handlerCtx, cancelCtx, headers.Headers.GetMethod(), ch, stream, ch.removeStreamByID, logger)
		ch.streams[id] = serverStream
	}
	ch.mu.Unlock()

	serverStream.onRequest(req)
}
