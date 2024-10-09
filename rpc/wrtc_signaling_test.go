package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"go.viam.com/utils"
	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebRTCSignalingWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCSignaling(t, signalingCallQueue, logger)
}

func TestWebRTCSignalingWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testWebRTCSignaling(t, signalingCallQueue, logger)
}

//nolint:thelper
func testWebRTCSignaling(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
	hosts := []string{"yeehaw", "woahthere"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
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

			answerer := newWebRTCSignalingAnswerer(
				grpcListener.Addr().String(),
				hosts,
				webrtcServer,
				[]DialOption{WithInsecure()},
				webrtc.Configuration{},
				logger,
			)
			answerer.Start()

			cc, err := grpc.Dial(grpcListener.Addr().String(), grpc.WithBlock(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			test.That(t, err, test.ShouldBeNil)
			defer func() {
				test.That(t, cc.Close(), test.ShouldBeNil)
			}()
			signalClient := webrtcpb.NewSignalingServiceClient(cc)

			callClient, err := signalClient.Call(context.Background(), &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "expected rpc-host")

			md := metadata.New(map[string]string{"rpc-host": ""})
			callCtx := metadata.NewOutgoingContext(context.Background(), md)
			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")

			md = metadata.New(map[string]string{"rpc-host": "one"})
			md.Append("rpc-host", "two")
			callCtx = metadata.NewOutgoingContext(context.Background(), md)
			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "expected 1 rpc-host")

			md = metadata.New(map[string]string{"rpc-host": host})
			callCtx = metadata.NewOutgoingContext(context.Background(), md)

			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "unexpected")

			callClient, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{Sdp: "thing"})
			test.That(t, err, test.ShouldBeNil)
			_, err = callClient.Recv()
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, err.Error(), test.ShouldContainSubstring, "illegal")

			for _, tc := range []bool{true, false} {
				t.Run(fmt.Sprintf("with trickle disabled %t", tc), func(t *testing.T) {
					ch, err := DialWebRTC(
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
						test.That(t, ch.Close(), test.ShouldBeNil)
					}()

					echoClient := echopb.NewEchoServiceClient(ch)
					resp, err := echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: "hello"})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, resp.Message, test.ShouldEqual, "hello")
				})
			}

			// Mimic order of stopping used in `simpleServer.Stop()` (answerer, sig
			// server's gRPC listener, then machine).
			answerer.Stop()
			grpcServer.Stop()
			webrtcServer.Stop()
			test.That(t, <-serveDone, test.ShouldBeNil)
		})
	}
}

func TestWebRTCAnswererImmediateStop(t *testing.T) {
	// Primarily a regression test for RSDK-3441.

	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()

	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		defaultHeartbeatInterval)
	defer signalingServer.Close()

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	webrtcServer := newWebRTCServer(logger)
	defer webrtcServer.Stop()
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	hosts := []string{"foo", "bar"}
	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		hosts,
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)

	// Running both asynchronously means Stop will potentially happen before Start,
	// but this setup still ensures that the two methods do not race each other.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		answerer.Start()
	}()
	go func() {
		defer wg.Done()
		answerer.Stop()
	}()
	wg.Wait()
}

func TestSignalingHeartbeats(t *testing.T) {
	logger, observer := golog.NewObservedTestLogger(t)

	// Create a simple signaling server with an in-memory call-queue.
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	// Use a lowered heartbeatInterval (500ms instead of 15s).
	signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger,
		500*time.Millisecond)
	defer signalingServer.Close()
	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)
	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	// Create a simple WebRTC server that (needlessly) serves the Echo service.
	// Start an answerer with it.
	webrtcServer := newWebRTCServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})
	answerer := newWebRTCSignalingAnswerer(
		grpcListener.Addr().String(),
		[]string{"foo"},
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	answerer.Start()

	// Assert that the answerer eventually logs received heartbeats.
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		t.Helper()
		test.That(tb, observer.FilterMessageSnippet(heartbeatReceivedLog).Len(),
			test.ShouldBeGreaterThan, 0)
	})

	// Mimic order of stopping used in `simpleServer.Stop()` (answerer, sig
	// server's gRPC listener, then machine).
	answerer.Stop()
	grpcServer.Stop()
	webrtcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}
