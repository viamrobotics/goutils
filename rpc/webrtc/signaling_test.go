package rpcwebrtc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	"go.viam.com/utils/rpc"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	rpcwebrtc "go.viam.com/utils/rpc/webrtc"
	"go.viam.com/utils/testutils"
)

func TestSignaling(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	signalingServer := rpcwebrtc.NewSignalingServer(rpcwebrtc.NewMemoryCallQueue())

	grpcListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	grpcServer := grpc.NewServer()
	grpcServer.RegisterService(&webrtcpb.SignalingService_ServiceDesc, signalingServer)

	serveDone := make(chan error)
	go func() {
		serveDone <- grpcServer.Serve(grpcListener)
	}()

	answerer := rpcwebrtc.NewSignalingAnswerer("foo", "yeehaw", nil, true, logger)
	go func() {
		time.Sleep(time.Second)
		answerer.Stop()
	}()
	test.That(t, answerer.Start(), test.ShouldEqual, context.Canceled)

	webrtcServer := rpcwebrtc.NewServer(logger)
	webrtcServer.RegisterService(&echopb.EchoService_ServiceDesc, &echoserver.Server{})

	answerer = rpcwebrtc.NewSignalingAnswerer(grpcListener.Addr().String(), "yeehaw", webrtcServer, true, logger)
	test.That(t, answerer.Start(), test.ShouldBeNil)

	cc, err := grpc.Dial(grpcListener.Addr().String(), grpc.WithBlock(), grpc.WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, cc.Close(), test.ShouldBeNil)
	}()
	signalClient := webrtcpb.NewSignalingServiceClient(cc)

	_, err = signalClient.Call(context.Background(), &webrtcpb.CallRequest{})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "expected rpc-host")

	md := metadata.New(map[string]string{"rpc-host": "yeehaw"})
	callCtx := metadata.NewOutgoingContext(context.Background(), md)

	_, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "unexpected")

	_, err = signalClient.Call(callCtx, &webrtcpb.CallRequest{Sdp: "thing"})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "illegal")

	ch, err := rpcwebrtc.Dial(context.Background(), rpc.HostURI(grpcListener.Addr().String(), "yeehaw"), true, logger)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, ch.Close(), test.ShouldBeNil)
	}()

	echoClient := echopb.NewEchoServiceClient(ch)
	resp, err := echoClient.Echo(context.Background(), &echopb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resp.Message, test.ShouldEqual, "hello")

	webrtcServer.Stop()
	answerer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}
