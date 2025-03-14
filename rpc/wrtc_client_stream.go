package rpc

import (
	"context"
	"errors"
	"io"
	"math"

	protov1 "github.com/golang/protobuf/proto" //nolint:staticcheck
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

var (
	_ = grpc.ClientStream(&webrtcClientStream{})
	// ErrDisconnected indicates that the channel underlying the client stream
	// has been closed, and the client is therefore disconnected.
	ErrDisconnected = errors.New("client disconnected; underlying channel closed")
)

// A webrtcClientStream is the high level gRPC streaming interface used for both
// unary and streaming call requests.
type webrtcClientStream struct {
	*webrtcBaseStream
	ctx              context.Context
	cancel           func()
	ch               *webrtcClientChannel
	headers          metadata.MD
	trailers         metadata.MD
	userCtx          context.Context
	headersReceived  chan struct{}
	trailersReceived bool

	// sendClose represents whether the send direction of the stream is closed. However,
	// control flow signals such as RST_STREAM will still be sent.
	sendClosed bool
}

// newWebRTCClientStream creates a gRPC stream from the given client channel with a
// unique identity in order to be able to recognize responses on a single
// underlying data channel.
func newWebRTCClientStream(
	ctx context.Context,
	channel *webrtcClientChannel,
	stream *webrtcpb.Stream,
	onDone func(id uint64),
	logger utils.ZapCompatibleLogger,
) (*webrtcClientStream, error) {
	// Assume that cancelation of the client channel's context means the peer
	// connection and base channel have both closed, and the client is
	// disconnected.
	//
	// We could rely on eventual reads/writes from/to the stream failing with a
	// `io.ErrClosedPipe`, but not checking the channel's context here will mean
	// we can create a stream _while_ the channel is closing/closed, which can
	// result in data races and undefined behavior. The caller to this function
	// is holding the channel mutex that's also acquired in the "close" path that
	// will cancel `channel.ctx`.
	if channel.ctx.Err() != nil {
		return nil, ErrDisconnected
	}

	ctx, cancel := utils.MergeContext(channel.ctx, ctx)
	bs := newWebRTCBaseStream(ctx, cancel, stream, onDone, logger)
	s := &webrtcClientStream{
		webrtcBaseStream: bs,
		ctx:              ctx,
		cancel:           cancel,
		ch:               channel,
		headersReceived:  make(chan struct{}),
	}
	channel.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer channel.activeBackgroundWorkers.Done()
		<-ctx.Done()
		if !s.webrtcBaseStream.Closed() {
			if err := s.resetStream(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				s.webrtcBaseStream.logger.Errorw("error resetting stream", "error", err)
			}
		}
	})
	return s, nil
}

// SendMsg is generally called by generated code. On error, SendMsg aborts
// the stream. If the error was generated by the client, the status is
// returned directly; otherwise, io.EOF is returned and the status of
// the stream may be discovered using RecvMsg.
//
// SendMsg blocks until:
//   - There is sufficient flow control to schedule m with the transport, or
//   - The stream is done, or
//   - The stream breaks.
//
// SendMsg does not wait until the message is received by the server. An
// untimely stream closure may result in lost messages. To ensure delivery,
// users should ensure the RPC completed successfully using RecvMsg.
//
// It is safe to have a goroutine calling SendMsg and another goroutine
// calling RecvMsg on the same stream at the same time, but it is undefined behavior
// to call SendMsg on the same stream in different goroutines.
func (s *webrtcClientStream) SendMsg(m interface{}) error {
	return s.writeMessage(m, false)
}

// Context returns the context for this stream.
//
// It should not be called until after Header or RecvMsg has returned. Once
// called, subsequent client-side retries are disabled.
func (s *webrtcClientStream) Context() context.Context {
	s.webrtcBaseStream.mu.Lock()
	defer s.webrtcBaseStream.mu.Unlock()
	if s.userCtx == nil {
		// be nice to misbehaving users
		return s.ctx
	}
	return s.userCtx
}

// Header returns the header metadata received from the server if there
// is any. It blocks if the metadata is not ready to read.
func (s *webrtcClientStream) Header() (metadata.MD, error) {
	select {
	case <-s.headersReceived:
		return s.headers, nil
	default:
	}

	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-s.headersReceived:
		return s.headers, nil
	}
}

// Trailer returns the trailer metadata from the server, if there is any.
// It must only be called after stream.CloseAndRecv has returned, or
// stream.Recv has returned a non-nil error (including io.EOF).
func (s *webrtcClientStream) Trailer() metadata.MD {
	s.webrtcBaseStream.mu.Lock()
	defer s.webrtcBaseStream.mu.Unlock()
	return s.trailers
}

// CloseSend closes the send direction of the stream. It closes the stream
// when non-nil error is met. It is also not safe to call CloseSend
// concurrently with SendMsg.
func (s *webrtcClientStream) CloseSend() error {
	return s.writeMessage(nil, true)
}

// checkWriteErrForStreamClose checks the given error to consider the stream for closure.
func checkWriteErrForStreamClose(err error) error {
	if err == nil || errors.Is(err, io.ErrClosedPipe) {
		// ignore because either no error or we expect to be closed down elsewhere
		// in the near future.
		return nil
	}
	return err
}

// resetStream cancels the stream and should always send a reset signal.
// It is also not safe to call concurrently with SendMsg.
func (s *webrtcClientStream) resetStream() (err error) {
	s.webrtcBaseStream.mu.Lock()
	defer s.webrtcBaseStream.mu.Unlock()

	s.sendClosed = true

	defer func() {
		s.webrtcBaseStream.closeWithError(checkWriteErrForStreamClose(err), false)
	}()
	return s.ch.writeReset(s.webrtcBaseStream.stream)
}

func (s *webrtcClientStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.close()
}

func (s *webrtcClientStream) close() {
	s.cancel()
	s.webrtcBaseStream.close()
}

// writeHeaders is assumed to be called by the client channel in a single goroutine not
// overlapping with any other write.
func (s *webrtcClientStream) writeHeaders(headers *webrtcpb.RequestHeaders) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	defer func() {
		if err := checkWriteErrForStreamClose(err); err != nil {
			s.webrtcBaseStream.closeWithError(err, false)
		}
	}()
	return s.ch.writeHeaders(s.webrtcBaseStream.stream, headers)
}

var maxRequestMessagePacketDataSize int

func init() {
	md, err := proto.Marshal(&webrtcpb.Request{
		Stream: &webrtcpb.Stream{
			Id: math.MaxUint64,
		},
		Type: &webrtcpb.Request_Message{
			Message: &webrtcpb.RequestMessage{
				HasMessage: true,
				PacketMessage: &webrtcpb.PacketMessage{
					Data: []byte{0x0},
					Eom:  true,
				},
				Eos: true,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	// maxRequestMessagePacketDataSize = maxDataChannelSize - max proto request wrapper size
	maxRequestMessagePacketDataSize = maxDataChannelSize - len(md)
}

func (s *webrtcClientStream) writeMessage(m interface{}, eos bool) (err error) {
	s.webrtcBaseStream.mu.RLock()
	if s.sendClosed {
		s.webrtcBaseStream.mu.RUnlock()
		return io.ErrClosedPipe
	}

	if eos {
		s.webrtcBaseStream.mu.RUnlock()
		s.webrtcBaseStream.mu.Lock()
		if s.sendClosed {
			s.webrtcBaseStream.mu.Unlock()
			return io.ErrClosedPipe
		}
		s.sendClosed = true
		defer s.webrtcBaseStream.mu.Unlock()
	} else {
		defer s.webrtcBaseStream.mu.RUnlock()
	}

	defer func() {
		if err := checkWriteErrForStreamClose(err); err != nil {
			s.webrtcBaseStream.closeWithError(err, false)
		}
	}()

	var data []byte
	if m != nil {
		if v1Msg, ok := m.(protov1.Message); ok {
			m = protov1.MessageV2(v1Msg)
		}
		data, err = proto.Marshal(m.(proto.Message))
		if err != nil {
			return
		}
	}

	if len(data) == 0 {
		return s.ch.writeMessage(s.webrtcBaseStream.stream, &webrtcpb.RequestMessage{
			HasMessage: m != nil, // maybe no data but a non-nil message
			PacketMessage: &webrtcpb.PacketMessage{
				Eom: true,
			},
			Eos: eos,
		})
	}

	for len(data) != 0 {
		amountToSend := maxRequestMessagePacketDataSize
		if len(data) < amountToSend {
			amountToSend = len(data)
		}
		packet := &webrtcpb.PacketMessage{
			Data: data[:amountToSend],
		}
		data = data[amountToSend:]
		if len(data) == 0 {
			packet.Eom = true
		}
		if err := s.ch.writeMessage(s.webrtcBaseStream.stream, &webrtcpb.RequestMessage{
			HasMessage:    m != nil, // maybe no data but a non-nil message
			PacketMessage: packet,
			Eos:           eos,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *webrtcClientStream) onResponse(resp *webrtcpb.Response) {
	switch r := resp.GetType().(type) {
	case *webrtcpb.Response_Headers:
		select {
		case <-s.headersReceived:
			s.webrtcBaseStream.closeWithError(errors.New("headers already received"), false)
			return
		default:
		}
		if s.trailersReceived {
			s.webrtcBaseStream.closeWithError(errors.New("headers received after trailers"), false)
			return
		}
		s.processHeaders(r.Headers)
	case *webrtcpb.Response_Message:
		select {
		case <-s.headersReceived:
		default:
			s.webrtcBaseStream.closeWithError(errors.New("headers not yet received"), false)
			return
		}
		if s.trailersReceived {
			s.webrtcBaseStream.closeWithError(errors.New("message received after trailers"), false)
			return
		}
		s.processMessage(r.Message)
	case *webrtcpb.Response_Trailers:
		s.processTrailers(r.Trailers)
	default:
		s.webrtcBaseStream.logger.Errorf("unknown response type %T", r)
	}
}

func (s *webrtcClientStream) processHeaders(headers *webrtcpb.ResponseHeaders) {
	s.webrtcBaseStream.mu.Lock()
	s.headers = metadataFromProto(headers.GetMetadata())
	s.userCtx = metadata.NewIncomingContext(s.ctx, s.headers)
	s.webrtcBaseStream.mu.Unlock()
	close(s.headersReceived)
}

func (s *webrtcClientStream) processMessage(msg *webrtcpb.ResponseMessage) {
	if s.trailersReceived {
		s.webrtcBaseStream.logger.Error("message received after trailers")
		return
	}
	data, eop := s.webrtcBaseStream.processMessage(msg.GetPacketMessage())
	if !eop {
		return
	}
	s.webrtcBaseStream.mu.Lock()
	if s.webrtcBaseStream.recvClosed.Load() {
		s.webrtcBaseStream.mu.Unlock()
		return
	}
	msgCh := s.webrtcBaseStream.msgCh
	s.webrtcBaseStream.activeSenders.Add(1)
	s.webrtcBaseStream.mu.Unlock()

	func() {
		defer s.webrtcBaseStream.activeSenders.Done()
		select {
		case msgCh <- data:
		case <-s.ctx.Done():
		}
	}()
}

func (s *webrtcClientStream) processTrailers(trailers *webrtcpb.ResponseTrailers) {
	s.webrtcBaseStream.mu.Lock()
	defer s.webrtcBaseStream.mu.Unlock()
	s.trailersReceived = true
	if trailers.GetMetadata() != nil {
		s.trailers = metadataFromProto(trailers.GetMetadata())
	}
	respStatus := status.FromProto(trailers.GetStatus())
	s.webrtcBaseStream.closeFromTrailers(respStatus.Err())
}
