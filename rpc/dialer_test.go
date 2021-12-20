package rpc

import (
	"context"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"google.golang.org/grpc"

	"go.viam.com/test"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
)

func TestCachedDialer(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer1, err := NewServer(logger)
	test.That(t, err, test.ShouldBeNil)
	rpcServer2, err := NewServer(logger)
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

	closeCount := 0
	closeCount2 := 0
	closeChecker := func() error {
		closeCount++
		return nil
	}
	closeChecker2 := func() error {
		closeCount2++
		return nil
	}
	cachedDialer := NewCachedDialer()
	conn1, cached, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), closeChecker, grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	conn2, cached, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), closeChecker, grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeTrue)
	conn3, cached, err := cachedDialer.DialDirect(context.Background(), httpListener2.Addr().String(), closeChecker2, grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, cached, test.ShouldBeFalse)
	test.That(t, conn1.(*reffedConn).ClientConn, test.ShouldEqual, conn2.(*reffedConn).ClientConn)
	test.That(t, conn2.(*reffedConn).ClientConn, test.ShouldNotEqual, conn3.(*reffedConn).ClientConn)

	test.That(t, closeCount, test.ShouldEqual, 0)
	test.That(t, closeCount2, test.ShouldEqual, 0)

	test.That(t, conn1.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 0)
	test.That(t, closeCount2, test.ShouldEqual, 0)

	test.That(t, conn2.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 0)

	test.That(t, conn3.Close(), test.ShouldBeNil)

	test.That(t, closeCount, test.ShouldEqual, 1)
	test.That(t, closeCount2, test.ShouldEqual, 1)

	conn1New, cached, err := cachedDialer.DialDirect(context.Background(), httpListener1.Addr().String(), closeChecker, grpc.WithInsecure(), grpc.WithBlock())
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

func TestReffedConn(t *testing.T) {
	tracking := &closeReffedConn{}
	wrapper := newRefCountedConnWrapper(tracking, nil)
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
	ClientConn
	closeCalled int
}

func (crc *closeReffedConn) Close() error {
	crc.closeCalled++
	return nil
}
