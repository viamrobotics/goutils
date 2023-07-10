package rpc

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"go.uber.org/atomic"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestTracingInterceptors(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	var clientSpan *trace.Span
	ctx, clientSpan := trace.StartSpan(context.Background(), "client")
	defer clientSpan.End()

	unaryServerTestingInterceptor := func(
		ctx context.Context, req interface{},
		info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (interface{}, error) {
		serverSpan := trace.FromContext(ctx)

		// Ideally we would test that serverSpan's parent span ID is the same as
		// clientSpan's ID, but we can't access that data from here so testing
		// that they share the same trace ID is the best we can do (which still
		// tests that serverSpan and clientSpan are somehow related to one
		// another)
		currentMD, _ := metadata.FromIncomingContext(ctx)
		currentMD.Set("captured-trace-id", serverSpan.SpanContext().TraceID.String())
		grpc.SetHeader(ctx, currentMD)
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if s, ok := status.FromError(err); ok {
			return nil, errors.Wrapf(err, s.Message())
		}
		if s := status.FromContextError(err); s != nil {
			return nil, s.Err()
		}
		return nil, err
	}

	var capturedStreamTraceID atomic.String
	streamServerTestingInterceptor := func(
		srv interface{}, ss grpc.ServerStream,
		info *grpc.StreamServerInfo, handler grpc.StreamHandler,
	) error {
		serverSpan := trace.FromContext(ss.Context())

		if info.FullMethod == "/proto.rpc.examples.echo.v1.EchoService/EchoMultiple" {
			capturedStreamTraceID.Store(serverSpan.SpanContext().TraceID.String())
		}
		err := handler(srv, ss)
		if err == nil {
			return nil
		}
		if s, ok := status.FromError(err); ok {
			return errors.Wrapf(err, s.Message())
		}
		if s := status.FromContextError(err); s != nil {
			return s.Err()
		}
		return err
	}

	internalSignalingHost := "yeehaw"
	serverOpts := []ServerOption{
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{internalSignalingHost},
		}),
		WithUnauthenticated(),
		WithUnaryServerInterceptor(unaryServerTestingInterceptor),
		WithStreamServerInterceptor(streamServerTestingInterceptor),
	}

	rpcServer, err := NewServer(
		logger,
		serverOpts...,
	)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer.RegisterServiceServer(
		ctx,
		&pb.EchoService_ServiceDesc,
		&es,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	defer func() {
		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
		err = <-errChan
		test.That(t, err, test.ShouldBeNil)
	}()

	unaryTest := func(ctx context.Context, client pb.EchoServiceClient) {
		t.Helper()
		var mdResp metadata.MD
		resp, err := client.Echo(ctx, &pb.EchoRequest{Message: "hello"}, grpc.Header(&mdResp))
		test.That(t, resp.Message, test.ShouldEqual, "hello")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, mdResp.Get("captured-trace-id"),
			test.ShouldResemble, []string{clientSpan.SpanContext().TraceID.String()})
	}

	streamTest := func(ctx context.Context, client pb.EchoServiceClient) {
		t.Helper()
		var mdResp metadata.MD
		multiClient, err := client.EchoMultiple(ctx, &pb.EchoMultipleRequest{Message: "hello?"}, grpc.Header(&mdResp))
		test.That(t, err, test.ShouldBeNil)
		fullResponse := ""
		for {
			resp, err := multiClient.Recv()
			test.That(t, err, test.ShouldBeIn, []error{nil, io.EOF})
			if err != nil {
				break
			}
			fullResponse += resp.Message
		}
		test.That(t, fullResponse, test.ShouldEqual, "hello?")
		test.That(t, capturedStreamTraceID.Load(), test.ShouldEqual, clientSpan.SpanContext().TraceID.String())
	}

	// gRPC
	grpcOpts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(UnaryClientTracingInterceptor()),
		grpc.WithStreamInterceptor(StreamClientTracingInterceptor()),
	}

	conn, err := grpc.DialContext(ctx, listener.Addr().String(), grpcOpts...)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()

	client := pb.NewEchoServiceClient(conn)
	unaryTest(ctx, client)
	streamTest(ctx, client)

	// WebRTC
	rtcConn, err := dialWebRTC(ctx, listener.Addr().String(), internalSignalingHost, &dialOptions{
		webrtcOpts: DialWebRTCOptions{
			SignalingInsecure: true,
		},
		webrtcOptsSet:     true,
		unaryInterceptor:  UnaryClientTracingInterceptor(),
		streamInterceptor: StreamClientTracingInterceptor(),
	}, logger)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, rtcConn.Close(), test.ShouldBeNil)
	}()

	client = pb.NewEchoServiceClient(rtcConn)
	unaryTest(ctx, client)
	streamTest(ctx, client)
}
