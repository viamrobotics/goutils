package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
	"go.viam.com/test"
	pbstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	"go.viam.com/utils/testutils"
)

func TestWebRTCClientChannel(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)

	clientCh := newWebRTCClientChannel(pc1, dc1, logger, nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer func() {
		test.That(t, serverCh.Close(), test.ShouldBeNil)
	}()

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
			if header, ok := req.Type.(*webrtcpb.Request_Headers); ok {
				test.That(t, header.Headers.Timeout.AsDuration(), test.ShouldNotBeZeroValue)
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
		clientErr <- clientCh.Invoke(context.Background(), "thing", someStatus.Proto(), &respStatus)
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

	clientCh := newWebRTCClientChannel(pc1, dc1, logger, nil, nil)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer func() {
		test.That(t, serverCh.Close(), test.ShouldBeNil)
	}()

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
					Eos: true,
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
	err = clientCh.Invoke(ctx, "thing", someStatus.Proto(), &respStatus)
	test.That(t, err, test.ShouldBeError, context.Canceled)
	test.That(t, &respStatus, test.ShouldResemble, &pbstatus.Status{})

	<-resetCh
}

func TestWebRTCClientChannelWithInterceptor(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	pc1, pc2, dc1, dc2 := setupWebRTCPeers(t)

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

	clientCh := newWebRTCClientChannel(pc1, dc1, logger, unaryInterceptor, streamInterceptor)
	defer func() {
		test.That(t, clientCh.Close(), test.ShouldBeNil)
	}()
	serverCh := newBaseChannel(context.Background(), pc2, dc2, nil, logger)
	defer func() {
		test.That(t, serverCh.Close(), test.ShouldBeNil)
	}()

	<-clientCh.Ready()
	<-serverCh.Ready()

	var wg sync.WaitGroup
	wg.Add(4)

	idCounter := uint64(1)
	var headerCounter int
	serverCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		req := &webrtcpb.Request{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)
		if r, ok := req.Type.(*webrtcpb.Request_Headers); ok {
			test.That(t, r.Headers.Metadata, test.ShouldNotBeNil)

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
			test.That(t, r.Headers.Metadata.Md[key], test.ShouldNotBeNil)
			test.That(t, r.Headers.Metadata.Md[key].Values, test.ShouldHaveLength, 1)
			test.That(t, r.Headers.Metadata.Md[key].Values[0], test.ShouldEqual, value)
			headerCounter++
		}

		test.That(t, serverCh.write(&webrtcpb.Response{
			Stream: &webrtcpb.Stream{Id: idCounter},
			Type:   &webrtcpb.Response_Headers{},
		}), test.ShouldBeNil)

		test.That(t, serverCh.write(&webrtcpb.Response{
			Stream: &webrtcpb.Stream{Id: idCounter},
			Type: &webrtcpb.Response_Message{
				Message: &webrtcpb.ResponseMessage{
					PacketMessage: &webrtcpb.PacketMessage{
						Data: []byte{},
						Eom:  true,
					},
				},
			},
		}), test.ShouldBeNil)
		idCounter++
		wg.Done()
	})

	var unaryMsg interface{}
	var respStatus pbstatus.Status
	test.That(t, clientCh.Invoke(context.Background(), "a unary", unaryMsg, &respStatus), test.ShouldBeNil)
	test.That(t, interceptedUnaryMethods, test.ShouldHaveLength, 1)
	test.That(t, interceptedUnaryMethods, test.ShouldContain, "a unary")

	var streamMsg interface{}
	clientStream, err := clientCh.NewStream(context.Background(), &grpc.StreamDesc{}, "a stream")
	test.That(t, err, test.ShouldBeNil)
	err = clientStream.SendMsg(streamMsg)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, interceptedStreamMethods, test.ShouldHaveLength, 1)
	test.That(t, interceptedStreamMethods, test.ShouldContain, "a stream")

	wg.Wait()
}
