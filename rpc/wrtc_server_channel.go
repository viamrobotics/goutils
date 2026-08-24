package rpc

import (
	"context"
	"strings"
	"sync"

	jwt "github.com/golang-jwt/jwt/v4"
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
	// token (or the coarse audience approximation when none was forwarded), since both
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
	callerAuthToken string,
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
	// Reconstruct the auth entity the auth interceptor would have set (that interceptor is
	// not in the WebRTC chain). The signaler-forwarded token gives us both the caller's
	// identity (JWT subject) and its auth metadata claim; fall back to the coarse audience
	// approximation when no token was forwarded (e.g. an unauthenticated caller). We trust
	// the signaler's authentication, so we do not re-verify the token.
	entityInfo := EntityInfo{Entity: strings.Join(authAudience, ":")}
	claims, err := claimsFromAuthToken(callerAuthToken)
	if err != nil {
		// The signaler handed us a non-empty token we can't parse. Fall back to the
		// audience entity, but warn: this shouldn't happen and likely means something
		// upstream is forwarding malformed tokens.
		logger.Warnw("failed to parse signaler-forwarded caller auth token; using audience as entity", "error", err)
	} else if claims != nil {
		if entity := claims.Entity(); entity != "" {
			entityInfo.Entity = entity
		}
		entityInfo.AuthMetadata = claims.Metadata()
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

// claimsFromAuthToken parses the (already signaler-verified) bearer token into its
// claims. Parsing is unverified because we trust the signaler that forwarded the token.
// It returns (nil, nil) for an empty token (an unauthenticated caller) and (nil, err)
// for a non-empty but unparseable token, so the caller can surface that we were handed
// garbage.
func claimsFromAuthToken(token string) (*JWTClaims, error) {
	if token == "" {
		return nil, nil //nolint:nilnil
	}
	var claims JWTClaims
	if _, _, err := jwt.NewParser().ParseUnverified(token, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}
