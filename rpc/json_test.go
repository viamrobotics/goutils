package rpc_test

import (
	"context"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"google.golang.org/grpc"

	"go.viam.com/test"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/rpc/server"
)

func TestCallClientMethodLineJSON(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := server.New(logger)
	test.That(t, err, test.ShouldBeNil)

	es := echoserver.Server{}
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&es,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := grpc.DialContext(context.Background(), httpListener.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	client := pb.NewEchoServiceClient(conn)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()

	resp, err := rpc.CallClientMethodLineJSON(context.Background(), client, nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resp, test.ShouldResemble, []byte(nil))

	resp, err = rpc.CallClientMethodLineJSON(context.Background(), client, []byte(`Echo`))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resp, test.ShouldResemble, []byte(`{"message":""}`))

	_, err = rpc.CallClientMethodLineJSON(context.Background(), client, []byte(`Echo foo`))
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "error unmarshaling")

	resp, err = rpc.CallClientMethodLineJSON(context.Background(), client, []byte(`Echo {}`))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resp, test.ShouldResemble, []byte(`{"message":""}`))

	resp, err = rpc.CallClientMethodLineJSON(context.Background(), client, []byte(`Echo {"message": "hey"}`))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, resp, test.ShouldResemble, []byte(`{"message":"hey"}`))

	es.SetFail(true)
	_, err = rpc.CallClientMethodLineJSON(context.Background(), client, []byte(`Echo {}`))
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "whoops")

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
