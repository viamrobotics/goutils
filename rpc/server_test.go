package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestServer(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	_, certFile, keyFile, certPool, err := testutils.GenerateSelfSignedCertificate("localhost")
	test.That(t, err, test.ShouldBeNil)

	testPrivKey, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)

	hosts := []string{"yeehaw", "woahthere"}
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("with secure=%t", secure), func(t *testing.T) {
			for _, host := range hosts {
				t.Run(host, func(t *testing.T) {
					for _, withAuthentication := range []bool{false, true} {
						t.Run(fmt.Sprintf("with authentication=%t", withAuthentication), func(t *testing.T) {
							serverOpts := []ServerOption{
								WithWebRTCServerOptions(WebRTCServerOptions{
									Enable:                 true,
									InternalSignalingHosts: hosts,
								}),
							}
							if withAuthentication {
								serverOpts = append(serverOpts,
									WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
										return map[string]string{}, nil
									})),
									WithAuthRSAPrivateKey(testPrivKey),
								)
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
									errChan <- rpcServer.ServeTLS(listener, certFile, keyFile, &tls.Config{
										MinVersion: tls.VersionTLS12,
										RootCAs:    certPool,
									})
								} else {
									errChan <- rpcServer.Serve(listener)
								}
							}()

							// standard grpc
							tlsConf := &tls.Config{
								MinVersion: tls.VersionTLS12,
								RootCAs:    certPool,
								ServerName: "localhost",
							}
							grpcOpts := []grpc.DialOption{grpc.WithBlock()}
							if secure {
								grpcOpts = append(
									grpcOpts,
									grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)),
								)
							} else {
								grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
								test.That(t, err.Error(), test.ShouldContainSubstring, "do not know how")

								authResp, err := authClient.Authenticate(
									context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
										Type:    "fake",
										Payload: "something",
									}})
								test.That(t, err, test.ShouldBeNil)

								// Validate the JWT token/header from the Authenticate call.
								token, err := jwt.Parse(authResp.AccessToken, func(token *jwt.Token) (interface{}, error) {
									return &testPrivKey.PublicKey, nil
								})
								test.That(t, err, test.ShouldBeNil)
								thumbprint, err := RSAPublicKeyThumbprint(&testPrivKey.PublicKey)
								test.That(t, err, test.ShouldBeNil)
								test.That(t, token.Header["kid"], test.ShouldEqual, thumbprint)

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
							rd, err := io.ReadAll(httpResp1.Body)
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
								rd, err = io.ReadAll(httpResp2.Body)
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
							rd, err = io.ReadAll(httpResp2.Body)
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
							_, err = dialWebRTC(context.Background(), listener.Addr().String(), "", &dialOptions{
								tlsConfig: tlsConf,
								webrtcOpts: DialWebRTCOptions{
									SignalingInsecure: !secure,
								},
								webrtcOptsSet: true,
							}, logger)
							test.That(t, err, test.ShouldNotBeNil)
							if withAuthentication {
								test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
							} else {
								test.That(t, err.Error(), test.ShouldContainSubstring, "non-empty rpc-host")
							}

							rtcConn, err := dialWebRTC(context.Background(), listener.Addr().String(), host, &dialOptions{
								tlsConfig: tlsConf,
								webrtcOpts: DialWebRTCOptions{
									SignalingInsecure: !secure,
								},
								webrtcOptsSet: true,
							}, logger)
							if withAuthentication {
								test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
								rtcConn, err = dialWebRTC(context.Background(), listener.Addr().String(), host, &dialOptions{
									tlsConfig: tlsConf,
									webrtcOpts: DialWebRTCOptions{
										SignalingInsecure: !secure,
										SignalingCreds:    Credentials{Type: "fake"},
									},
									webrtcOptsSet: true,
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

func TestServerWithInternalBindAddress(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	port, err := utils.TryReserveRandomPort()
	test.That(t, err, test.ShouldBeNil)

	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)
	rpcServer, err := NewServer(logger, WithUnauthenticated(), WithInternalBindAddress(bindAddr))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, rpcServer.Start(), test.ShouldBeNil)
	test.That(t, rpcServer.InternalAddr().String(), test.ShouldEqual, bindAddr)

	conn, err := Dial(context.Background(), bindAddr, logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
}

func TestServerWithExternalListenerAddress(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	listenerAddr, ok := listener.Addr().(*net.TCPAddr)
	test.That(t, ok, test.ShouldBeTrue)
	rpcServer, err := NewServer(
		logger,
		WithUnauthenticated(),
		WithExternalListenerAddress(listenerAddr),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, rpcServer.InternalAddr().String(), test.ShouldNotEqual, listener.Addr().String())

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	// prove mDNS is broadcasting our listener address
	conn, err := Dial(
		context.Background(),
		rpcServer.InstanceNames()[0],
		logger,
		WithInsecure(),
		WithDialDebug(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.(*grpc.ClientConn).Target(), test.ShouldEqual, listener.Addr().String())
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestServerMutlicastDNS(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	rpcServer, err := NewServer(
		logger,
		WithUnauthenticated(),
	)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Start(), test.ShouldBeNil)

	conn, err := Dial(
		context.Background(),
		rpcServer.InstanceNames()[0],
		logger,
		WithInsecure(),
		WithDialDebug(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
}

func TestServerWithDisableMulticastDNS(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	rpcServer, err := NewServer(
		logger,
		WithUnauthenticated(),
		WithDisableMulticastDNS(),
	)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Start(), test.ShouldBeNil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = Dial(
		ctx,
		rpcServer.InstanceNames()[0],
		logger,
		WithInsecure(),
		WithDialDebug(),
	)
	test.That(t, err, test.ShouldResemble, context.DeadlineExceeded)

	conn, err := Dial(
		context.Background(),
		rpcServer.InternalAddr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
}

func TestServerUnauthenticatedOption(t *testing.T) {
	logger := golog.NewTestLogger(t)

	_, err := NewServer(
		logger,
		WithUnauthenticated(),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
	)
	test.That(t, err, test.ShouldEqual, errMixedUnauthAndAuth)
}

type fakeStatsHandler struct {
	serverConnections int
	clientConnections int
	mu                sync.Mutex
}

func (s *fakeStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	return ctx
}
func (s *fakeStatsHandler) HandleRPC(ctx context.Context, info stats.RPCStats) {}
func (s *fakeStatsHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn processes the Conn stats.
func (s *fakeStatsHandler) HandleConn(ctx context.Context, info stats.ConnStats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if info.IsClient() {
		if _, ok := info.(*stats.ConnBegin); ok {
			s.clientConnections++
		}
	} else {
		if _, ok := info.(*stats.ConnBegin); ok {
			s.serverConnections++
		}
	}
}

func TestWithStatsHandler(t *testing.T) {
	logger := golog.NewTestLogger(t)

	handler := fakeStatsHandler{}

	rpcServer, err := NewServer(
		logger,
		WithUnauthenticated(),
		WithStatsHandler(&handler),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, rpcServer.Start(), test.ShouldBeNil)

	conn, err := Dial(
		context.Background(),
		rpcServer.InternalAddr().String(),
		logger,
		WithInsecure(),
		WithDialDebug(),
	)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, handler.serverConnections, test.ShouldBeGreaterThan, 1)

	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
}
