package rpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	pbstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"go.viam.com/utils"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	"go.viam.com/utils/testutils"
)

func TestWebRTCClientChannel(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer utils.UncheckedErrorFunc(pc1.GracefulClose)
	defer utils.UncheckedErrorFunc(pc2.GracefulClose)

	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	someStatus, _ := status.FromError(errors.New("ouch"))

	someStatusMd, err := proto.Marshal(someStatus.Proto())
	test.That(t, err, test.ShouldBeNil)

	someOtherStatus, _ := status.FromError(errors.New("ouchie"))

	someOtherStatusMd, err := proto.Marshal(someOtherStatus.Proto())
	test.That(t, err, test.ShouldBeNil)

	var expectedMessagesMu sync.Mutex
	expectedMessages := []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method:  "thing",
					Timeout: durationpb.New(0),
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: true,
				},
			},
		},
	}

	expectedTrailer := metadata.MD{}

	var rejected sync.WaitGroup
	var hasTimeout, errorAfterMessage, trailersOnly, rejectAll bool
	idCounter := uint64(1)
	serverCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		expectedMessagesMu.Lock()
		defer expectedMessagesMu.Unlock()
		if rejectAll {
			rejected.Done()
			return
		}
		test.That(t, serverCh.write(someStatus.Proto()), test.ShouldBeNil) // unexpected proto
		req := &webrtcpb.Request{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)
		test.That(t, expectedMessages, test.ShouldNotBeEmpty)
		expected := expectedMessages[0]
		if hasTimeout {
			if header, ok := req.GetType().(*webrtcpb.Request_Headers); ok {
				test.That(t, header.Headers.GetTimeout().AsDuration(), test.ShouldNotBeZeroValue)
				header.Headers.Timeout = nil
			}
		}
		logger.Debugw("comparing", "expected", expected, "actual", req)
		test.That(t, proto.Equal(expected, req), test.ShouldBeTrue)
		expectedMessages = expectedMessages[1:]
		if len(expectedMessages) == 0 {
			if !trailersOnly {
				test.That(t, serverCh.write(&webrtcpb.Response{
					Stream: &webrtcpb.Stream{Id: idCounter},
					Type:   &webrtcpb.Response_Headers{},
				}), test.ShouldBeNil)
			}

			if !trailersOnly && !errorAfterMessage {
				test.That(t, serverCh.write(&webrtcpb.Response{
					Stream: &webrtcpb.Stream{Id: idCounter},
					Type: &webrtcpb.Response_Message{
						Message: &webrtcpb.ResponseMessage{
							PacketMessage: &webrtcpb.PacketMessage{
								Data: someOtherStatusMd,
								Eom:  true,
							},
						},
					},
				}), test.ShouldBeNil)
			}

			respStatus := ErrorToStatus(nil)
			if errorAfterMessage {
				respStatus = status.New(codes.InvalidArgument, "whoops")
			}
			test.That(t, serverCh.write(&webrtcpb.Response{
				Stream: &webrtcpb.Stream{Id: idCounter},
				Type: &webrtcpb.Response_Trailers{
					Trailers: &webrtcpb.ResponseTrailers{
						Status:   respStatus.Proto(),
						Metadata: metadataToProto(expectedTrailer),
					},
				},
			}), test.ShouldBeNil)
			idCounter++

			// Ignore bad streams
			test.That(t, serverCh.write(&webrtcpb.Response{
				Stream: &webrtcpb.Stream{Id: 1000},
				Type:   &webrtcpb.Response_Headers{},
			}), test.ShouldBeNil)
			test.That(t, serverCh.write(&webrtcpb.Response{
				Stream: &webrtcpb.Stream{Id: 1000},
				Type: &webrtcpb.Response_Message{
					Message: &webrtcpb.ResponseMessage{
						PacketMessage: &webrtcpb.PacketMessage{
							Data: someOtherStatusMd,
							Eom:  true,
						},
					},
				},
			}), test.ShouldBeNil)
			test.That(t, serverCh.write(&webrtcpb.Response{
				Stream: &webrtcpb.Stream{Id: 1000},
				Type: &webrtcpb.Response_Trailers{
					Trailers: &webrtcpb.ResponseTrailers{
						Status:   ErrorToStatus(nil).Proto(),
						Metadata: metadataToProto(expectedTrailer),
					},
				},
			}), test.ShouldBeNil)
		}
	})

	var respStatus pbstatus.Status
	test.That(t, clientCh.Invoke(context.Background(), "thing", someStatus.Proto(), &respStatus), test.ShouldBeNil)
	test.That(t, status.FromProto(&respStatus).Code(), test.ShouldEqual, someOtherStatus.Code())
	test.That(t, status.FromProto(&respStatus).Message(), test.ShouldEqual, someOtherStatus.Message())
	test.That(t, status.FromProto(&respStatus).Details(), test.ShouldResemble, someOtherStatus.Details())

	expectedMessagesMu.Lock()
	hasTimeout = true
	expectedMessages = []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 2},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method: "thing",
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 2},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: true,
				},
			},
		},
	}
	expectedMessagesMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	test.That(t, clientCh.Invoke(ctx, "thing", someStatus.Proto(), &respStatus), test.ShouldBeNil)
	test.That(t, status.FromProto(&respStatus).Code(), test.ShouldEqual, someOtherStatus.Code())
	test.That(t, status.FromProto(&respStatus).Message(), test.ShouldEqual, someOtherStatus.Message())
	test.That(t, status.FromProto(&respStatus).Details(), test.ShouldResemble, someOtherStatus.Details())

	expectedMessagesMu.Lock()
	hasTimeout = false
	expectedMessages = []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 3},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method:  "thing",
					Timeout: durationpb.New(0),
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 3},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: true,
				},
			},
		},
	}
	expectedMessagesMu.Unlock()

	test.That(t, clientCh.Invoke(context.Background(), "thing", someStatus.Proto(), &respStatus), test.ShouldBeNil)
	test.That(t, status.FromProto(&respStatus).Code(), test.ShouldEqual, someOtherStatus.Code())
	test.That(t, status.FromProto(&respStatus).Message(), test.ShouldEqual, someOtherStatus.Message())
	test.That(t, status.FromProto(&respStatus).Details(), test.ShouldResemble, someOtherStatus.Details())

	expectedMessagesMu.Lock()
	errorAfterMessage = true
	expectedMessages = []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 4},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method:  "thing",
					Timeout: durationpb.New(0),
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 4},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: true,
				},
			},
		},
	}
	expectedMessagesMu.Unlock()

	err = clientCh.Invoke(context.Background(), "thing", someStatus.Proto(), &respStatus)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, status.Convert(err).Code(), test.ShouldEqual, codes.InvalidArgument)
	test.That(t, status.Convert(err).Message(), test.ShouldEqual, "whoops")

	expectedMessagesMu.Lock()
	trailersOnly = true
	errorAfterMessage = true
	expectedMessages = []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 5},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method:  "thing",
					Timeout: durationpb.New(0),
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 5},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: true,
				},
			},
		},
	}
	expectedMessagesMu.Unlock()

	err = clientCh.Invoke(context.Background(), "thing", someStatus.Proto(), &respStatus)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, status.Convert(err).Code(), test.ShouldEqual, codes.InvalidArgument)
	test.That(t, status.Convert(err).Message(), test.ShouldEqual, "whoops")

	expectedMessagesMu.Lock()
	rejectAll = true
	expectedMessagesMu.Unlock()

	rejected.Add(2)
	clientErr := make(chan error)
	go func() {
		clientStream, err := clientCh.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, "thing")
		if err != nil {
			clientErr <- err
			return
		}
		if err != nil {
			clientErr <- clientStream.SendMsg(someStatus.Proto())
			return
		}

		clientStream.SendMsg(someStatus.Proto())
		clientErr <- clientStream.RecvMsg(&respStatus)
	}()
	rejected.Wait()
	// client channel cancellation will send a RST_STREAM signal for non-closed streams
	rejected.Add(1)
	test.That(t, clientCh.Close(), test.ShouldBeNil)
	test.That(t, <-clientErr, test.ShouldEqual, context.Canceled)
}

func TestWebRTCClientChannelResetStream(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer pc1.GracefulClose()
	defer pc2.GracefulClose()

	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	someStatus, _ := status.FromError(errors.New("ouch"))

	someStatusMd, err := proto.Marshal(someStatus.Proto())
	test.That(t, err, test.ShouldBeNil)

	var expectedMessagesMu sync.Mutex
	expectedMessages := []*webrtcpb.Request{
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Request_Headers{
				Headers: &webrtcpb.RequestHeaders{
					Method:  "thing",
					Timeout: durationpb.New(0),
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Request_Message{
				Message: &webrtcpb.RequestMessage{
					HasMessage: true,
					PacketMessage: &webrtcpb.PacketMessage{
						Data: someStatusMd,
						Eom:  true,
					},
					Eos: false,
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Request_RstStream{
				RstStream: true,
			},
		},
	}

	resetCh := make(chan struct{})

	var ctx context.Context
	var cancel func()
	serverCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		expectedMessagesMu.Lock()
		defer expectedMessagesMu.Unlock()

		req := &webrtcpb.Request{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)

		expected := expectedMessages[0]
		expectedMessages = expectedMessages[1:]

		logger.Debugw("comparing", "expected", expected, "actual", req)
		test.That(t, proto.Equal(expected, req), test.ShouldBeTrue)

		if len(expectedMessages) == 1 {
			cancel()
		}

		if len(expectedMessages) == 0 {
			close(resetCh)
		}
	})
	expectedMessagesMu.Lock()
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	expectedMessagesMu.Unlock()

	var respStatus pbstatus.Status
	clientStream, err := clientCh.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true}, "thing")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, clientStream.SendMsg(someStatus.Proto()), test.ShouldBeNil)
	err = clientStream.RecvMsg(&respStatus)
	test.That(t, err, test.ShouldBeError, context.Canceled)
	test.That(t, &respStatus, test.ShouldResemble, &pbstatus.Status{})

	<-resetCh
}

func TestWebRTCClientChannelWithInterceptor(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	clientPC, serverPC, clientDataChannel, serverDataChannel := setupWebRTCPeers(t)
	defer clientPC.GracefulClose()
	defer serverPC.GracefulClose()

	// Set up a server handler for the client test. The first client request will be unary and an
	// interceptor will append "some/value". The second will be a streaming request and append
	// "other/thing". The interceptors also record the methods being sent.
	var interceptedUnaryMethods []string
	unaryInterceptor := func(
		ctx context.Context,
		method string,
		req,
		reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		interceptedUnaryMethods = append(interceptedUnaryMethods, method)
		ctx = metadata.AppendToOutgoingContext(ctx, "some", "value")
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	var interceptedStreamMethods []string
	streamInterceptor := func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		interceptedStreamMethods = append(interceptedStreamMethods, method)
		ctx = metadata.AppendToOutgoingContext(ctx, "other", "thing")
		return streamer(ctx, desc, cc, method, opts...)
	}

	clientCh := newWebRTCClientChannel(
		clientPC,
		clientDataChannel,
		nil,
		utils.Sublogger(logger, "client"),
		unaryInterceptor,
		streamInterceptor)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), serverPC, serverDataChannel, nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	var wg sync.WaitGroup
	wg.Add(4)

	var headerCounter int

	// Every set of headers bumps the `headerCounter`. This mimics unique/auto-incrementing grpc
	// stream ids for every new request.
	//
	// The unary and stream requests will both have the same response. First the headers will be
	// sent, followed by an empty message with EOM=true.
	serverCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		req := &webrtcpb.Request{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)
		if r, ok := req.GetType().(*webrtcpb.Request_Headers); ok {
			test.That(t, r.Headers.GetMetadata(), test.ShouldNotBeNil)

			var key, value string
			switch headerCounter {
			case 0:
				key = "some"
				value = "value"
			case 1:
				key = "other"
				value = "thing"
			default:
				panic("header counter too high")
			}
			test.That(t, r.Headers.GetMetadata().GetMd()[key], test.ShouldNotBeNil)
			test.That(t, r.Headers.GetMetadata().GetMd()[key].GetValues(), test.ShouldHaveLength, 1)
			test.That(t, r.Headers.GetMetadata().GetMd()[key].GetValues()[0], test.ShouldEqual, value)
			headerCounter++
		}

		test.That(t, serverCh.write(&webrtcpb.Response{
			Stream: &webrtcpb.Stream{Id: uint64(headerCounter)},
			Type:   &webrtcpb.Response_Headers{},
		}), test.ShouldBeNil)

		test.That(t, serverCh.write(&webrtcpb.Response{
			Stream: &webrtcpb.Stream{Id: uint64(headerCounter)},
			Type: &webrtcpb.Response_Message{
				Message: &webrtcpb.ResponseMessage{
					PacketMessage: &webrtcpb.PacketMessage{
						Data: []byte{},
						Eom:  true,
					},
				},
			},
		}), test.ShouldBeNil)
		wg.Done()
	})

	var unaryMsg interface{}
	var respStatus pbstatus.Status
	logger.Info("Sending unary")
	test.That(t, clientCh.Invoke(context.Background(), "a unary", unaryMsg, &respStatus), test.ShouldBeNil)
	test.That(t, interceptedUnaryMethods, test.ShouldHaveLength, 1)
	test.That(t, interceptedUnaryMethods, test.ShouldContain, "a unary")

	var streamMsg interface{}
	logger.Info("Sending stream")
	clientStream, err := clientCh.NewStream(context.Background(), &grpc.StreamDesc{}, "a stream")
	test.That(t, err, test.ShouldBeNil)
	err = clientStream.SendMsg(streamMsg)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, interceptedStreamMethods, test.ShouldHaveLength, 1)
	test.That(t, interceptedStreamMethods, test.ShouldContain, "a stream")

	wg.Wait()
}

func TestWebRTCClientChannelCanStopStreamRecvMsg(t *testing.T) {
	// Regression test for RSDK-4473.
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer pc1.GracefulClose()
	defer pc2.GracefulClose()

	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	someStatus, _ := status.FromError(errors.New("wowza"))

	serverFinished := make(chan struct{})
	once := sync.Once{}
	serverCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Do not do anything on message reception server-side (no response to
		// mimic a network outage or lost packet).
		//
		// NOTE(benjirewis): two messages will usually be received from the client:
		// the first specifying the start of the "thing" stream, and the second
		// carrying the "wowza" status. Use a sync.Once to make sure channel is
		// closed only once.
		once.Do(func() { close(serverFinished) })
	})

	clientStream, err := clientCh.NewStream(context.Background(),
		&grpc.StreamDesc{ClientStreams: true}, "thing")
	test.That(t, err, test.ShouldBeNil)

	// Send a message to the server and delay slightly to allow server to receive
	// the message and close serverFinished.
	test.That(t, clientStream.SendMsg(someStatus.Proto()), test.ShouldBeNil)
	time.Sleep(50 * time.Millisecond)

	// Close client peer connection before receiving a client message. Assert
	// that RecvMsg does not hang.
	test.That(t, pc1.GracefulClose(), test.ShouldBeNil)
	var respStatus pbstatus.Status
	err = clientStream.RecvMsg(&respStatus)
	test.That(t, err, test.ShouldNotBeNil)

	// The error returned from RecvMsg can be either a context canceled error
	// or an EOF error. The former is returned when RecvMsg catches the context
	// error first and reports it. The latter is returned when the goroutine
	// created in newWebRTCClientStream catches the context error first and
	// attempts to reset the stream.
	test.That(t, err.Error(), test.ShouldBeIn, context.Canceled.Error(), io.EOF.Error())
	test.That(t, &respStatus, test.ShouldResemble, &pbstatus.Status{})

	<-serverFinished
}

func TestClientStreamCancel(t *testing.T) {
	// Tests that clients can cancel server streams over WebRTC.
	// 1. We set up a server with a stream endpoint and call it with a client.
	// 2. After the initial request, the client closes its send side of the stream
	//    (replicating what happens in a server-side streaming scenario).
	// 3. 3 messages are sent from the server. After the 3rd message, the client stream is cancelled.
	// 4. The server should receive a RST_STREAM message and cancel the server context.
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)
	defer pc1.GracefulClose()
	defer pc2.GracefulClose()

	clientCh := newWebRTCClientChannel(pc1, dc1, nil, utils.Sublogger(logger, "client"), nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()

	server := newWebRTCServer(logger)
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "service_name",
		Streams: []grpc.StreamDesc{
			{
				StreamName: "stream_name",
				Handler: grpc.StreamHandler(func(srv any, stream grpc.ServerStream) error {
					pongStatus, _ := status.FromError(errors.New("pong"))

					for i := 0; i < 10; i++ {
						if stream.Context().Err() != nil {
							return stream.Context().Err()
						}
						stream.SendMsg(pongStatus.Proto())

						// Using channels is not enough ensure that the client reset
						// went through in time.
						// Sleep here to let client process messages and send reset.
						time.Sleep(10 * time.Millisecond)
					}
					// Failure as this means the context was never cancelled.
					t.Fail()
					return nil
				}),
				ServerStreams: true,
				ClientStreams: false,
			},
		},
	}, nil)

	serverCh := newWebRTCServerChannel(server, pc2, dc2, nil, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	clientStream, err := clientCh.NewStream(
		streamCtx,
		&grpc.StreamDesc{
			StreamName:    "client_stream",
			ServerStreams: true,
			ClientStreams: false,
		},
		"/service_name/stream_name",
	)
	test.That(t, err, test.ShouldBeNil)

	someStatus, _ := status.FromError(errors.New("wowza"))
	err = clientStream.SendMsg(someStatus.Proto())
	test.That(t, err, test.ShouldBeNil)

	err = clientStream.CloseSend()
	test.That(t, err, test.ShouldBeNil)

	for i := 0; i < 3; i++ {
		var respStatus pbstatus.Status
		err = clientStream.RecvMsg(&respStatus)
		test.That(t, err, test.ShouldBeNil)
	}
	cancelStream()
}
