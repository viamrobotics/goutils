package rpc

import (
	"context"
	"crypto/tls"
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
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/internal"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
)

func TestServer(t *testing.T) {
	logger := golog.NewTestLogger(t)

	hosts := []string{"yeehaw", "woahthere"}
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("with secure=%t", secure), func(t *testing.T) {
			for _, host := range hosts {
				t.Run(host, func(t *testing.T) {
					for _, withAuthentication := range []bool{false, true} {
						t.Run(fmt.Sprintf("with authentication=%t", withAuthentication), func(t *testing.T) {
							serverOpts := []ServerOption{
								WithWebRTCServerOptions(WebRTCServerOptions{
									Enable:         true,
									SignalingHosts: hosts,
								}),
							}
							if withAuthentication {
								serverOpts = append(serverOpts,
									WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
										return map[string]string{}, nil
									}, func(ctx context.Context, entity string) (interface{}, error) {
										return 1, nil
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

							listener, err := net.Listen("tcp", "localhost:0")
							test.That(t, err, test.ShouldBeNil)

							errChan := make(chan error)
							go func() {
								if secure {
									tlsCertFile := internal.ResolveFile("testdata/cert.pem")
									tlsKeyFile := internal.ResolveFile("testdata/key.pem")
									errChan <- rpcServer.ServeTLS(listener, tlsCertFile, tlsKeyFile)
								} else {
									errChan <- rpcServer.Serve(listener)
								}
							}()

							// standard grpc
							tlsConf := &tls.Config{
								MinVersion:         tls.VersionTLS12,
								InsecureSkipVerify: true,
							}
							grpcOpts := []grpc.DialOption{grpc.WithBlock()}
							if secure {
								grpcOpts = append(
									grpcOpts,
									grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)),
								)
							} else {
								grpcOpts = append(grpcOpts, grpc.WithInsecure())
							}
							conn, err := grpc.DialContext(context.Background(), listener.Addr().String(), grpcOpts...)
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
								test.That(t, err.Error(), test.ShouldContainSubstring, "no auth handler")

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

							var scheme string
							if secure {
								scheme = "https"
							} else {
								scheme = "http"
							}

							transport := &http.Transport{TLSClientConfig: tlsConf}
							httpClient := &http.Client{Transport: transport}
							defer transport.CloseIdleConnections()

							// grpc-web
							httpURL := fmt.Sprintf("%s://%s/proto.rpc.examples.echo.v1.EchoService/Echo", scheme, listener.Addr().String())
							grpcWebReq := `AAAAAAYKBGhleSE=`
							req, err := http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
							test.That(t, err, test.ShouldBeNil)
							req.Header.Add("content-type", "application/grpc-web-text")
							httpResp1, err := httpClient.Do(req)
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
								httpResp2, err := httpClient.Do(req)
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
							httpResp2, err := httpClient.Do(req)
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
							httpURL = fmt.Sprintf("%s://%s/rpc/examples/echo/v1/echo", scheme, listener.Addr().String())
							req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
							test.That(t, err, test.ShouldBeNil)
							req.Header.Add("content-type", "application/json")
							httpResp3, err := httpClient.Do(req)
							test.That(t, err, test.ShouldBeNil)
							defer httpResp3.Body.Close()
							if withAuthentication {
								test.That(t, httpResp3.StatusCode, test.ShouldEqual, 401)
								req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
								test.That(t, err, test.ShouldBeNil)
								req.Header.Add("content-type", "application/json")
								req.Header.Add("authorization", bearer)
								httpResp3, err = httpClient.Do(req)
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
							httpResp4, err := httpClient.Do(req)
							test.That(t, err, test.ShouldBeNil)
							defer httpResp4.Body.Close()
							test.That(t, httpResp4.StatusCode, test.ShouldEqual, 500)
							es.SetFail(false)

							// WebRTC
							_, err = dialWebRTC(context.Background(), listener.Addr().String(), &dialOptions{
								tlsConfig: tlsConf,
								webrtcOpts: DialWebRTCOptions{
									SignalingInsecure: !secure,
								},
							}, logger)
							test.That(t, err, test.ShouldNotBeNil)
							if withAuthentication {
								test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
							} else {
								test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")
							}

							rtcConn, err := dialWebRTC(context.Background(), HostURI(listener.Addr().String(), host), &dialOptions{
								tlsConfig: tlsConf,
								webrtcOpts: DialWebRTCOptions{
									SignalingInsecure: !secure,
								},
							}, logger)
							if withAuthentication {
								test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
								rtcConn, err = dialWebRTC(context.Background(), HostURI(listener.Addr().String(), host), &dialOptions{
									tlsConfig: tlsConf,
									webrtcOpts: DialWebRTCOptions{
										SignalingInsecure: !secure,
										SignalingCreds:    Credentials{Type: "fake"},
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
				})
			}
		})
	}
}
