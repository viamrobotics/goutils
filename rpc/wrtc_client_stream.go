package rpc

import (
	"context"
	"errors"
	"sync"

	"github.com/edaniels/golog"
	//nolint:staticcheck
	protov1 "github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

var _ = grpc.ClientStream(&webrtcClientStream{})

// A webrtcClientStream is the high level gRPC streaming interface used for both
// unary and streaming call requests.
type webrtcClientStream struct {
	*webrtcBaseStream
	ctx                     context.Context
	cancel                  func()
	mu                      sync.Mutex
	activeBackgroundWorkers sync.WaitGroup
	ch                      *webrtcClientChannel
	headers                 metadata.MD
	trailers                metadata.MD
	userCtx                 context.Context
	headersReceived         chan struct{}
	trailersReceived        bool
}

// newWebRTCClientStream creates a gRPC stream from the given client channel with a
// unique identity in order to be able to recognize responses on a single
// underlying data channel.
func newWebRTCClientStream(
	ctx context.Context,
	channel *webrtcClientChannel,
	stream *webrtcpb.Stream,
	onDone func(id uint64),
	logger golog.Logger,
) *webrtcClientStream {
	ctx, cancel := context.WithCancel(ctx)
	bs := newWebRTCBaseStream(ctx, cancel, stream, onDone, logger)
	s := &webrtcClientStream{
		webrtcBaseStream: bs,
		ctx:              ctx,
		cancel:           cancel,
		ch:               channel,
		headersReceived:  make(chan struct{}),
	}
	s.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer s.activeBackgroundWorkers.Done()
		<-ctx.Done()
		if !s.webrtcBaseStream.Closed() {
			if err := s.ResetStream(); err != nil {
				s.webrtcBaseStream.logger.Errorw("error resetting stream", "error", err)
			}
		}
	})
	return s
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
// calling RecvMsg on the same stream at the same time, but it is not safe
// to call SendMsg on the same stream in different goroutines. It is also
// not safe to call CloseSend concurrently with SendMsg or ResetStream.
func (s *webrtcClientStream) SendMsg(m interface{}) error {
	return s.writeMessage(m, false)
}

// Context returns the context for this stream.
//
// It should not be called until after Header or RecvMsg has returned. Once
// called, subsequent client-side retries are disabled.
func (s *webrtcClientStream) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trailers
}

// CloseSend closes the send direction of the stream. It closes the stream
// when non-nil error is met. It is also not safe to call CloseSend
// concurrently with SendMsg or ResetStream.
func (s *webrtcClientStream) CloseSend() error {
	return s.writeMessage(nil, true)
}

// ResetStream cancels the stream and sends a reset signal.
// It is also not safe to call ResetStream
// concurrently with SendMsg or CloseSend.
func (s *webrtcClientStream) ResetStream() (err error) {
	defer func() {
		if err != nil {
			s.webrtcBaseStream.closeWithRecvError(err)
		}
	}()
	return s.ch.writeReset(s.webrtcBaseStream.stream)
}

// Close closes the stream.
func (s *webrtcClientStream) Close() error {
	s.cancel()
	s.activeBackgroundWorkers.Wait()
	return nil
}

func (s *webrtcClientStream) writeHeaders(headers *webrtcpb.RequestHeaders) (err error) {
	defer func() {
		if err != nil {
			s.webrtcBaseStream.closeWithRecvError(err)
		}
	}()
	return s.ch.writeHeaders(s.webrtcBaseStream.stream, headers)
}

var maxRequestMessagePacketDataSize int

func init() {
	md, err := proto.Marshal(&webrtcpb.Request{
		Stream: &webrtcpb.Stream{
			Id: 1,
		},
		Type: &webrtcpb.Request_Message{
			Message: &webrtcpb.RequestMessage{
				PacketMessage: &webrtcpb.PacketMessage{Eom: true},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	// max msg size - packet size - msg type size - proto padding (?)
	maxRequestMessagePacketDataSize = maxDataChannelSize - len(md) - 1
}

func (s *webrtcClientStream) writeMessage(m interface{}, eos bool) (err error) {
	defer func() {
		if err != nil {
			s.webrtcBaseStream.closeWithRecvError(err)
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
	switch r := resp.Type.(type) {
	case *webrtcpb.Response_Headers:
		select {
		case <-s.headersReceived:
			s.webrtcBaseStream.closeWithRecvError(errors.New("headers already received"))
			return
		default:
		}
		if s.trailersReceived {
			s.webrtcBaseStream.closeWithRecvError(errors.New("headers received after trailers"))
			return
		}
		s.processHeaders(r.Headers)
	case *webrtcpb.Response_Message:
		select {
		case <-s.headersReceived:
		default:
			s.webrtcBaseStream.closeWithRecvError(errors.New("headers not yet received"))
			return
		}
		if s.trailersReceived {
			s.webrtcBaseStream.closeWithRecvError(errors.New("message received after trailers"))
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
	s.headers = metadataFromProto(headers.Metadata)
	s.mu.Lock()
	s.userCtx = metadata.NewIncomingContext(s.ctx, s.headers)
	s.mu.Unlock()
	close(s.headersReceived)
}

func (s *webrtcClientStream) processMessage(msg *webrtcpb.ResponseMessage) {
	if s.trailersReceived {
		s.webrtcBaseStream.logger.Error("message received after trailers")
		return
	}
	data, eop := s.webrtcBaseStream.processMessage(msg.PacketMessage)
	if !eop {
		return
	}
	s.webrtcBaseStream.mu.Lock()
	if s.webrtcBaseStream.recvClosed {
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
	s.trailersReceived = true
	respStatus := status.FromProto(trailers.Status)
	s.webrtcBaseStream.closeWithRecvError(respStatus.Err())
}
