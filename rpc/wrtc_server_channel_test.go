package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/edaniels/golog"
	"github.com/viamrobotics/webrtc/v3"
	"go.viam.com/test"
	"go.viam.com/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	"go.viam.com/utils/testutils"
)

func TestWebRTCServerChannel(t *testing.T) {
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
	defer server.Stop()
	// use signaling server just as some random service to test against.
	// It helps that it is in our package.
	queue := newMemoryWebRTCCallQueueTest(logger)
	defer queue.Close()
	signalServer := NewWebRTCSignalingServer(queue, nil, utils.Sublogger(logger, "client"), defaultHeartbeatInterval)
	defer signalServer.Close()
	server.RegisterService(
		&webrtcpb.SignalingService_ServiceDesc,
		signalServer,
	)

	serverCh := newWebRTCServerChannel(server, pc2, dc2, []string{"one", "two"}, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	someStatus, _ := status.FromError(errors.New("ouch"))

	// bad data
	test.That(t, clientCh.write(someStatus.Proto()), test.ShouldBeNil)  // unexpected proto
	test.That(t, clientCh.write(&webrtcpb.Request{}), test.ShouldBeNil) // bad request
	test.That(t, clientCh.writeMessage(&webrtcpb.Stream{                // message before headers
		Id: 1,
	}, &webrtcpb.RequestMessage{}), test.ShouldBeNil)

	var expectedMessagesMu sync.Mutex
	expectedMessages := []*webrtcpb.Response{
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Response_Headers{
				Headers: &webrtcpb.ResponseHeaders{},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 1},
			Type: &webrtcpb.Response_Trailers{
				Trailers: &webrtcpb.ResponseTrailers{
					Status: status.New(codes.Unimplemented, codes.Unimplemented.String()).Proto(),
				},
			},
		},
	}
	messagesRead := make(chan struct{})
	clientCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		expectedMessagesMu.Lock()
		defer expectedMessagesMu.Unlock()
		req := &webrtcpb.Response{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)
		logger.Debugw("got message", "actual", req)
		test.That(t, expectedMessages, test.ShouldNotBeEmpty)
		expected := expectedMessages[0]
		logger.Debugw("comparing", "expected", expected, "actual", req)
		test.That(t, proto.Equal(expected, req), test.ShouldBeTrue)
		expectedMessages = expectedMessages[1:]
		if len(expectedMessages) == 0 {
			close(messagesRead)
		}
	})

	test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{ // no method
		Id: 1,
	}, &webrtcpb.RequestHeaders{}), test.ShouldBeNil)

	<-messagesRead

	expectedMessagesMu.Lock()
	expectedMessages = []*webrtcpb.Response{
		{
			Stream: &webrtcpb.Stream{Id: 2},
			Type: &webrtcpb.Response_Headers{
				Headers: &webrtcpb.ResponseHeaders{},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 2},
			Type: &webrtcpb.Response_Trailers{
				Trailers: &webrtcpb.ResponseTrailers{
					Status: status.New(codes.InvalidArgument, "headers already received").Proto(),
				},
			},
		},
	}
	messagesRead = make(chan struct{})
	expectedMessagesMu.Unlock()

	test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
		Id: 2,
	}, &webrtcpb.RequestHeaders{
		Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
	}), test.ShouldBeNil)

	test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
		Id: 2,
	}, &webrtcpb.RequestHeaders{
		Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
	}), test.ShouldBeNil)

	<-messagesRead

	respMd, err := proto.Marshal(&webrtcpb.CallResponse{
		Uuid: "insecure-uuid-1",
		Stage: &webrtcpb.CallResponse_Init{
			Init: &webrtcpb.CallResponseInitStage{
				Sdp: "world",
			},
		},
	})
	test.That(t, err, test.ShouldBeNil)

	expectedMessagesMu.Lock()
	expectedMessages = []*webrtcpb.Response{
		{
			Stream: &webrtcpb.Stream{Id: 3},
			Type: &webrtcpb.Response_Headers{
				Headers: &webrtcpb.ResponseHeaders{},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 3},
			Type: &webrtcpb.Response_Message{
				Message: &webrtcpb.ResponseMessage{
					PacketMessage: &webrtcpb.PacketMessage{
						Data: respMd,
						Eom:  true,
					},
				},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 3},
			Type: &webrtcpb.Response_Trailers{
				Trailers: &webrtcpb.ResponseTrailers{
					Status: ErrorToStatus(nil).Proto(),
				},
			},
		},
	}
	messagesRead = make(chan struct{})
	expectedMessagesMu.Unlock()

	test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
		Id: 3,
	}, &webrtcpb.RequestHeaders{
		Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
		Metadata: metadataToProto(metadata.MD{
			"rpc-host": []string{"yeehaw"},
		}),
	}), test.ShouldBeNil)

	reqMd, err := proto.Marshal(&webrtcpb.CallRequest{Sdp: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, clientCh.writeMessage(&webrtcpb.Stream{
		Id: 3,
	}, &webrtcpb.RequestMessage{
		HasMessage: true,
		PacketMessage: &webrtcpb.PacketMessage{
			Data: reqMd,
			Eom:  true,
		},
		Eos: true,
	}), test.ShouldBeNil)

	offer, err := signalServer.callQueue.RecvOffer(context.Background(), []string{"yeehaw"})
	test.That(t, err, test.ShouldBeNil)
	answererSDP := "world"
	test.That(t, offer.AnswererRespond(context.Background(), WebRTCCallAnswer{InitialSDP: &answererSDP}), test.ShouldBeNil)
	test.That(t, offer.AnswererDone(context.Background()), test.ShouldBeNil)

	<-messagesRead

	expectedMessagesMu.Lock()
	expectedMessages = []*webrtcpb.Response{
		{
			Stream: &webrtcpb.Stream{Id: 4},
			Type: &webrtcpb.Response_Headers{
				Headers: &webrtcpb.ResponseHeaders{},
			},
		},
		{
			Stream: &webrtcpb.Stream{Id: 4},
			Type: &webrtcpb.Response_Trailers{
				Trailers: &webrtcpb.ResponseTrailers{
					Status: ErrorToStatus(errors.New("error from answerer: ohno")).Proto(),
				},
			},
		},
	}
	messagesRead = make(chan struct{})
	expectedMessagesMu.Unlock()

	test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
		Id: 4,
	}, &webrtcpb.RequestHeaders{
		Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
		Metadata: metadataToProto(metadata.MD{
			"rpc-host": []string{"yeehaw"},
		}),
	}), test.ShouldBeNil)

	test.That(t, clientCh.writeMessage(&webrtcpb.Stream{
		Id: 4,
	}, &webrtcpb.RequestMessage{
		HasMessage: true,
		PacketMessage: &webrtcpb.PacketMessage{
			Data: reqMd,
			Eom:  true,
		},
		Eos: true,
	}), test.ShouldBeNil)

	offer, err = signalServer.callQueue.RecvOffer(context.Background(), []string{"yeehaw"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, offer.AnswererRespond(context.Background(), WebRTCCallAnswer{Err: errors.New("ohno")}), test.ShouldBeNil)

	<-messagesRead
}

func TestWebRTCServerChannelResetStream(t *testing.T) {
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
	defer server.Stop()
	// use signaling server just as some random service to test against.
	// It helps that it is in our package.
	queue := newMemoryWebRTCCallQueueTest(logger)
	defer queue.Close()
	signalServer := NewWebRTCSignalingServer(queue, nil, utils.Sublogger(logger, "client"), defaultHeartbeatInterval)
	defer signalServer.Close()
	server.RegisterService(
		&webrtcpb.SignalingService_ServiceDesc,
		signalServer,
	)

	serverCh := newWebRTCServerChannel(server, pc2, dc2, []string{"one", "two"}, logger)
	defer serverCh.Close()

	<-clientCh.Ready()
	<-serverCh.Ready()

	var expectedMessagesMu sync.Mutex
	var expectedMessages []*webrtcpb.Response
	var messagesRead chan struct{}
	clientCh.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		expectedMessagesMu.Lock()
		defer expectedMessagesMu.Unlock()
		req := &webrtcpb.Response{}
		test.That(t, proto.Unmarshal(msg.Data, req), test.ShouldBeNil)
		logger.Debugw("got message", "actual", req)
		test.That(t, expectedMessages, test.ShouldNotBeEmpty)
		expected := expectedMessages[0]
		test.That(t, proto.Equal(expected, req), test.ShouldBeTrue)
		expectedMessages = expectedMessages[1:]
		if len(expectedMessages) == 0 {
			close(messagesRead)
		}
	})
	t.Run("reset stream before message sent", func(t *testing.T) {
		expectedMessagesMu.Lock()
		expectedMessages = []*webrtcpb.Response{
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Headers{
					Headers: &webrtcpb.ResponseHeaders{},
				},
			},
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Trailers{
					Trailers: &webrtcpb.ResponseTrailers{
						Status: ErrorToStatus(status.Error(codes.Canceled, "request cancelled")).Proto(),
					},
				},
			},
		}
		messagesRead = make(chan struct{})
		expectedMessagesMu.Unlock()

		test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
			Id: 1,
		}, &webrtcpb.RequestHeaders{
			Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
			Metadata: metadataToProto(metadata.MD{
				"rpc-host": []string{"yeehaw"},
			}),
		}), test.ShouldBeNil)
		test.That(t, clientCh.writeReset(&webrtcpb.Stream{Id: 1}), test.ShouldBeNil)
		<-messagesRead
	})
	t.Run("reset stream in middle of message", func(t *testing.T) {
		expectedMessagesMu.Lock()
		expectedMessages = []*webrtcpb.Response{
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Headers{
					Headers: &webrtcpb.ResponseHeaders{},
				},
			},
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Trailers{
					Trailers: &webrtcpb.ResponseTrailers{
						Status: ErrorToStatus(status.Error(codes.Canceled, "request cancelled")).Proto(),
					},
				},
			},
		}
		messagesRead = make(chan struct{})
		expectedMessagesMu.Unlock()

		test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
			Id: 1,
		}, &webrtcpb.RequestHeaders{
			Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
			Metadata: metadataToProto(metadata.MD{
				"rpc-host": []string{"yeehaw"},
			}),
		}), test.ShouldBeNil)

		reqMd, err := proto.Marshal(&webrtcpb.CallRequest{Sdp: "hello"})
		test.That(t, err, test.ShouldBeNil)

		test.That(t, clientCh.writeMessage(&webrtcpb.Stream{
			Id: 1,
		}, &webrtcpb.RequestMessage{
			HasMessage: true,
			PacketMessage: &webrtcpb.PacketMessage{
				Data: reqMd,
				Eom:  false,
			},
			Eos: false,
		}), test.ShouldBeNil)
		test.That(t, clientCh.writeReset(&webrtcpb.Stream{Id: 1}), test.ShouldBeNil)
		<-messagesRead
	})
	t.Run("reset stream after message", func(t *testing.T) {
		expectedMessagesMu.Lock()
		respMd, err := proto.Marshal(&webrtcpb.CallResponse{
			Uuid: "insecure-uuid-1",
			Stage: &webrtcpb.CallResponse_Init{
				Init: &webrtcpb.CallResponseInitStage{
					Sdp: "world",
				},
			},
		})
		test.That(t, err, test.ShouldBeNil)
		expectedMessages = []*webrtcpb.Response{
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Headers{
					Headers: &webrtcpb.ResponseHeaders{},
				},
			},
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Message{
					Message: &webrtcpb.ResponseMessage{
						PacketMessage: &webrtcpb.PacketMessage{
							Data: respMd,
							Eom:  true,
						},
					},
				},
			},
			{
				Stream: &webrtcpb.Stream{Id: 1},
				Type: &webrtcpb.Response_Trailers{
					Trailers: &webrtcpb.ResponseTrailers{
						Status: ErrorToStatus(status.Error(codes.Canceled, "request cancelled")).Proto(),
					},
				},
			},
		}
		messagesRead = make(chan struct{})
		expectedMessagesMu.Unlock()

		test.That(t, clientCh.writeHeaders(&webrtcpb.Stream{
			Id: 1,
		}, &webrtcpb.RequestHeaders{
			Method: "/proto.rpc.webrtc.v1.SignalingService/Call",
			Metadata: metadataToProto(metadata.MD{
				"rpc-host": []string{"yeehaw"},
			}),
		}), test.ShouldBeNil)

		reqMd, err := proto.Marshal(&webrtcpb.CallRequest{Sdp: "hello"})
		test.That(t, err, test.ShouldBeNil)

		test.That(t, clientCh.writeMessage(&webrtcpb.Stream{
			Id: 1,
		}, &webrtcpb.RequestMessage{
			HasMessage: true,
			PacketMessage: &webrtcpb.PacketMessage{
				Data: reqMd,
				Eom:  true,
			},
			Eos: true,
		}), test.ShouldBeNil)

		offer, err := signalServer.callQueue.RecvOffer(context.Background(), []string{"yeehaw"})
		test.That(t, err, test.ShouldBeNil)
		answererSDP := "world"
		test.That(t, offer.AnswererRespond(context.Background(), WebRTCCallAnswer{InitialSDP: &answererSDP}), test.ShouldBeNil)
		test.That(t, clientCh.writeReset(&webrtcpb.Stream{Id: 1}), test.ShouldBeNil)

		<-messagesRead
	})
}
