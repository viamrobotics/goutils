package dialer_test

import (
	"context"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"google.golang.org/grpc"

	"go.viam.com/test"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc/dialer"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/rpc/server"
)

func TestCachedDialer(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer1, err := server.New(logger)
	test.That(t, err, test.ShouldBeNil)
	rpcServer2, err := server.New(logger)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer1.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&es,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)
	err = rpcServer2.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&es,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
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

	cachedDialer := dialer.NewCachedDialer()
	conn1, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	conn2, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	conn3, err := cachedDialer.DialDirect(context.Background(), httpListener2.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn1.(*dialer.ReffedConn).ClientConn, test.ShouldEqual, conn2.(*dialer.ReffedConn).ClientConn)
	test.That(t, conn2.(*dialer.ReffedConn).ClientConn, test.ShouldNotEqual, conn3.(*dialer.ReffedConn).ClientConn)
	test.That(t, conn1.Close(), test.ShouldBeNil)
	test.That(t, conn2.Close(), test.ShouldBeNil)
	test.That(t, conn3.Close(), test.ShouldBeNil)
	conn1New, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn1New.(*dialer.ReffedConn).ClientConn, test.ShouldNotEqual, conn1.(*dialer.ReffedConn).ClientConn)

	test.That(t, cachedDialer.Close(), test.ShouldBeNil)

	test.That(t, rpcServer1.Stop(), test.ShouldBeNil)
	test.That(t, rpcServer2.Stop(), test.ShouldBeNil)
	err = <-errChan1
	test.That(t, err, test.ShouldBeNil)
	err = <-errChan2
	test.That(t, err, test.ShouldBeNil)
}

func TestReffedConn(t *testing.T) {
	tracking := &closeReffedConn{}
	wrapper := dialer.NewRefCountedConnWrapper(tracking, nil)
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

type closeReffedConn struct {
	dialer.ClientConn
	closeCalled int
}

func (crc *closeReffedConn) Close() error {
	crc.closeCalled++
	return nil
}

func TestContextDialer(t *testing.T) {
	ctx := context.Background()
	cachedDialer := dialer.NewCachedDialer()
	ctx = dialer.ContextWithDialer(ctx, cachedDialer)
	cachedDialer2 := dialer.ContextDialer(context.Background())
	test.That(t, cachedDialer2, test.ShouldBeNil)
	cachedDialer2 = dialer.ContextDialer(ctx)
	test.That(t, cachedDialer2, test.ShouldEqual, cachedDialer)
}
