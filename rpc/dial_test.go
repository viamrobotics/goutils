package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/multierr"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestDial(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel1()
	_, err := Dial(ctx1, "127.0.0.1:1", logger, WithInsecure())
	test.That(t, err, test.ShouldResemble, context.DeadlineExceeded)
	cancel1()

	t.Run("unauthenticated", func(t *testing.T) {
		rpcServer, err := NewServer(logger)
		test.That(t, err, test.ShouldBeNil)

		httpListener, err := net.Listen("tcp", "localhost:0")
		test.That(t, err, test.ShouldBeNil)

		errChan := make(chan error)
		go func() {
			errChan <- rpcServer.Serve(httpListener)
		}()

		conn, err := Dial(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
		test.That(t, err, test.ShouldBeNil)
		test.That(t, conn.Close(), test.ShouldBeNil)

		conn, err = DialDirectGRPC(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
		test.That(t, err, test.ShouldBeNil)
		test.That(t, conn.Close(), test.ShouldBeNil)

		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
		err = <-errChan
		test.That(t, err, test.ShouldBeNil)
	})

	hosts := []string{"yeehaw", "woahthere"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var testMu sync.Mutex
			fakeAuthWorks := false

			httpListener, err := net.Listen("tcp", "localhost:0")
			test.That(t, err, test.ShouldBeNil)

			httpListenerExternal, err := net.Listen("tcp", "localhost:0")
			test.That(t, err, test.ShouldBeNil)

			privKeyExternal, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
			test.That(t, err, test.ShouldBeNil)

			externalSignalingHosts := make([]string, len(hosts))
			copy(externalSignalingHosts, hosts)
			externalSignalingHosts = append(externalSignalingHosts, "ext-only")

			internalSignalingHosts := make([]string, len(hosts))
			copy(internalSignalingHosts, hosts)
			internalSignalingHosts = append(internalSignalingHosts, "int-only")

			rpcServer, err := NewServer(
				logger,
				WithWebRTCServerOptions(WebRTCServerOptions{
					Enable:                   true,
					ExternalSignalingHosts:   externalSignalingHosts,
					InternalSignalingHosts:   internalSignalingHosts,
					EnableInternalSignaling:  true,
					ExternalSignalingAddress: httpListenerExternal.Addr().String(),
					ExternalSignalingDialOpts: []DialOption{
						WithInsecure(),
						WithCredentials(Credentials{Type: "fakeExtWithKey", Payload: "sosecret"}),
					},
				}),
				WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
					testMu.Lock()
					defer testMu.Unlock()
					if fakeAuthWorks {
						return map[string]string{}, nil
					}
					return nil, errors.New("this auth does not work yet")
				}, func(ctx context.Context, entity string) (interface{}, error) {
					return entity, nil
				})),
				WithAuthHandler("something", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
					testMu.Lock()
					defer testMu.Unlock()
					if fakeAuthWorks {
						return map[string]string{}, nil
					}
					return nil, errors.New("this auth does not work yet")
				}, func(ctx context.Context, entity string) (interface{}, error) {
					return entity, nil
				})),
				WithAuthHandler("fakeExtWithKey", WithPublicKeyProvider(
					func(ctx context.Context, entity string) (interface{}, error) {
						return entity, nil
					},
					&privKeyExternal.PublicKey,
				)),
				WithAuthHandler("inter-node", WithPublicKeyProvider(
					func(ctx context.Context, entity string) (interface{}, error) {
						if entity != "someent" {
							return nil, errors.New("not someent")
						}
						if ContextAuthMetadata(ctx)["some"] != "data" {
							return nil, errors.New("bad authed data")
						}
						return entity, nil
					},
					&privKeyExternal.PublicKey,
				)),
			)
			test.That(t, err, test.ShouldBeNil)

			err = rpcServer.RegisterServiceServer(
				context.Background(),
				&pb.EchoService_ServiceDesc,
				&echoserver.Server{},
				pb.RegisterEchoServiceHandlerFromEndpoint,
			)
			test.That(t, err, test.ShouldBeNil)

			var authToFail bool
			acceptedFakeWithKeyEnts := []string{"someotherthing", httpListenerExternal.Addr().String()}
			rpcServerExternal, err := NewServer(
				logger,
				WithAuthHandler("fakeExtWithKey", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
					var found bool
					for _, exp := range acceptedFakeWithKeyEnts {
						if exp == entity {
							found = true
							break
						}
					}
					if !found {
						return nil, errors.Errorf("wrong entity %q; wanted %v", entity, acceptedFakeWithKeyEnts)
					}
					if payload != "sosecret" {
						return nil, errors.New("wrong secret")
					}
					return map[string]string{}, nil
				}, func(ctx context.Context, entity string) (interface{}, error) {
					return entity, nil
				})),
				WithAuthRSAPrivateKey(privKeyExternal),
				WithAuthenticateToHandler(CredentialsType("inter-node"), func(ctx context.Context, entity string) (map[string]string, error) {
					if authToFail {
						return nil, errors.New("darn")
					}
					if entity != "someent" {
						return nil, errors.New("nope")
					}
					return map[string]string{"some": "data"}, nil
				}),
			)
			test.That(t, err, test.ShouldBeNil)

			signalingCallQueue := NewMemoryWebRTCCallQueue()
			test.That(t, rpcServerExternal.RegisterServiceServer(
				context.Background(),
				&webrtcpb.SignalingService_ServiceDesc,
				NewWebRTCSignalingServer(signalingCallQueue, nil, externalSignalingHosts...),
				webrtcpb.RegisterSignalingServiceHandlerFromEndpoint,
			), test.ShouldBeNil)

			errChan := make(chan error)
			go func() {
				errChan <- rpcServer.Serve(httpListener)
			}()
			go func() {
				errChan <- rpcServerExternal.Serve(httpListenerExternal)
			}()

			t.Run("no credentials provided", func(t *testing.T) {
				// this fails because WebRTC does some RPC.
				_, err = Dial(
					context.Background(),
					httpListener.Addr().String(),
					logger,
					WithDialDebug(),
					WithInsecure(),
				)
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)

				// this fails for the same reason.
				_, err = Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListener.Addr().String(),
						SignalingInsecure:      true,
					}))
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok = status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
			})

			testMu.Lock()
			fakeAuthWorks = true
			testMu.Unlock()

			t.Run("with credentials provided", func(t *testing.T) {
				conn, err := DialDirectGRPC(context.Background(), httpListener.Addr().String(), logger,
					WithDialDebug(),
					WithInsecure(),
					WithCredentials(Credentials{Type: "fake"}),
				)
				test.That(t, err, test.ShouldBeNil)

				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)

				conn, err = Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithInsecure(),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListener.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fake"},
					}),
				)
				test.That(t, err, test.ShouldBeNil)

				client = pb.NewEchoServiceClient(conn)
				echoResp, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit signaling server", func(t *testing.T) {
				conn, err := Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListener.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fake"},
					}),
				)
				test.That(t, err, test.ShouldBeNil)

				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit signaling server with exclusive host", func(t *testing.T) {
				conn, err := Dial(context.Background(), "int-only", logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListener.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fake"},
					}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit signaling server with unaccepted host", func(t *testing.T) {
				_, err := Dial(context.Background(), "ext-only", logger,
					WithDialDebug(),
					WithDisableDirectGRPC(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListener.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fake"},
					}),
				)
				test.That(t, err, test.ShouldBeError, ErrConnectionOptionsExhausted)
			})

			t.Run("explicit signaling server with external auth", func(t *testing.T) {
				conn, err := Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress:        httpListener.Addr().String(),
						SignalingInsecure:             true,
						SignalingCreds:                Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingExternalAuthAddress:  httpListenerExternal.Addr().String(),
						SignalingExternalAuthInsecure: true,
					}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit external signaling server", func(t *testing.T) {
				conn, err := Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListenerExternal.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingAuthEntity:    httpListenerExternal.Addr().String(),
					}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit external signaling server with exclusive host", func(t *testing.T) {
				conn, err := Dial(context.Background(), "ext-only", logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListenerExternal.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingAuthEntity:    httpListenerExternal.Addr().String(),
					}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			t.Run("explicit external signaling server with unaccepted host", func(t *testing.T) {
				_, err := Dial(context.Background(), "int-only", logger,
					WithDialDebug(),
					WithDisableDirectGRPC(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress: httpListenerExternal.Addr().String(),
						SignalingInsecure:      true,
						SignalingCreds:         Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingAuthEntity:    httpListenerExternal.Addr().String(),
					}),
				)
				test.That(t, err, test.ShouldBeError, ErrConnectionOptionsExhausted)
			})

			t.Run("explicit signaling server with external auth but bad secret", func(t *testing.T) {
				_, err = Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress:        httpListener.Addr().String(),
						SignalingInsecure:             true,
						SignalingCreds:                Credentials{Type: "fakeExtWithKey", Payload: "notsosecret"},
						SignalingExternalAuthAddress:  httpListenerExternal.Addr().String(),
						SignalingExternalAuthInsecure: true,
					}),
				)
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
				test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong secret")
			})

			t.Run("explicit signaling server with external auth plus auth to extension but bad ent", func(t *testing.T) {
				_, err = Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress:        httpListener.Addr().String(),
						SignalingInsecure:             true,
						SignalingCreds:                Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingExternalAuthAddress:  httpListenerExternal.Addr().String(),
						SignalingExternalAuthInsecure: true,
						SignalingExternalAuthToEntity: "something",
					}),
				)
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
				test.That(t, gStatus.Message(), test.ShouldContainSubstring, "nope")
			})

			t.Run("explicit signaling server with external auth plus auth to extension and good ent", func(t *testing.T) {
				conn, err := Dial(context.Background(), host, logger,
					WithDialDebug(),
					WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
					WithWebRTCOptions(DialWebRTCOptions{
						SignalingServerAddress:        httpListener.Addr().String(),
						SignalingInsecure:             true,
						SignalingCreds:                Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
						SignalingExternalAuthAddress:  httpListenerExternal.Addr().String(),
						SignalingExternalAuthInsecure: true,
						SignalingExternalAuthToEntity: "someent",
					}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})

			test.That(t, rpcServer.Stop(), test.ShouldBeNil)
			err = <-errChan
			test.That(t, err, test.ShouldBeNil)
			test.That(t, rpcServerExternal.Stop(), test.ShouldBeNil)
			err = <-errChan
			test.That(t, err, test.ShouldBeNil)
			test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
		})
	}
}

func TestDialExternalAuth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	httpListenerInternal, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	listenerInternalTCPAddr, ok := httpListenerInternal.Addr().(*net.TCPAddr)
	test.That(t, ok, test.ShouldBeTrue)
	listenerInternalPort := listenerInternalTCPAddr.Port
	internalAddr := fmt.Sprintf("localhost:%d", listenerInternalPort)

	httpListenerExternal, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	httpListenerExternal2, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	privKeyExternal, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)
	privKeyExternal2, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	rpcServerInternal, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{"yeehaw", internalAddr},
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
		WithAuthHandler("inter-node", WithPublicKeyProvider(
			func(ctx context.Context, entity string) (interface{}, error) {
				if entity != "someent" {
					return nil, errors.New("bad authed ent")
				}
				if ContextAuthMetadata(ctx)["some"] != "data" {
					return nil, errors.New("bad authed data")
				}
				return entity, nil
			},
			&privKeyExternal.PublicKey,
		)),
	)
	test.That(t, err, test.ShouldBeNil)
	internalExternalAuthSrv := &externalAuthServer{privKey: privKeyExternal}
	internalExternalAuthSrv.fail = true
	err = rpcServerInternal.RegisterServiceServer(
		context.Background(),
		&rpcpb.ExternalAuthService_ServiceDesc,
		internalExternalAuthSrv,
		rpcpb.RegisterExternalAuthServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	var authToFail bool
	acceptedFakeWithKeyEnts := []string{"someotherthing", httpListenerExternal.Addr().String()}
	rpcServerExternal, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{"yeehaw"},
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
		WithAuthHandler("fakeWithKey", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			var found bool
			for _, exp := range acceptedFakeWithKeyEnts {
				if exp == entity {
					found = true
					break
				}
			}
			if !found {
				return nil, errors.Errorf("wrong entity %q; wanted %v", entity, acceptedFakeWithKeyEnts)
			}
			if payload != "sosecret" {
				return nil, errors.New("wrong secret")
			}
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
		WithAuthRSAPrivateKey(privKeyExternal),
		WithAuthenticateToHandler(CredentialsType("inter-node"), func(ctx context.Context, entity string) (map[string]string, error) {
			if authToFail {
				return nil, errors.New("darn")
			}
			if entity != "someent" {
				return nil, errors.New("nope")
			}
			return map[string]string{"some": "data"}, nil
		}),
	)
	test.That(t, err, test.ShouldBeNil)

	rpcServerExternal2, err := NewServer(
		logger,
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
		WithAuthHandler("fakeWithKey", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
		WithAuthRSAPrivateKey(privKeyExternal2),
		WithAuthenticateToHandler(CredentialsType("inter-node"), func(ctx context.Context, entity string) (map[string]string, error) {
			if MustContextAuthEntity(ctx) != httpListenerExternal2.Addr().String() {
				return nil, errors.New("bad authed external entity")
			}
			if entity != "someent" {
				return nil, errors.New("nope")
			}
			return map[string]string{}, nil
		}),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServerInternal.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServerInternal.Serve(httpListenerInternal)
	}()
	go func() {
		errChan <- rpcServerExternal.Serve(httpListenerExternal)
	}()
	go func() {
		errChan <- rpcServerExternal2.Serve(httpListenerExternal2)
	}()

	testExternalAuth := func(
		t *testing.T,
		addr string,
		opts []DialOption,
		logger golog.Logger,
		errFunc func(t *testing.T, err error),
	) {
		t.Helper()

		opts = append(opts, WithDialDebug())

		conn, err := Dial(context.Background(), addr, logger, opts...)
		if errFunc == nil {
			test.That(t, err, test.ShouldBeNil)

			client := pb.NewEchoServiceClient(conn)
			echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
			test.That(t, conn.Close(), test.ShouldBeNil)
		} else {
			test.That(t, err, test.ShouldNotBeNil)
			errFunc(t, err)
		}

		opts = append(opts, WithWebRTCOptions(DialWebRTCOptions{Disable: true}))
		conn, err = Dial(context.Background(), addr, logger, opts...)
		test.That(t, err, test.ShouldBeNil)

		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})

		if errFunc == nil {
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		} else {
			test.That(t, err, test.ShouldNotBeNil)
			errFunc(t, err)
		}
	}

	t.Run("with external auth", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with external auth to localhost", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, internalAddr, opts, logger, nil)
	})

	t.Run("with external auth bad secret", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "notsosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong secret")
		})
	})

	t.Run("with no external auth entity provided", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), ""),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "no auth handler")
		})
	})

	t.Run("with unknown external entity", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "who"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "nope")
		})
	})

	t.Run("with expected external entity", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithEntityCredentials("someotherthing", Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with unexpected external entity", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithEntityCredentials("wrongthing", Credentials{Type: "fakeWithKey"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong entity")
		})
	})

	t.Run("with external auth where service fails", func(t *testing.T) {
		prevFail := authToFail
		authToFail = true
		defer func() {
			authToFail = prevFail
		}()
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "darn")
		})
	})

	t.Run("with external auth but mismatched keys", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerExternal2.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "verification error")
		})
	})

	t.Run("with external auth set to same internal", func(t *testing.T) {
		prevFail := internalExternalAuthSrv.fail
		internalExternalAuthSrv.fail = false
		defer func() {
			internalExternalAuthSrv.fail = prevFail
		}()
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with external auth set authenticating to wrong entity", func(t *testing.T) {
		prevFail := internalExternalAuthSrv.fail
		prevEnt := internalExternalAuthSrv.expectedEnt
		internalExternalAuthSrv.fail = false
		internalExternalAuthSrv.expectedEnt = "somethingwrong"
		defer func() {
			internalExternalAuthSrv.fail = prevFail
			internalExternalAuthSrv.expectedEnt = prevEnt
		}()
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), internalExternalAuthSrv.expectedEnt),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "bad authed ent")
		})
	})

	t.Run("with external auth setting wrong metadata", func(t *testing.T) {
		prevFail := internalExternalAuthSrv.fail
		prevNoMetadata := internalExternalAuthSrv.noMetadata
		prevEnt := internalExternalAuthSrv.expectedEnt
		internalExternalAuthSrv.fail = false
		internalExternalAuthSrv.noMetadata = true
		internalExternalAuthSrv.expectedEnt = ""
		defer func() {
			internalExternalAuthSrv.fail = prevFail
			internalExternalAuthSrv.noMetadata = prevNoMetadata
			internalExternalAuthSrv.expectedEnt = prevEnt
		}()
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "bad authed data")
		})
	})

	test.That(t, rpcServerInternal.Stop(), test.ShouldBeNil)
	test.That(t, rpcServerExternal.Stop(), test.ShouldBeNil)
	test.That(t, rpcServerExternal2.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestDialNoSignalerPresent(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	rpcServer, err := NewServer(logger, WithUnauthenticated())
	test.That(t, err, test.ShouldBeNil)

	signalingServer := &injectSignalingServer{}
	test.That(t, rpcServer.RegisterServiceServer(
		context.Background(),
		&webrtcpb.SignalingService_ServiceDesc,
		signalingServer,
		webrtcpb.RegisterSignalingServiceHandlerFromEndpoint,
	), test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := Dial(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, signalingServer.callCount, test.ShouldEqual, 1)

	conn, err = DialDirectGRPC(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, signalingServer.callCount, test.ShouldEqual, 1)

	signalingServer.returnHostNotAllowedMsg = true

	conn, err = Dial(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, signalingServer.callCount, test.ShouldEqual, 2)

	conn, err = DialDirectGRPC(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)
	test.That(t, signalingServer.callCount, test.ShouldEqual, 2)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestDialFixupWebRTCOptions(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{Enable: true}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			if entity != "passmethrough" {
				return nil, errors.New("nope")
			}
			return map[string]string{}, nil
		}, func(ctx context.Context, entity string) (interface{}, error) {
			return entity, nil
		})),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Start(), test.ShouldBeNil)

	conn, err := Dial(
		context.Background(),
		rpcServer.InstanceNames()[0],
		logger,
		WithInsecure(),
		WithDialDebug(),
		WithDisableDirectGRPC(),
		WithEntityCredentials("passmethrough", Credentials{Type: "fake"}),
	)
	test.That(t, err, test.ShouldBeNil)
	client := pb.NewEchoServiceClient(conn)
	echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
}

func TestDialMulticastDNS(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	t.Run("unauthenticated", func(t *testing.T) {
		rpcServer, err := NewServer(
			logger,
			WithUnauthenticated(),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, rpcServer.Start(), test.ShouldBeNil)
		test.That(t, rpcServer.InstanceNames(), test.ShouldHaveLength, 1)

		conn, err := Dial(
			context.Background(),
			rpcServer.InstanceNames()[0],
			logger,
			WithInsecure(),
			WithDialDebug(),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, conn.Close(), test.ShouldBeNil)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = Dial(
			ctx,
			rpcServer.InstanceNames()[0],
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithDialMulticastDNSOptions(DialMulticastDNSOptions{Disable: true}),
		)
		test.That(t, err, test.ShouldResemble, context.DeadlineExceeded)

		test.That(t, rpcServer.Stop(), test.ShouldBeNil)

		rpcServer, err = NewServer(
			logger,
			WithUnauthenticated(),
			WithWebRTCServerOptions(WebRTCServerOptions{Enable: true}),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, rpcServer.Start(), test.ShouldBeNil)

		conn, err = Dial(
			context.Background(),
			rpcServer.InstanceNames()[0],
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithDisableDirectGRPC(),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, conn.Close(), test.ShouldBeNil)

		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	})

	t.Run("authenticated", func(t *testing.T) {
		rpcServer, err := NewServer(
			logger,
			WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
			}, func(ctx context.Context, entity string) (interface{}, error) {
				return entity, nil
			})),
		)
		test.That(t, err, test.ShouldBeNil)

		err = rpcServer.RegisterServiceServer(
			context.Background(),
			&pb.EchoService_ServiceDesc,
			&echoserver.Server{},
			pb.RegisterEchoServiceHandlerFromEndpoint,
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
		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, conn.Close(), test.ShouldBeNil)

		conn, err = Dial(
			context.Background(),
			rpcServer.InstanceNames()[0],
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithCredentials(Credentials{Type: "fake"}),
			WithDialMulticastDNSOptions(DialMulticastDNSOptions{RemoveAuthCredentials: true}),
		)
		test.That(t, err, test.ShouldBeNil)
		client = pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok = status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, conn.Close(), test.ShouldBeNil)

		conn, err = Dial(
			context.Background(),
			rpcServer.InstanceNames()[0],
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithCredentials(Credentials{Type: "fake"}),
		)
		test.That(t, err, test.ShouldBeNil)
		client = pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		test.That(t, conn.Close(), test.ShouldBeNil)

		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	})

	t.Run("authenticated with names", func(t *testing.T) {
		names := []string{primitive.NewObjectID().Hex(), primitive.NewObjectID().Hex()}
		rpcServer, err := NewServer(
			logger,
			WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
			}, func(ctx context.Context, entity string) (interface{}, error) {
				return entity, nil
			})),
			WithInstanceNames(names...),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, rpcServer.InstanceNames(), test.ShouldResemble, names)

		err = rpcServer.RegisterServiceServer(
			context.Background(),
			&pb.EchoService_ServiceDesc,
			&echoserver.Server{},
			pb.RegisterEchoServiceHandlerFromEndpoint,
		)
		test.That(t, err, test.ShouldBeNil)

		test.That(t, rpcServer.Start(), test.ShouldBeNil)

		for idx, name := range names {
			t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
				conn, err := Dial(
					context.Background(),
					name,
					logger,
					WithInsecure(),
					WithDialDebug(),
					WithCredentials(Credentials{Type: "fake"}),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)
			})
		}

		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	})
}

func TestDialMutualTLSAuth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	cert, certFile, keyFile, certPool, err := testutils.GenerateSelfSignedCertificate("somename", "altname")
	test.That(t, err, test.ShouldBeNil)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	test.That(t, err, test.ShouldBeNil)

	tlsConfig := &tls.Config{
		RootCAs:      certPool,
		ClientCAs:    certPool,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	for _, viaServerServe := range []bool{false, true} {
		tcName := "via server start"
		if viaServerServe {
			tcName = "via server serve"
		}

		makeServer := func(differentTLSNames ...string) (string, func() error, error) {
			tlsNames := leaf.DNSNames
			if len(differentTLSNames) != 0 {
				tlsNames = differentTLSNames
			}
			var server Server
			var err error
			opts := []ServerOption{WithTLSAuthHandler(tlsNames, func(ctx context.Context, ents ...string) (interface{}, error) {
				return "somespecialinterface", nil
			})}
			if viaServerServe {
				server, err = NewServer(
					logger,
					opts...,
				)
			} else {
				opts = append(opts, WithInternalTLSConfig(tlsConfig))
				server, err = NewServer(
					logger,
					opts...,
				)
			}
			if err != nil {
				return "", nil, err
			}

			echoServer := &echoserver.Server{ContextAuthEntity: MustContextAuthEntity}
			echoServer.SetAuthorized(true)
			server.RegisterServiceServer(
				context.Background(),
				&pb.EchoService_ServiceDesc,
				echoServer,
				pb.RegisterEchoServiceHandlerFromEndpoint,
			)

			if viaServerServe {
				httpListener, err := net.Listen("tcp", "localhost:0")
				if err != nil {
					return "", nil, err
				}

				tlsConfig := tlsConfig.Clone()
				tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
				httpServer := &http.Server{
					ReadTimeout:    10 * time.Second,
					MaxHeaderBytes: MaxMessageSize,
					TLSConfig:      tlsConfig,
				}
				httpServer.Addr = httpListener.Addr().String()
				httpServer.Handler = server.GRPCHandler()

				errChan := make(chan error)
				go func() {
					serveErr := httpServer.ServeTLS(httpListener, certFile, keyFile)
					if serveErr != nil && errors.Is(serveErr, http.ErrServerClosed) {
						serveErr = nil
					}
					errChan <- serveErr
				}()
				return httpListener.Addr().String(), func() error {
					return multierr.Combine(httpServer.Shutdown(context.Background()), <-errChan, server.Stop())
				}, nil
			}

			if err := server.Start(); err != nil {
				return "", nil, err
			}
			return server.InternalAddr().String(), server.Stop, nil
		}

		t.Run(tcName, func(t *testing.T) {
			t.Run("unauthenticated", func(t *testing.T) {
				serverAddr, stopServer, err := makeServer()
				test.That(t, err, test.ShouldBeNil)

				tlsConfig := tlsConfig.Clone()
				tlsConfig.Certificates = nil
				tlsConfig.ServerName = "somename"
				conn, err := Dial(
					context.Background(),
					serverAddr,
					logger,
					WithDialDebug(),
					WithTLSConfig(tlsConfig),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
				test.That(t, conn.Close(), test.ShouldBeNil)

				test.That(t, stopServer(), test.ShouldBeNil)
			})

			t.Run("authenticated", func(t *testing.T) {
				serverAddr, stopServer, err := makeServer()
				test.That(t, err, test.ShouldBeNil)

				tlsConfig := tlsConfig.Clone()
				tlsConfig.ServerName = "somename"
				conn, err := Dial(
					context.Background(),
					serverAddr,
					logger,
					WithDialDebug(),
					WithTLSConfig(tlsConfig),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)

				tlsConfig.ServerName = "altname"
				conn, err = Dial(
					context.Background(),
					serverAddr,
					logger,
					WithDialDebug(),
					WithTLSConfig(tlsConfig),
				)
				test.That(t, err, test.ShouldBeNil)
				client = pb.NewEchoServiceClient(conn)
				echoResp, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
				test.That(t, conn.Close(), test.ShouldBeNil)

				test.That(t, stopServer(), test.ShouldBeNil)
			})

			t.Run("verified cert but unaccepted", func(t *testing.T) {
				serverAddr, stopServer, err := makeServer("unknown-name")
				test.That(t, err, test.ShouldBeNil)

				tlsConfig := tlsConfig.Clone()
				tlsConfig.ServerName = "somename"
				conn, err := Dial(
					context.Background(),
					serverAddr,
					logger,
					WithDialDebug(),
					WithTLSConfig(tlsConfig),
				)
				test.That(t, err, test.ShouldBeNil)
				client := pb.NewEchoServiceClient(conn)
				_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
				test.That(t, conn.Close(), test.ShouldBeNil)

				test.That(t, stopServer(), test.ShouldBeNil)
			})
		})
	}
}

type injectSignalingServer struct {
	webrtcpb.UnimplementedSignalingServiceServer
	callCount               int
	returnHostNotAllowedMsg bool
}

func (srv *injectSignalingServer) OptionalWebRTCConfig(
	ctx context.Context,
	req *webrtcpb.OptionalWebRTCConfigRequest,
) (*webrtcpb.OptionalWebRTCConfigResponse, error) {
	srv.callCount++
	if srv.returnHostNotAllowedMsg {
		return nil, status.Error(codes.InvalidArgument, hostNotAllowedMsg)
	}
	return srv.UnimplementedSignalingServiceServer.OptionalWebRTCConfig(ctx, req)
}

type externalAuthServer struct {
	rpcpb.ExternalAuthServiceServer
	fail        bool
	expectedEnt string
	noMetadata  bool
	privKey     *rsa.PrivateKey
}

func (svc *externalAuthServer) AuthenticateTo(
	_ context.Context,
	req *rpcpb.AuthenticateToRequest,
) (*rpcpb.AuthenticateToResponse, error) {
	if svc.fail {
		return nil, errors.New("darn")
	}
	if svc.expectedEnt != "" {
		if svc.expectedEnt != req.Entity {
			return nil, errors.New("nope unexpected")
		}
	} else if req.Entity != "someent" {
		return nil, errors.New("nope")
	}
	authMetadata := map[string]string{"some": "data"}
	if svc.noMetadata {
		authMetadata = nil
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{req.Entity},
		},
		CredentialsType: CredentialsType("inter-node"),
		AuthMetadata:    authMetadata,
	})

	tokenString, err := token.SignedString(svc.privKey)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "failed to ext")
	}

	return &rpcpb.AuthenticateToResponse{
		AccessToken: tokenString,
	}, nil
}
