package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"github.com/viamrobotics/webrtc/v3"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebRTCClientServerWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientServer(t, signalingCallQueue, logger)
}

func TestWebRTCClientServerWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {}, nil)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientServer(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientServer(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	hosts := []string{"yeehaw", "woahthere"}
	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		hosts,
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	answerer.Start()
	waitForAnswererOnline(context.Background(), t, hosts, signalingCallQueue)

	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			for _, tc := range []bool{true, false} {
				t.Run(fmt.Sprintf("with trickle disabled %t", tc), func(t *testing.T) {
					cc, err := DialWebRTC(
						context.Background(),
						grpcListener.Addr().String(),
						host,
						logger,
						WithWebRTCOptions(DialWebRTCOptions{
							SignalingInsecure: true,
							DisableTrickleICE: tc,
						}),
					)
					test.That(t, err, test.ShouldBeNil)
					defer func() {
						test.That(t, cc.Close(), test.ShouldBeNil)
					}()

					echoClient := echopb.NewEchoServiceClient(cc)
					resp, err := echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: "hello"})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, resp.GetMessage(), test.ShouldEqual, "hello")

					// big message
					bigZ := strings.Repeat("z", 1<<18)
					resp, err = echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: bigZ})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, resp.GetMessage(), test.ShouldEqual, bigZ)
				})
			}
		})
	}

	webrtcServer.Stop()
	answerer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestWebRTCClientDialCancelWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialCancel(t, signalingCallQueue, logger)
}

func TestWebRTCClientDialCancelWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {}, nil)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialCancel(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientDialCancel(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	grpcConn, err := DialDirectGRPC(context.Background(), grpcListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer grpcConn.Close()

	signalingClient := webrtcpb.NewSignalingServiceClient(grpcConn)
	md := metadata.New(nil)
	host := primitive.NewObjectID().Hex()
	md.Append(RPCHostMetadataField, host)
	answerCtx := metadata.NewOutgoingContext(context.Background(), md)
	answerClient, err := signalingClient.Answer(answerCtx)
	test.That(t, err, test.ShouldBeNil)
	waitForAnswererOnline(context.Background(), t, []string{host}, signalingCallQueue)

	cancelCtx, cancel := context.WithCancel(context.Background())

	dialErrCh := make(chan error)
	go func() {
		_, err := DialWebRTC(
			cancelCtx,
			grpcListener.Addr().String(),
			host,
			logger,
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingInsecure: true,
			}),
		)
		dialErrCh <- err
	}()

	_, err = answerClient.Recv()
	test.That(t, err, test.ShouldBeNil)

	cancel()

	dialErr := <-dialErrCh
	test.That(t, dialErr.Error(), test.ShouldContainSubstring, context.Canceled.Error())

	offerUpdate, err := answerClient.Recv()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, offerUpdate.GetError().String(), test.ShouldContainSubstring, context.Canceled.Error())

	webrtcServer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestWebRTCClientDialReflectAnswererErrorWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialReflectAnswererError(t, signalingCallQueue, logger)
}

func TestWebRTCClientDialReflectAnswererErrorWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {}, nil)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialReflectAnswererError(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientDialReflectAnswererError(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	grpcConn, err := DialDirectGRPC(context.Background(), grpcListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer grpcConn.Close()

	signalingClient := webrtcpb.NewSignalingServiceClient(grpcConn)
	md := metadata.New(nil)
	host := primitive.NewObjectID().Hex()
	md.Append(RPCHostMetadataField, host)
	answerCtx := metadata.NewOutgoingContext(context.Background(), md)
	answerClient, err := signalingClient.Answer(answerCtx)
	test.That(t, err, test.ShouldBeNil)
	waitForAnswererOnline(context.Background(), t, []string{host}, signalingCallQueue)

	dialErrCh := make(chan error)
	go func() {
		_, err := DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			logger,
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingInsecure: true,
			}),
		)
		dialErrCh <- err
	}()

	offer, err := answerClient.Recv()
	test.That(t, err, test.ShouldBeNil)

	test.That(t, answerClient.Send(&webrtcpb.AnswerResponse{
		Uuid: offer.GetUuid(),
		Stage: &webrtcpb.AnswerResponse_Init{
			Init: &webrtcpb.AnswerResponseInitStage{
				Sdp: "hehehee",
			},
		},
	}), test.ShouldBeNil)

	dialErr := <-dialErrCh
	test.That(t, dialErr.Error(), test.ShouldContainSubstring, "illegal")

	offerUpdate, err := answerClient.Recv()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, offerUpdate.GetError().String(), test.ShouldContainSubstring, "illegal")

	webrtcServer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestWebRTCClientDialConcurrentWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialConcurrent(t, signalingCallQueue, logger)
}

func TestWebRTCClientDialConcurrentWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {}, nil)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialConcurrent(t, signalingCallQueue, logger)
}

// this is a good integration test against mongoDBWebRTCCallQueue
//
//nolint:thelper
func testWebRTCClientDialConcurrent(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	logger = utils.Sublogger(logger, "test")

	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, utils.Sublogger(logger, "signaling-server"),
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	grpcConn, err := DialDirectGRPC(context.Background(), grpcListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer grpcConn.Close()

	signalingClient := webrtcpb.NewSignalingServiceClient(grpcConn)
	md := metadata.New(nil)
	host := primitive.NewObjectID().Hex()
	md.Append(RPCHostMetadataField, host)
	answerCtx := metadata.NewOutgoingContext(context.Background(), md)
	answerClient1, err := signalingClient.Answer(answerCtx)
	test.That(t, err, test.ShouldBeNil)
	answerClient2, err := signalingClient.Answer(answerCtx)
	test.That(t, err, test.ShouldBeNil)
	waitForAnswererOnline(context.Background(), t, []string{host}, signalingCallQueue)

	dialErrCh := make(chan error, 2)
	go func() {
		logger.Info("starting dial 1")
		cc, err := DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			utils.Sublogger(logger, "dial1"),
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingInsecure: true,
			}),
		)
		if cc != nil {
			cc.Close()
		}
		dialErrCh <- err
	}()
	go func() {
		logger.Info("starting dial 2")
		cc, err := DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			utils.Sublogger(logger, "dial2"),
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingInsecure: true,
			}),
		)
		if cc != nil {
			cc.Close()
		}
		dialErrCh <- err
	}()

	logger.Info("answer client 1 is receiving")
	offer1, err := answerClient1.Recv()
	test.That(t, err, test.ShouldBeNil)

	logger.Info("answer client 2 is receiving")
	offer2, err := answerClient2.Recv()
	test.That(t, err, test.ShouldBeNil)

	test.That(t, offer1.GetUuid(), test.ShouldNotEqual, offer2.GetUuid())

	test.That(t, answerClient1.Send(&webrtcpb.AnswerResponse{
		Uuid: offer1.GetUuid(),
		Stage: &webrtcpb.AnswerResponse_Init{
			Init: &webrtcpb.AnswerResponseInitStage{
				Sdp: "hehehee",
			},
		},
	}), test.ShouldBeNil)

	dialErr := <-dialErrCh
	test.That(t, dialErr.Error(), test.ShouldContainSubstring, "illegal")

	offerUpdate, err := answerClient1.Recv()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, offerUpdate.GetUuid(), test.ShouldEqual, offer1.GetUuid())
	test.That(t, offerUpdate.GetError().String(), test.ShouldContainSubstring, "illegal")

	test.That(t, answerClient2.Send(&webrtcpb.AnswerResponse{
		Uuid: offer2.GetUuid(),
		Stage: &webrtcpb.AnswerResponse_Error{
			Error: &webrtcpb.AnswerResponseErrorStage{
				Status: status.New(codes.DataLoss, "whoops").Proto(),
			},
		},
	}), test.ShouldBeNil)

	dialErr = <-dialErrCh
	test.That(t, dialErr.Error(), test.ShouldContainSubstring, "whoops")

	_, err = answerClient2.Recv()
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, context.Canceled.Error())

	webrtcServer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestWebRTCClientAnswerConcurrentWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientAnswerConcurrent(t, signalingCallQueue, logger)
}

func TestWebRTCClientAnswerConcurrentWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {}, nil)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientAnswerConcurrent(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientAnswerConcurrent(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	host := primitive.NewObjectID().Hex()

	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		[]string{host},
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	answerer.Start()
	waitForAnswererOnline(context.Background(), t, []string{host}, signalingCallQueue)

	grpcConn, err := DialDirectGRPC(context.Background(), grpcListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer grpcConn.Close()
	signalingClient := webrtcpb.NewSignalingServiceClient(grpcConn)
	md := metadata.New(nil)
	md.Append(RPCHostMetadataField, host)
	callCtx := metadata.NewOutgoingContext(context.Background(), md)

	pc1, _, err := newPeerConnectionForClient(context.Background(), webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)
	defer pc1.GracefulClose()

	encodedSDP1, err := EncodeSDP(pc1.LocalDescription())
	test.That(t, err, test.ShouldBeNil)

	pc2, _, err := newPeerConnectionForClient(context.Background(), webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)
	defer pc2.GracefulClose()

	encodedSDP2, err := EncodeSDP(pc2.LocalDescription())
	test.That(t, err, test.ShouldBeNil)

	callClient1, err := signalingClient.Call(callCtx, &webrtcpb.CallRequest{
		Sdp: encodedSDP1,
	})
	test.That(t, err, test.ShouldBeNil)
	callClient2, err := signalingClient.Call(callCtx, &webrtcpb.CallRequest{
		Sdp: encodedSDP2,
	})
	test.That(t, err, test.ShouldBeNil)

	answer1, err := callClient1.Recv()
	test.That(t, err, test.ShouldBeNil)

	answer2, err := callClient2.Recv()
	test.That(t, err, test.ShouldBeNil)

	test.That(t, answer1.GetUuid(), test.ShouldNotEqual, answer2.GetUuid())

	webrtcServer.Stop()
	answerer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}

func TestWebRTCClientSubsequentStreams(t *testing.T) {
	logger := golog.NewTestLogger(t)
	serverOpts := []ServerOption{
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable: true,
		}),
		WithUnauthenticated(),
	}
	rpcServer, err := NewServer(
		logger,
		serverOpts...,
	)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&echopb.EchoService_ServiceDesc,
		&es,
		echopb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	rtcConn, err := DialWebRTC(
		context.Background(),
		listener.Addr().String(),
		rpcServer.InstanceNames()[0],
		logger,
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)

	client := echopb.NewEchoServiceClient(rtcConn)

	msg := "hello"
	echoResp, err := client.Echo(context.Background(), &echopb.EchoRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, msg)

	echoResp, err = client.Echo(context.Background(), &echopb.EchoRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, msg)

	echoClient, err := client.EchoMultiple(context.Background(), &echopb.EchoMultipleRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	var echoMultiResp echopb.EchoMultipleResponse
	for i := 0; i < 5; i++ {
		test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeNil)
		test.That(t, echoMultiResp.GetMessage(), test.ShouldEqual, msg[i:i+1])
	}
	test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeError, io.EOF)

	echoClient, err = client.EchoMultiple(context.Background(), &echopb.EchoMultipleRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	for i := 0; i < 5; i++ {
		test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeNil)
		test.That(t, echoMultiResp.GetMessage(), test.ShouldEqual, msg[i:i+1])
	}
	test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeError, io.EOF)

	test.That(t, rtcConn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

// TestWebRTCClientStreamHeaderRace is a deterministic unit regression test for
// the race in webrtcClientStream.Header().
//
// Scenario reproduced here (one of the two ways the bug manifests):
//   - The Header() goroutine parks in the blocking select while headersReceived
//     is still open.
//   - ctx is then canceled BEFORE headersReceived closes. Go's select
//     implementation unblocks the parked goroutine via the ctx.Done() case.
//   - headersReceived is then closed, but the goroutine has already been
//     dequeued from its wait list and will run the ctx.Done() branch.
//   - Old code: returned nil, ctx.Err() ("context canceled") even though
//     headers had since arrived.
//   - Fixed code: the ctx.Done() branch performs a non-blocking fallback check
//     on headersReceived; finding it closed, it returns the headers instead.
//
// Why cancel() before close(headersReceived) in the real world:
// In the normal completion flow, processHeaders (closes headersReceived) runs
// before processTrailers (cancels ctx) because data-channel messages are
// ordered. However, ctx can be canceled first when the underlying channel or
// user context is torn down while a slow/never-responding server has not yet
// sent headers — a valid edge case the fix must also handle correctly.
//
// The experiment confirms:
//   - cancel() before close(headersReceived): ~98 % of goroutines select ctx.Done()
//   - close(headersReceived) before cancel(): ~0 % select ctx.Done()
//
// Hence this ordering makes the old bug reproduce 100 % of the time.
func TestWebRTCClientStreamHeaderRace(t *testing.T) {
	// GOMAXPROCS=1 gives deterministic cooperative scheduling: after Gosched()
	// the new goroutine runs until it parks in the blocking select, then the
	// main goroutine resumes.
	restore := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(restore)

	const N = 1000
	for i := 0; i < N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		headersReceived := make(chan struct{})

		// Construct only the fields that Header() touches; the rest can be zero.
		s := &webrtcClientStream{
			webrtcBaseStream: &webrtcBaseStream{},
			ctx:              ctx,
			headersReceived:  headersReceived,
			headers:          metadata.MD{"test": []string{"value"}},
		}

		errCh := make(chan error, 1)
		go func() {
			_, err := s.Header()
			errCh <- err
		}()

		// Yield so the goroutine runs: fast-path misses (headersReceived open),
		// falls into the blocking select, and parks there.
		runtime.Gosched()

		// Cancel ctx first. The parked goroutine is immediately made runnable
		// with the ctx.Done() case "selected" by the Go runtime. It has not
		// yet run; it will do so when the main goroutine next blocks.
		cancel()

		// Now close headersReceived. The goroutine is already dequeued from
		// its wait list, so this has no effect on which case it returns — it
		// will still execute the ctx.Done() branch. The non-blocking fallback
		// check inside that branch will see headersReceived is now closed and
		// return the headers instead of an error.
		close(headersReceived)

		// <-errCh blocks the main goroutine, giving the scheduler to the
		// Header() goroutine.
		test.That(t, <-errCh, test.ShouldBeNil)
	}
}

// TestWebRTCClientStreamHeaderRaceIntegration is an integration regression
// test for the same race. It exercises the real gRPC-over-WebRTC stack by
// calling Header() in a goroutine immediately after NewStream (before the
// request has been sent), so that the goroutine is parked in the blocking
// select while the server is still processing. When the server responds with
// headers + trailers in rapid succession the race is triggered naturally.
func TestWebRTCClientStreamHeaderRaceIntegration(t *testing.T) {
	logger := golog.NewTestLogger(t)
	serverOpts := []ServerOption{
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable: true,
		}),
		WithUnauthenticated(),
	}
	rpcServer, err := NewServer(logger, serverOpts...)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&echopb.EchoService_ServiceDesc,
		&es,
		echopb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	rtcConn, err := DialWebRTC(
		context.Background(),
		listener.Addr().String(),
		rpcServer.InstanceNames()[0],
		logger,
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)

	// Use NewStream directly (bypassing the generated gRPC helper) so we can
	// call Header() before SendMsg. After NewStream the server has received the
	// RPC headers and started the handler goroutine, but the handler is blocked
	// on RecvMsg waiting for the request body. Header() therefore falls through
	// the fast-path and parks in the blocking select.
	//
	// Once we send the request the server processes it immediately (EchoMultiple
	// with an empty message sends 0 response messages and returns), issuing
	// response-headers then trailers back-to-back. The data-channel callback
	// goroutine may close headersReceived and cancel ctx without yielding
	// between them, leaving both channels ready when the parked Header()
	// goroutine is next scheduled.
	const method = "/proto.rpc.examples.echo.v1.EchoService/EchoMultiple"
	const iterations = 50
	for i := 0; i < iterations; i++ {
		stream, streamErr := rtcConn.NewStream(
			context.Background(),
			&grpc.StreamDesc{ServerStreams: true},
			method,
		)
		test.That(t, streamErr, test.ShouldBeNil)

		// Launch Header() before the request is sent so it parks in the
		// blocking select (headersReceived not yet closed).
		headerErrCh := make(chan error, 1)
		go func() {
			_, err := stream.Header()
			headerErrCh <- err
		}()

		// Send request + EOS — server processes and responds immediately.
		test.That(t, stream.SendMsg(&echopb.EchoMultipleRequest{Message: ""}), test.ShouldBeNil)
		test.That(t, stream.CloseSend(), test.ShouldBeNil)

		// Header() must return nil even if both channels close while it waits.
		test.That(t, <-headerErrCh, test.ShouldBeNil)

		// Drain to clean up. EchoMultiple with "" sends 0 response messages,
		// so only the EOF trailer should arrive.
		var resp echopb.EchoMultipleResponse
		for {
			recvErr := stream.RecvMsg(&resp)
			if errors.Is(recvErr, io.EOF) {
				break
			}
			test.That(t, recvErr, test.ShouldBeNil)
		}
	}

	test.That(t, rtcConn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestErrDisconnected(t *testing.T) {
	logger := golog.NewTestLogger(t)
	serverOpts := []ServerOption{
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable: true,
		}),
		WithUnauthenticated(),
	}
	rpcServer, err := NewServer(
		logger,
		serverOpts...,
	)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&echopb.EchoService_ServiceDesc,
		&es,
		echopb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	rtcConn, err := DialWebRTC(
		context.Background(),
		listener.Addr().String(),
		rpcServer.InstanceNames()[0],
		logger,
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)

	client := echopb.NewEchoServiceClient(rtcConn)

	msg := "these-are-not-the-droids-you're-looking-for"
	echoResp, err := client.Echo(context.Background(), &echopb.EchoRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, msg)

	// Close underlying ClientConn and expect that further usages of the gRPC
	// client will result in `ErrDisconnected`.
	test.That(t, rtcConn.Close(), test.ShouldBeNil)
	for i := 0; i < 2; i++ {
		echoResp, err = client.Echo(context.Background(), &echopb.EchoRequest{Message: msg})
		test.That(t, echoResp, test.ShouldBeNil)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err, test.ShouldBeError, ErrDisconnected)
	}

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
