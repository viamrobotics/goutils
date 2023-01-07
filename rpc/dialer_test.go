package rpc

import (
	"context"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
)

func TestCachedDialer(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer1, err := NewServer(logger)
	test.That(t, err, test.ShouldBeNil)
	rpcServer2, err := NewServer(logger)
	test.That(t, err, test.ShouldBeNil)

	httpListener1, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	httpListener2, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan1 := make(chan error)
	go func() {
		errChan1 <- rpcServer1.Serve(httpListener1)
	}()
	errChan2 := make(chan error)
	go func() {
		errChan2 <- rpcServer2.Serve(httpListener2)
	}()

	closeCount := 0
	closeCount2 := 0
	closeCount3 := 0
	closeChecker := func() error {
		closeCount++
		return nil
	}
	closeChecker2 := func() error {
		closeCount2++
		return nil
	}
	closeChecker3 := func() error {
		closeCount3++
		return nil
	}
	cachedDialer := NewCachedDialer()
	conn1, cached, err := cachedDialer.DialDirect(
		context.Background(),
		httpListener1.Addr().String(),
		"",
		closeChecker,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	conn2, cached, err := cachedDialer.DialDirect(
		context.Background(),
		httpListener1.Addr().String(),
		"",
		closeChecker,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeTrue)
	conn3, cached, err := cachedDialer.DialDirect(
		context.Background(),
		httpListener1.Addr().String(),
		"more",
		closeChecker2,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	conn4, cached, err := cachedDialer.DialDirect(
		context.Background(),
		httpListener2.Addr().String(),
		"",
		closeChecker3,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	test.That(t, conn1.(*reffedConn).ClientConn, test.ShouldEqual, conn2.(*reffedConn).ClientConn)
	test.That(t, conn3.(*reffedConn).ClientConn, test.ShouldNotEqual, conn2.(*reffedConn).ClientConn)
	test.That(t, conn2.(*reffedConn).ClientConn, test.ShouldNotEqual, conn4.(*reffedConn).ClientConn)

	test.That(t, closeCount, test.ShouldEqual, 0)
	test.That(t, closeCount2, test.ShouldEqual, 0)
	test.That(t, closeCount3, test.ShouldEqual, 0)

	test.That(t, conn1.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 0)
	test.That(t, closeCount2, test.ShouldEqual, 0)
	test.That(t, closeCount3, test.ShouldEqual, 0)

	test.That(t, conn2.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 0)
	test.That(t, closeCount3, test.ShouldEqual, 0)

	test.That(t, conn3.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 1)
	test.That(t, closeCount3, test.ShouldEqual, 0)

	test.That(t, conn4.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 1)
	test.That(t, closeCount3, test.ShouldEqual, 1)

	conn1New, cached, err := cachedDialer.DialDirect(
		context.Background(),
		httpListener1.Addr().String(),
		"",
		closeChecker,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	test.That(t, conn1New.(*reffedConn).ClientConn, test.ShouldNotEqual, conn1.(*reffedConn).ClientConn)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 1)

	test.That(t, cachedDialer.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 2)
	test.That(t, closeCount2, test.ShouldEqual, 1)

	test.That(t, rpcServer1.Stop(), test.ShouldBeNil)
	test.That(t, rpcServer2.Stop(), test.ShouldBeNil)
	err = <-errChan1
	test.That(t, err, test.ShouldBeNil)
	err = <-errChan2
	test.That(t, err, test.ShouldBeNil)
}

func TestCachedDialerDeadlock(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(
		logger,
		WithAuthHandler(CredentialsTypeAPIKey, MakeSimpleAuthHandler([]string{"foo"}, "bar")),
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	cachedDialer := NewCachedDialer()
	ctx := ContextWithDialer(context.Background(), cachedDialer)

	// leave connection open that will come from "multi" which ends up referring to a cached
	// WebRTC or gRPC Direct connection.
	_, err = Dial(
		ctx,
		httpListener.Addr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
		WithEntityCredentials("foo", Credentials{Type: CredentialsTypeAPIKey, Payload: "bar"}),
	)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, cachedDialer.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestReffedConn(t *testing.T) {
	tracking := &closeReffedConn{}
	wrapper := newRefCountedConnWrapper("proto", tracking, nil)
	conn1 := wrapper.Ref()
	conn2 := wrapper.Ref()
	test.That(t, conn1.Close(), test.ShouldBeNil)
	test.That(t, tracking.closeCalled, test.ShouldEqual, 0)
	test.That(t, conn2.Close(), test.ShouldBeNil)
	test.That(t, tracking.closeCalled, test.ShouldEqual, 1)
	test.That(t, conn1.Close(), test.ShouldBeNil)
	test.That(t, tracking.closeCalled, test.ShouldEqual, 1)
	test.That(t, conn2.Close(), test.ShouldBeNil)
	test.That(t, tracking.closeCalled, test.ShouldEqual, 1)
}

func TestDialAllowInsecure(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(
		logger,
		WithAuthHandler(CredentialsTypeAPIKey, MakeSimpleAuthHandler([]string{"foo"}, "bar")),
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithAllowInsecureDowngrade(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	conn, err = Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithAllowInsecureWithCredentialsDowngrade(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	_, err = Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithAllowInsecureDowngrade(),
		WithEntityCredentials("foo", Credentials{Type: CredentialsTypeAPIKey, Payload: "bar"}),
	)
	test.That(t, err, test.ShouldEqual, ErrInsecureWithCredentials)

	conn, err = Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithAllowInsecureWithCredentialsDowngrade(),
		WithEntityCredentials("foo", Credentials{Type: CredentialsTypeAPIKey, Payload: "bar"}),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestClientConnAuthenticator(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(
		logger,
		WithAuthHandler(CredentialsTypeAPIKey, MakeSimpleAuthHandler([]string{"foo"}, "bar")),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
		WithEntityCredentials("foo", Credentials{Type: CredentialsTypeAPIKey, Payload: "bar"}),
	)
	test.That(t, err, test.ShouldBeNil)

	connAuther, ok := conn.(ClientConnAuthenticator)
	test.That(t, ok, test.ShouldBeTrue)

	authMaterial, err := connAuther.Authenticate(context.Background())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authMaterial, test.ShouldNotBeEmpty)

	client := pb.NewEchoServiceClient(conn)
	echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")

	test.That(t, conn.Close(), test.ShouldBeNil)

	// reuse
	conn, err = Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
		WithStaticAuthenticationMaterial(authMaterial),
	)
	test.That(t, err, test.ShouldBeNil)

	_, ok = conn.(ClientConnAuthenticator)
	test.That(t, ok, test.ShouldBeFalse)

	client = pb.NewEchoServiceClient(conn)
	echoResp, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")

	test.That(t, conn.Close(), test.ShouldBeNil)

	// reuse poorly
	conn, err = Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
		WithStaticAuthenticationMaterial(authMaterial+"ah"),
	)
	test.That(t, err, test.ShouldBeNil)

	_, ok = conn.(ClientConnAuthenticator)
	test.That(t, ok, test.ShouldBeFalse)

	client = pb.NewEchoServiceClient(conn)
	_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)

	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

type closeReffedConn struct {
	ClientConn
	closeCalled int
}

func (crc *closeReffedConn) Close() error {
	crc.closeCalled++
	return nil
}

func TestWithDialStatsHandler(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(logger)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	go func() {
		rpcServer.Serve(httpListener)
	}()

	stats := fakeStatsHandler{}

	conn, err := Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithDialStatsHandler(&stats),
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, stats.clientConnections, test.ShouldBeGreaterThan, 1)
}

func TestWithUnaryInterceptor(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(logger, WithUnauthenticated())
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	go func() {
		rpcServer.Serve(httpListener)
	}()

	var interceptedMethods []string
	collector := func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		interceptedMethods = append(interceptedMethods, method)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	var interceptedCount int
	counter := func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		interceptedCount++
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	conn, err := Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithUnaryClientInterceptor(collector),
		WithUnaryClientInterceptor(counter),
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)

	client := pb.NewEchoServiceClient(conn)
	_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	test.That(t, err, test.ShouldBeNil)

	test.That(
		t,
		interceptedMethods,
		test.ShouldResemble,
		[]string{
			"/proto.rpc.webrtc.v1.SignalingService/OptionalWebRTCConfig",
			"/proto.rpc.examples.echo.v1.EchoService/Echo",
		},
	)
	test.That(t, interceptedCount, test.ShouldEqual, 2)
}

func TestWithStreamInterceptor(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(logger, WithUnauthenticated())
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	go func() {
		rpcServer.Serve(httpListener)
	}()

	var interceptedMethods []string
	collector := func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		interceptedMethods = append(interceptedMethods, method)
		return streamer(ctx, desc, cc, method, opts...)
	}
	var interceptedCount int
	counter := func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		interceptedCount++
		return streamer(ctx, desc, cc, method, opts...)
	}
	conn, err := Dial(
		context.Background(),
		httpListener.Addr().String(),
		logger,
		WithStreamClientInterceptor(collector),
		WithStreamClientInterceptor(counter),
		WithDialDebug(),
		WithInsecure(),
	)
	test.That(t, err, test.ShouldBeNil)

	client := pb.NewEchoServiceClient(conn)
	_, err = client.EchoMultiple(context.Background(), &pb.EchoMultipleRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	test.That(t, err, test.ShouldBeNil)

	test.That(
		t,
		interceptedMethods,
		test.ShouldResemble,
		[]string{"/proto.rpc.examples.echo.v1.EchoService/EchoMultiple"},
	)
	test.That(t, interceptedCount, test.ShouldEqual, 1)
}
