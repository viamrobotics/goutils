package rpc

import (
	"context"
	"fmt"
	"io"
	"math"
	"sync/atomic"

	"github.com/edaniels/golog"
	protov1 "github.com/golang/protobuf/proto" //nolint:staticcheck
	"github.com/pion/sctp"
	"github.com/pkg/errors"
	"go.uber.org/multierr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

var _ = grpc.ServerStream(&webrtcServerStream{})

// ErrIllegalHeaderWrite indicates that setting header is illegal because of
// the state of the stream.
var ErrIllegalHeaderWrite = errors.New("transport: the stream is done or WriteHeader was already called")

// A webrtcServerStream is the high level gRPC streaming interface used for handling both
// unary and streaming call responses.
type webrtcServerStream struct {
	*webrtcBaseStream
	ch              *webrtcServerChannel
	method          string
	headersWritten  atomic.Bool
	headersReceived bool
	header          metadata.MD
	trailer         metadata.MD
	sendClosed      atomic.Bool
}

// newWebRTCServerStream creates a gRPC stream from the given server channel with a
// unique identity in order to be able to recognize requests on a single
// underlying data channel.
func newWebRTCServerStream(
	ctx context.Context,
	cancelCtx func(),
	method string,
	channel *webrtcServerChannel,
	stream *webrtcpb.Stream,
	onDone func(id uint64),
	logger golog.Logger,
) *webrtcServerStream {
	bs := newWebRTCBaseStream(ctx, cancelCtx, stream, onDone, logger)
	s := &webrtcServerStream{
		webrtcBaseStream: bs,
		ch:               channel,
		method:           method,
	}
	return s
}

// Method returns the method for the stream.
func (s *webrtcServerStream) Method() string {
	return s.method
}

// SetHeader sets the header metadata. It may be called multiple times.
// When call multiple times, all the provided metadata will be merged.
// All the metadata will be sent out when one of the following happens:
//   - webrtcServerStream.SendHeader() is called;
//   - The first response is sent out;
//   - An RPC status is sent out (error or success).
func (s *webrtcServerStream) SetHeader(header metadata.MD) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.headersWritten.Load() {
		return errors.WithStack(ErrIllegalHeaderWrite)
	}
	if s.header == nil {
		s.header = header
	} else if header != nil {
		s.header = metadata.Join(s.header, header)
	}
	return nil
}

// SendHeader sends the header metadata.
// The provided md and headers set by SetHeader() will be sent.
// It fails if called multiple times.
func (s *webrtcServerStream) SendHeader(header metadata.MD) error {
	if err := s.SetHeader(header); err != nil {
		return err
	}
	return s.writeHeaders()
}

// SetTrailer sets the trailer metadata which will be sent with the RPC status.
// When called more than once, all the provided metadata will be merged.
func (s *webrtcServerStream) SetTrailer(trailer metadata.MD) {
	if s.trailer == nil {
		s.trailer = trailer
	} else if trailer != nil {
		s.trailer = metadata.Join(s.trailer, trailer)
	}
}

type serverTransportStream struct {
	s *webrtcServerStream
}

func (s serverTransportStream) Method() string {
	return s.s.Method()
}

func (s serverTransportStream) SetHeader(header metadata.MD) error {
	return s.s.SetHeader(header)
}

func (s serverTransportStream) SendHeader(header metadata.MD) error {
	return s.s.SendHeader(header)
}

func (s serverTransportStream) SetTrailer(trailer metadata.MD) error {
	s.s.SetTrailer(trailer)
	return nil
}

var maxResponseMessagePacketDataSize int

func init() {
	md, err := proto.Marshal(&webrtcpb.Response{
		Stream: &webrtcpb.Stream{
			Id: math.MaxUint64,
		},
		Type: &webrtcpb.Response_Message{
			Message: &webrtcpb.ResponseMessage{
				PacketMessage: &webrtcpb.PacketMessage{
					Data: []byte{0x0},
					Eom:  true,
				},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	// maxResponseMessagePacketDataSize = maxDataChannelSize - max proto response wrapper size
	maxResponseMessagePacketDataSize = maxDataChannelSize - len(md)
}

// SendMsg sends a message. On error, SendMsg aborts the stream and the
// error is returned directly.
//
// SendMsg blocks until:
//   - There is sufficient flow control to schedule m with the transport, or
//   - The stream is done, or
//   - The stream breaks.
//
// SendMsg does not wait until the message is received by the client. An
// untimely stream closure may result in lost messages.
//
// It is safe to have a goroutine calling SendMsg and another goroutine
// calling RecvMsg on the same stream at the same time, but it is undefined behavior
// to call SendMsg on the same stream in different goroutines.
func (s *webrtcServerStream) SendMsg(m interface{}) (err error) {
	if s.sendClosed.Load() {
		return io.ErrClosedPipe
	}

	s.webrtcBaseStream.mu.RLock()
	defer func() {
		// `closeWithSendError` takes locks the `webrtcBaseStream.mu` RWLock in writer mode. Release
		// the lock before proceeding.
		s.webrtcBaseStream.mu.RUnlock()
		if err != nil {
			err = multierr.Combine(err, s.closeWithSendError(err))
		}
	}()

	if err := s.writeHeaders(); err != nil {
		return err
	}
	if v1Msg, ok := m.(protov1.Message); ok {
		m = protov1.MessageV2(v1Msg)
	}
	data, err := proto.Marshal(m.(proto.Message))
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return s.ch.writeMessage(s.stream, &webrtcpb.ResponseMessage{
			PacketMessage: &webrtcpb.PacketMessage{
				Eom: true,
			},
		})
	}

	for len(data) != 0 {
		amountToSend := maxResponseMessagePacketDataSize
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
		if err := s.ch.writeMessage(s.stream, &webrtcpb.ResponseMessage{
			PacketMessage: packet,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *webrtcServerStream) onRequest(request *webrtcpb.Request) {
	switch r := request.Type.(type) {
	case *webrtcpb.Request_Headers:
		if s.headersReceived {
			if err := s.closeWithSendError(status.Error(codes.InvalidArgument, "headers already received")); err != nil {
				s.logger.Errorw("error closing", "error", err)
			}
			return
		}
		s.processHeaders(r.Headers)
	case *webrtcpb.Request_Message:
		if !s.headersReceived {
			if err := s.closeWithSendError(status.Error(codes.InvalidArgument, "headers not yet received")); err != nil {
				s.logger.Errorw("error closing", "error", err)
			}
			return
		}
		s.processMessage(r.Message)
	case *webrtcpb.Request_RstStream:
		if err := s.closeWithSendError(status.Error(codes.Canceled, "request cancelled")); err != nil {
			s.logger.Errorw("error closing", "error", err)
		}
		return
	default:
		if err := s.closeWithSendError(status.Error(codes.InvalidArgument, fmt.Sprintf("unknown request type %T", r))); err != nil {
			s.logger.Errorw("error closing", "error", err)
		}
	}
}

func isContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	if utils.FilterOutError(err, context.Canceled) == nil {
		return true
	}
	gStatus, isGRPCErr := status.FromError(err)
	return isGRPCErr && gStatus.Code() == codes.Canceled
}

func (s *webrtcServerStream) processHeaders(headers *webrtcpb.RequestHeaders) {
	s.logger = s.logger.With("method", headers.Method)

	handlerFunc, ok := s.ch.server.handler(headers.Method)
	if !ok {
		if s.ch.server.unknownStreamDesc != nil {
			handlerFunc = s.ch.server.streamHandler(s.ch.server, headers.Method, *s.ch.server.unknownStreamDesc)
		} else {
			if err := s.closeWithSendError(status.Error(codes.Unimplemented, codes.Unimplemented.String())); err != nil {
				s.logger.Errorw("error closing", "error", err)
			}
			return
		}
	}

	// take a ticket
	select {
	case <-s.ch.server.ctx.Done():
		if err := s.closeWithSendError(status.FromContextError(s.ch.server.ctx.Err()).Err()); err != nil {
			s.logger.Errorw("error closing", "error", err)
		}
		return
	case s.ch.server.callTickets <- struct{}{}:
	default:
		if err := s.closeWithSendError(status.Error(codes.ResourceExhausted, "too many in-flight requests")); err != nil {
			s.logger.Errorw("error closing", "error", err)
		}
		return
	}

	s.headersReceived = true
	s.ch.server.activeBackgroundWorkers.Add(1)
	utils.PanicCapturingGo(func() {
		defer func() {
			<-s.ch.server.callTickets // return a ticket
		}()
		defer s.ch.server.activeBackgroundWorkers.Done()
		if err := handlerFunc(s); err != nil {
			if errors.Is(err, io.ErrClosedPipe) || isContextCanceled(err) {
				return
			}
			s.logger.Errorw("error calling handler", "error", err)
		}
	})
}

func (s *webrtcServerStream) processMessage(msg *webrtcpb.RequestMessage) {
	if s.recvClosed.Load() {
		s.logger.Error("message received after EOS")
		return
	}
	if msg.HasMessage {
		if msg.PacketMessage == nil {
			s.closeWithError(errors.New("expected RequestMessage.PacketMessgae to not be nil but it was"), false)
			return
		}
		data, eop := s.webrtcBaseStream.processMessage(msg.PacketMessage)
		if !eop {
			return
		}
		s.webrtcBaseStream.mu.Lock()
		if s.recvClosed.Load() {
			s.webrtcBaseStream.mu.Unlock()
			return
		}
		msgCh := s.msgCh
		s.webrtcBaseStream.activeSenders.Add(1)
		s.webrtcBaseStream.mu.Unlock()

		func() {
			defer s.webrtcBaseStream.activeSenders.Done()
			select {
			case msgCh <- data:
			case <-s.ctx.Done():
				return
			}
		}()
	}
	if msg.Eos {
		s.CloseRecv()
	}
}

// Must not be called with the `s.webrtcBaseStream.mu` mutex held.
func (s *webrtcServerStream) closeWithSendError(err error) (writeErr error) {
	if !s.sendClosed.CompareAndSwap(false, true) {
		return nil
	}
	defer func() {
		if writeErr == nil || errors.Is(writeErr, sctp.ErrStreamClosed) {
			writeErr = nil
		}
	}()
	defer func() {
		s.webrtcBaseStream.mu.Lock()
		defer s.webrtcBaseStream.mu.Unlock()
		s.close()
	}()
	if err != nil && (errors.Is(err, io.ErrClosedPipe)) {
		return nil
	}
	chClosed, chClosedReason := s.ch.Closed()
	if s.Closed() || chClosed {
		if errors.Is(chClosedReason, errDataChannelClosed) &&
			isContextCanceled(err) {
			return nil
		}
		return errors.Wrap(err, "close called multiple times with error")
	}
	if err := s.writeHeaders(); err != nil {
		return err
	}
	var respStatus *status.Status
	if err == nil {
		respStatus = ErrorToStatus(s.ctx.Err())
	} else {
		respStatus = ErrorToStatus(err)
	}
	return s.ch.writeTrailers(s.stream, &webrtcpb.ResponseTrailers{
		Status:   respStatus.Proto(),
		Metadata: metadataToProto(s.trailer),
	})
}

func (s *webrtcServerStream) writeHeaders() error {
	if !s.headersWritten.CompareAndSwap(false, true) {
		return nil
	}
	protoHeaders := metadataToProto(s.header)
	return s.ch.writeHeaders(s.stream, &webrtcpb.ResponseHeaders{
		Metadata: protoHeaders,
	})
}

// ErrorToStatus converts an error to a gRPC status. A nil
// error becomes a successful status.
func ErrorToStatus(err error) *status.Status {
	respStatus := status.FromContextError(err)
	if respStatus.Code() == codes.Unknown {
		respStatus = status.Convert(err)
		if respStatus == nil {
			respStatus = status.New(codes.OK, "")
		}
	}
	return respStatus
}
