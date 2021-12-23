package rpc

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
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
)

func TestServer(t *testing.T) {
	logger := golog.NewTestLogger(t)

	for _, withAuthentication := range []bool{false, true} {
		t.Run(fmt.Sprintf("with authentication=%t", withAuthentication), func(t *testing.T) {
			serverOpts := []ServerOption{
				WithWebRTCServerOptions(WebRTCServerOptions{
					Enable:        true,
					SignalingHost: "yeehaw",
				}),
			}
			if withAuthentication {
				serverOpts = append(serverOpts, WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
					return nil
				}, func(ctx context.Context, entity string) error {
					return nil
				})))
			} else {
				serverOpts = append(serverOpts, WithUnauthenticated())
			}
			rpcServer, err := NewServer(
				logger,
				serverOpts...,
			)
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
			ctx := context.Background()
			var bearer string
			if withAuthentication {
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, "authentication")
				authClient := rpcpb.NewAuthServiceClient(conn)
				_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
					Type:    "notfake",
					Payload: "something",
				}})
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, "no way to")

				authResp, err := authClient.Authenticate(
					context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
						Type:    "fake",
						Payload: "something",
					}})
				test.That(t, err, test.ShouldBeNil)

				md := make(metadata.MD)
				bearer = fmt.Sprintf("Bearer %s", authResp.AccessToken)
				md.Set("authorization", bearer)
				ctx = metadata.NewOutgoingContext(context.Background(), md)
			} else {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
			}

			es.SetFail(true)
			_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
			test.That(t, err, test.ShouldNotBeNil)
			test.That(t, status.Convert(err).Message(), test.ShouldEqual, "whoops")
			es.SetFail(false)

			// grpc-web
			httpURL := fmt.Sprintf("http://%s/proto.rpc.examples.echo.v1.EchoService/Echo", httpListener.Addr().String())
			grpcWebReq := `AAAAAAYKBGhleSE=`
			req, err := http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
			test.That(t, err, test.ShouldBeNil)
			req.Header.Add("content-type", "application/grpc-web-text")
			httpResp1, err := http.DefaultClient.Do(req)
			test.That(t, err, test.ShouldBeNil)
			defer httpResp1.Body.Close()
			test.That(t, httpResp1.StatusCode, test.ShouldEqual, 200)
			rd, err := ioutil.ReadAll(httpResp1.Body)
			test.That(t, err, test.ShouldBeNil)
			if withAuthentication {
				test.That(t, httpResp1.Header["Grpc-Message"], test.ShouldResemble, []string{"authentication required"})
				test.That(t, string(rd), test.ShouldResemble, "")

				req, err := http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
				test.That(t, err, test.ShouldBeNil)
				req.Header.Add("content-type", "application/grpc-web-text")
				req.Header.Add("authorization", bearer)
				httpResp2, err := http.DefaultClient.Do(req)
				test.That(t, err, test.ShouldBeNil)
				defer httpResp2.Body.Close()
				test.That(t, httpResp2.StatusCode, test.ShouldEqual, 200)
				rd, err = ioutil.ReadAll(httpResp2.Body)
				test.That(t, err, test.ShouldBeNil)
			}
			// it says hey!
			test.That(t, string(rd), test.ShouldResemble, "AAAAAAYKBGhleSE=gAAAABBncnBjLXN0YXR1czogMA0K")

			es.SetFail(true)
			req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
			test.That(t, err, test.ShouldBeNil)
			req.Header.Add("content-type", "application/grpc-web-text")
			if withAuthentication {
				req.Header.Add("authorization", bearer)
			}
			httpResp2, err := http.DefaultClient.Do(req)
			test.That(t, err, test.ShouldBeNil)
			defer httpResp2.Body.Close()
			test.That(t, httpResp2.StatusCode, test.ShouldEqual, 200)
			es.SetFail(false)
			rd, err = ioutil.ReadAll(httpResp2.Body)
			test.That(t, err, test.ShouldBeNil)
			// it says hey!
			test.That(t, httpResp2.Header["Grpc-Message"], test.ShouldResemble, []string{"whoops"})
			test.That(t, string(rd), test.ShouldResemble, "")

			// JSON
			httpURL = fmt.Sprintf("http://%s/rpc/examples/echo/v1/echo", httpListener.Addr().String())
			req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
			test.That(t, err, test.ShouldBeNil)
			req.Header.Add("content-type", "application/json")
			// if withAuthentication {
			// 	req.Header.Add("authorization", bearer)
			// }
			httpResp3, err := http.DefaultClient.Do(req)
			test.That(t, err, test.ShouldBeNil)
			defer httpResp3.Body.Close()
			if withAuthentication {
				test.That(t, httpResp3.StatusCode, test.ShouldEqual, 401)
				req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
				test.That(t, err, test.ShouldBeNil)
				req.Header.Add("content-type", "application/json")
				req.Header.Add("authorization", bearer)
				httpResp3, err = http.DefaultClient.Do(req)
				test.That(t, err, test.ShouldBeNil)
				defer httpResp3.Body.Close()
			} else {
				test.That(t, httpResp3.StatusCode, test.ShouldEqual, 200)
			}
			dec := json.NewDecoder(httpResp3.Body)
			var echoM map[string]interface{}
			test.That(t, dec.Decode(&echoM), test.ShouldBeNil)
			test.That(t, echoM, test.ShouldResemble, map[string]interface{}{"message": "world"})

			es.SetFail(true)
			req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
			test.That(t, err, test.ShouldBeNil)
			req.Header.Add("content-type", "application/json")
			if withAuthentication {
				req.Header.Add("authorization", bearer)
			}
			httpResp4, err := http.DefaultClient.Do(req)
			test.That(t, err, test.ShouldBeNil)
			defer httpResp4.Body.Close()
			test.That(t, httpResp4.StatusCode, test.ShouldEqual, 500)
			es.SetFail(false)

			// WebRTC
			_, err = dialWebRTC(context.Background(), httpListener.Addr().String(), &dialOptions{
				webrtcOpts: DialWebRTCOptions{
					Insecure: true,
				},
			}, logger)
			test.That(t, err, test.ShouldNotBeNil)
			if withAuthentication {
				test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
			} else {
				test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")
			}

			rtcConn, err := dialWebRTC(context.Background(), HostURI(httpListener.Addr().String(), "yeehaw"), &dialOptions{
				webrtcOpts: DialWebRTCOptions{
					Insecure: true,
				},
			}, logger)
			if withAuthentication {
				test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
				rtcConn, err = dialWebRTC(context.Background(), HostURI(httpListener.Addr().String(), "yeehaw"), &dialOptions{
					creds: Credentials{Type: "fake"},
					webrtcOpts: DialWebRTCOptions{
						Insecure: true,
					},
				}, logger)
			}
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
		})
	}
}
