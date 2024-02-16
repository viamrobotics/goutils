package rpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

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
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger, func(hosts []string) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientServer(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientServer(t *testing.T, signalingCallQueue WebRTCCallQueue, logger golog.Logger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger)
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
					test.That(t, resp.Message, test.ShouldEqual, "hello")

					// big message
					bigZ := strings.Repeat("z", 1<<18)
					resp, err = echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: bigZ})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, resp.Message, test.ShouldEqual, bigZ)
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
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger, func(hosts []string) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialCancel(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientDialCancel(t *testing.T, signalingCallQueue WebRTCCallQueue, logger golog.Logger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger)
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
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger, func(hosts []string) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialReflectAnswererError(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientDialReflectAnswererError(t *testing.T, signalingCallQueue WebRTCCallQueue, logger golog.Logger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger)
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
		Uuid: offer.Uuid,
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
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger, func(hosts []string) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientDialConcurrent(t, signalingCallQueue, logger)
}

// this is a good integration test against mongoDBWebRTCCallQueue
//
//nolint:thelper
func testWebRTCClientDialConcurrent(t *testing.T, signalingCallQueue WebRTCCallQueue, logger golog.Logger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger)
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

	dialErrCh := make(chan error, 2)
	go func() {
		t.Log("starting dial 1")
		cc, err := DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			logger,
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
		t.Log("starting dial 2")
		cc, err := DialWebRTC(
			context.Background(),
			grpcListener.Addr().String(),
			host,
			logger,
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingInsecure: true,
			}),
		)
		if cc != nil {
			cc.Close()
		}
		dialErrCh <- err
	}()

	t.Log("answer client 1 is receiving")
	offer1, err := answerClient1.Recv()
	test.That(t, err, test.ShouldBeNil)

	t.Log("answer client 2 is receiving")
	offer2, err := answerClient2.Recv()
	test.That(t, err, test.ShouldBeNil)

	test.That(t, offer1.Uuid, test.ShouldNotEqual, offer2.Uuid)

	test.That(t, answerClient1.Send(&webrtcpb.AnswerResponse{
		Uuid: offer1.Uuid,
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
	test.That(t, offerUpdate.Uuid, test.ShouldEqual, offer1.Uuid)
	test.That(t, offerUpdate.GetError().String(), test.ShouldContainSubstring, "illegal")

	test.That(t, answerClient2.Send(&webrtcpb.AnswerResponse{
		Uuid: offer2.Uuid,
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
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger, func(hosts []string) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCClientAnswerConcurrent(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCClientAnswerConcurrent(t *testing.T, signalingCallQueue WebRTCCallQueue, logger golog.Logger) {
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger)
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

	grpcConn, err := DialDirectGRPC(context.Background(), grpcListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer grpcConn.Close()
	signalingClient := webrtcpb.NewSignalingServiceClient(grpcConn)
	md := metadata.New(nil)
	md.Append(RPCHostMetadataField, host)
	callCtx := metadata.NewOutgoingContext(context.Background(), md)

	pc1, _, err := newPeerConnectionForClient(context.Background(), webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)
	defer pc1.Close()

	encodedSDP1, err := encodeSDP(pc1.LocalDescription())
	test.That(t, err, test.ShouldBeNil)

	pc2, _, err := newPeerConnectionForClient(context.Background(), webrtc.Configuration{}, true, logger)
	test.That(t, err, test.ShouldBeNil)
	defer pc2.Close()

	encodedSDP2, err := encodeSDP(pc2.LocalDescription())
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

	test.That(t, answer1.Uuid, test.ShouldNotEqual, answer2.Uuid)

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
		test.That(t, echoMultiResp.Message, test.ShouldEqual, msg[i:i+1])
	}
	test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeError, io.EOF)

	echoClient, err = client.EchoMultiple(context.Background(), &echopb.EchoMultipleRequest{Message: msg})
	test.That(t, err, test.ShouldBeNil)
	for i := 0; i < 5; i++ {
		test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeNil)
		test.That(t, echoMultiResp.Message, test.ShouldEqual, msg[i:i+1])
	}
	test.That(t, echoClient.RecvMsg(&echoMultiResp), test.ShouldBeError, io.EOF)

	test.That(t, rtcConn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
