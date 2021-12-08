package rpc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/edaniels/golog"
	"github.com/pion/webrtc/v3"
	"go.viam.com/test"
	"google.golang.org/grpc"

	echopb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebRTCClientServer(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	queue := NewMemoryWebRTCCallQueue()
	defer queue.Close()
	signalingServer := newWebRTCSignalingServer(queue, nil)

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
		"yeehaw",
		webrtcServer,
		[]DialOption{WithInsecure()},
		webrtc.Configuration{},
		logger,
	)
	test.That(t, answerer.Start(), test.ShouldBeNil)

	for _, tc := range []bool{true, false} {
		t.Run(fmt.Sprintf("with trickle disabled %t", tc), func(t *testing.T) {
			cc, err := DialWebRTC(
				context.Background(),
				HostURI(grpcListener.Addr().String(), "yeehaw"),
				logger,
				WithWebRTCOptions(DialWebRTCOptions{
					Insecure:          true,
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

	webrtcServer.Stop()
	answerer.Stop()
	grpcServer.Stop()
	test.That(t, <-serveDone, test.ShouldBeNil)
}
