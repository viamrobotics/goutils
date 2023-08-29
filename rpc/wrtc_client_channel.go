package rpc

import (
	"context"
	"errors"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edaniels/golog"
	grpc_logging "github.com/grpc-ecosystem/go-grpc-middleware/logging"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/pion/webrtc/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
)

var (
	// WebRTCMaxStreamCount is the max number of streams a channel can have.
	WebRTCMaxStreamCount = 256
	errWebRTCMaxStreams  = errors.New("stream limit hit")
)

// A webrtcClientChannel reflects the client end of a gRPC connection serviced over
// a WebRTC data channel.
type webrtcClientChannel struct {
	streamIDCounter uint64
	*webrtcBaseChannel
	mu                sync.Mutex
	streams           map[uint64]activeWebRTCClientStream
	unaryInterceptor  grpc.UnaryClientInterceptor
	streamInterceptor grpc.StreamClientInterceptor
}

type activeWebRTCClientStream struct {
	cs *webrtcClientStream
}

// newWebRTCClientChannel wraps the given WebRTC data channel to be used as the client end
// of a gRPC connection.
func newWebRTCClientChannel(
	peerConn *webrtc.PeerConnection,
	dataChannel *webrtc.DataChannel,
	logger golog.Logger,
	unaryInterceptor grpc.UnaryClientInterceptor,
	streamInterceptor grpc.StreamClientInterceptor,
) *webrtcClientChannel {
	base := newBaseChannel(
		context.Background(),
		peerConn,
		dataChannel,
		nil,
		logger,
	)
	ch := &webrtcClientChannel{
		webrtcBaseChannel: base,
		streams:           map[uint64]activeWebRTCClientStream{},
		unaryInterceptor:  unaryInterceptor,
		streamInterceptor: streamInterceptor,
	}
	dataChannel.OnMessage(ch.onChannelMessage)
	return ch
}

// Close closes all streams and the underlying channel.
func (ch *webrtcClientChannel) Close() error {
	ch.mu.Lock()
	streamsToClose := make(map[uint64]activeWebRTCClientStream, len(ch.streams))
	for k, v := range ch.streams {
		streamsToClose[k] = v
	}
	ch.mu.Unlock()
	for _, s := range streamsToClose {
		s.cs.Close()
	}
	return ch.webrtcBaseChannel.Close()
}

// Invoke sends the RPC request on the wire and returns after response is
// received.  This is typically called by generated code.
//
// All errors returned by Invoke are compatible with the status package.
func (ch *webrtcClientChannel) Invoke(
	ctx context.Context,
	method string,
	args interface{},
	reply interface{},
	opts ...grpc.CallOption,
) error {
	fields := newClientLoggerFields(method)
	startTime := time.Now()
	err := ch.invokeWithInterceptor(ctx, method, args, reply, opts...)
	newCtx := ctxzap.ToContext(ctx, ch.webrtcBaseChannel.logger.Desugar().With(fields...))
	logFinalClientLine(newCtx, startTime, err, "finished client unary call")
	return err
}

func (ch *webrtcClientChannel) invokeWithInterceptor(
	ctx context.Context,
	method string,
	args, reply interface{},
	opts ...grpc.CallOption,
) error {
	if ch.unaryInterceptor == nil {
		return ch.invoke(ctx, method, args, reply, opts...)
	}

	// change signature of invoker to be compatible with grpc unary interceptor
	invoker := func(ctx context.Context, method string, req, reply interface{}, _ *grpc.ClientConn, opts ...grpc.CallOption) error {
		return ch.invoke(ctx, method, req, reply, opts...)
	}
	return ch.unaryInterceptor(ctx, method, args, reply, nil, invoker, opts...)
}

func (ch *webrtcClientChannel) invoke(
	ctx context.Context,
	method string,
	args, reply interface{},
	opts ...grpc.CallOption,
) error {
	clientStream, err := ch.newStream(ctx, ch.nextStreamID())
	if err != nil {
		return err
	}
	defer func() {
		clientStream.mu.Lock()
		defer clientStream.mu.Unlock()

		for _, opt := range opts {
			switch optV := opt.(type) {
			case grpc.HeaderCallOption:
				if clientStream.headers != nil {
					*optV.HeaderAddr = clientStream.headers.Copy()
				}
			case grpc.TrailerCallOption:
				if clientStream.trailers != nil {
					*optV.TrailerAddr = clientStream.trailers.Copy()
				}
			default:
				clientStream.webrtcBaseStream.logger.Errorf("do not know how to handle call option %T", opt)
			}
		}
	}()

	if err := clientStream.writeHeaders(makeRequestHeaders(ctx, method)); err != nil {
		return err
	}

	if err := clientStream.writeMessage(args, true); err != nil {
		return err
	}

	return clientStream.RecvMsg(reply)
}

// NewStream creates a new Stream for the client side. This is typically
// called by generated code. ctx is used for the lifetime of the stream.
//
// To ensure resources are not leaked due to the stream returned, one of the following
// actions must be performed:
//
//  1. Call Close on the ClientConn.
//  2. Cancel the context provided.
//  3. Call RecvMsg until a non-nil error is returned. A protobuf-generated
//     client-streaming RPC, for instance, might use the helper function
//     CloseAndRecv (note that CloseSend does not Recv, therefore is not
//     guaranteed to release all resources).
//  4. Receive a non-nil, non-io.EOF error from Header or SendMsg.
//
// If none of the above happen, a goroutine and a context will be leaked, and grpc
// will not call the optionally-configured stats handler with a stats.End message.
func (ch *webrtcClientChannel) NewStream(
	ctx context.Context,
	desc *grpc.StreamDesc,
	method string,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	fields := newClientLoggerFields(method)
	startTime := time.Now()
	clientStream, err := ch.streamWithInterceptor(ctx, method)
	newCtx := ctxzap.ToContext(ctx, ch.webrtcBaseChannel.logger.Desugar().With(fields...))
	logFinalClientLine(newCtx, startTime, err, "finished client streaming call")
	return clientStream, err
}

func (ch *webrtcClientChannel) streamWithInterceptor(ctx context.Context, method string) (grpc.ClientStream, error) {
	if ch.streamInterceptor == nil {
		return ch.newClientStream(ctx, method)
	}

	// change signature of streamer to be compatible with grpc stream interceptor
	streamer := func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return ch.newClientStream(ctx, method)
	}
	return ch.streamInterceptor(ctx, nil, nil, method, streamer)
}

func (ch *webrtcClientChannel) newClientStream(ctx context.Context, method string) (grpc.ClientStream, error) {
	clientStream, err := ch.newStream(ctx, ch.nextStreamID())
	if err != nil {
		return nil, err
	}

	if err := clientStream.writeHeaders(makeRequestHeaders(ctx, method)); err != nil {
		return nil, err
	}

	return clientStream, nil
}

func makeRequestHeaders(ctx context.Context, method string) *webrtcpb.RequestHeaders {
	headersMD, _ := metadata.FromOutgoingContext(ctx)
	var timeout time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	return &webrtcpb.RequestHeaders{
		Method:   method,
		Metadata: metadataToProto(headersMD),
		Timeout:  durationpb.New(timeout),
	}
}

func (ch *webrtcClientChannel) nextStreamID() *webrtcpb.Stream {
	return &webrtcpb.Stream{
		Id: atomic.AddUint64(&ch.streamIDCounter, 1),
	}
}

func (ch *webrtcClientChannel) removeStreamByID(id uint64) {
	ch.mu.Lock()
	delete(ch.streams, id)
	ch.mu.Unlock()
}

func (ch *webrtcClientChannel) newStream(
	ctx context.Context,
	stream *webrtcpb.Stream,
) (*webrtcClientStream, error) {
	id := stream.Id
	ch.mu.Lock()
	activeStream, ok := ch.streams[id]
	if !ok {
		if len(ch.streams) == WebRTCMaxStreamCount {
			ch.mu.Unlock()
			return nil, errWebRTCMaxStreams
		}
		clientStream := newWebRTCClientStream(
			ctx,
			ch,
			stream,
			ch.removeStreamByID,
			ch.webrtcBaseChannel.logger.With("id", id),
		)
		activeStream = activeWebRTCClientStream{clientStream}
		ch.streams[id] = activeStream
	}
	ch.mu.Unlock()
	return activeStream.cs, nil
}

func (ch *webrtcClientChannel) onChannelMessage(msg webrtc.DataChannelMessage) {
	resp := &webrtcpb.Response{}
	err := proto.Unmarshal(msg.Data, resp)
	if err != nil {
		ch.webrtcBaseChannel.logger.Errorw("error unmarshaling message; discarding", "error", err)
		return
	}

	stream := resp.Stream
	if stream == nil {
		ch.webrtcBaseChannel.logger.Debug("no stream id; discarding")
		return
	}

	id := stream.Id
	ch.mu.Lock()
	activeStream, ok := ch.streams[id]
	if !ok {
		ch.webrtcBaseChannel.logger.Debugw("no stream for id; discarding", "id", id)
		ch.mu.Unlock()
		return
	}
	ch.mu.Unlock()

	activeStream.cs.onResponse(resp)
}

func (ch *webrtcClientChannel) writeHeaders(stream *webrtcpb.Stream, headers *webrtcpb.RequestHeaders) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Request{
		Stream: stream,
		Type: &webrtcpb.Request_Headers{
			Headers: headers,
		},
	})
}

func (ch *webrtcClientChannel) writeMessage(stream *webrtcpb.Stream, msg *webrtcpb.RequestMessage) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Request{
		Stream: stream,
		Type: &webrtcpb.Request_Message{
			Message: msg,
		},
	})
}

func (ch *webrtcClientChannel) writeReset(stream *webrtcpb.Stream) error {
	return ch.webrtcBaseChannel.write(&webrtcpb.Request{
		Stream: stream,
		Type: &webrtcpb.Request_RstStream{
			RstStream: true,
		},
	})
}

// taken from
// https://github.com/grpc-ecosystem/go-grpc-middleware/blob/560829fc74fcf9a69b7ab01d484f8b8961dc734b/logging/zap/client_interceptors.go

func logFinalClientLine(ctx context.Context, startTime time.Time, err error, msg string) {
	code := grpc_logging.DefaultErrorToCode(err)
	level := grpc_zap.DefaultCodeToLevel(code)
	if err == nil {
		level = zap.DebugLevel
	}
	duration := grpc_zap.DefaultDurationToField(time.Since(startTime))
	grpc_zap.DefaultMessageProducer(ctx, msg, level, code, err, duration)
}

var (
	clientField = zap.String("span.kind", "client")
	systemField = zap.String("system", "grpc")
)

func newClientLoggerFields(fullMethodString string) []zapcore.Field {
	service := path.Dir(fullMethodString)[1:]
	method := path.Base(fullMethodString)
	return []zapcore.Field{
		systemField,
		clientField,
		zap.String("grpc.service", service),
		zap.String("grpc.method", method),
	}
}
