package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/edaniels/golog"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	rpcwebrtc "go.viam.com/utils/rpc/webrtc"

	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func TestServer(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewWithOptions(Options{WebRTC: WebRTCOptions{
		Enable:        true,
		Insecure:      true,
		SignalingHost: "yeehaw",
	}}, logger)
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

	// standard grpc
	conn, err := grpc.DialContext(context.Background(), httpListener.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()
	client := pb.NewEchoServiceClient(conn)

	echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")

	es.SetFail(true)
	_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, status.Convert(err).Message(), test.ShouldEqual, "whoops")
	es.SetFail(false)

	// grpc-web
	httpURL := fmt.Sprintf("http://%s/proto.rpc.examples.echo.v1.EchoService/Echo", httpListener.Addr().String())
	grpcWebReq := `AAAAAAYKBGhleSE=`
	httpResp1, err := http.Post(httpURL, "application/grpc-web-text", strings.NewReader(grpcWebReq))
	test.That(t, err, test.ShouldBeNil)
	defer httpResp1.Body.Close()
	test.That(t, httpResp1.StatusCode, test.ShouldEqual, 200)
	rd, err := ioutil.ReadAll(httpResp1.Body)
	test.That(t, err, test.ShouldBeNil)
	// it says hey!
	test.That(t, string(rd), test.ShouldResemble, "AAAAAAYKBGhleSE=gAAAABBncnBjLXN0YXR1czogMA0K")

	es.SetFail(true)
	httpResp1, err = http.Post(httpURL, "application/grpc-web-text", strings.NewReader(grpcWebReq))
	test.That(t, err, test.ShouldBeNil)
	defer httpResp1.Body.Close()
	test.That(t, httpResp1.StatusCode, test.ShouldEqual, 200)
	es.SetFail(false)
	rd, err = ioutil.ReadAll(httpResp1.Body)
	test.That(t, err, test.ShouldBeNil)
	// it says hey!
	test.That(t, httpResp1.Header["Grpc-Message"], test.ShouldResemble, []string{"whoops"})
	test.That(t, string(rd), test.ShouldResemble, "")

	// JSON
	httpURL = fmt.Sprintf("http://%s/rpc/examples/echo/v1/echo", httpListener.Addr().String())
	httpResp2, err := http.Post(httpURL, "application/json", strings.NewReader(`{"message": "world"}`))
	test.That(t, err, test.ShouldBeNil)
	defer httpResp2.Body.Close()
	test.That(t, httpResp2.StatusCode, test.ShouldEqual, 200)
	dec := json.NewDecoder(httpResp2.Body)
	var echoM map[string]interface{}
	test.That(t, dec.Decode(&echoM), test.ShouldBeNil)
	test.That(t, echoM, test.ShouldResemble, map[string]interface{}{"message": "world"})

	es.SetFail(true)
	httpResp2, err = http.Post(httpURL, "application/json", strings.NewReader(`{"message": "world"}`))
	test.That(t, err, test.ShouldBeNil)
	defer httpResp2.Body.Close()
	test.That(t, httpResp2.StatusCode, test.ShouldEqual, 500)
	es.SetFail(false)

	// WebRTC
	_, err = rpcwebrtc.Dial(context.Background(), httpListener.Addr().String(), rpcwebrtc.Options{
		Insecure: true,
	}, logger)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")

	rtcConn, err := rpcwebrtc.Dial(context.Background(), rpc.HostURI(httpListener.Addr().String(), "yeehaw"), rpcwebrtc.Options{
		Insecure: true,
	}, logger)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, rtcConn.Close(), test.ShouldBeNil)
	}()
	client = pb.NewEchoServiceClient(rtcConn)

	echoResp, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")

	es.SetFail(true)
	_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, status.Convert(err).Message(), test.ShouldEqual, "whoops")
	es.SetFail(false)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
