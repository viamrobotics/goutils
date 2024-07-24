package rpc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/multierr"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	webrtcpb "go.viam.com/utils/proto/rpc/webrtc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestDialWithMemoryQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	signalingCallQueue := NewMemoryWebRTCCallQueue(logger)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testDial(t, signalingCallQueue, logger)
}

func TestDialWithMongoDBQueue(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)
	client := testutils.BackingMongoDBClient(t)
	test.That(t, client.Database(mongodbWebRTCCallQueueDBName).Drop(context.Background()), test.ShouldBeNil)
	signalingCallQueue, err := NewMongoDBWebRTCCallQueue(context.Background(), uuid.NewString(), 50, client, logger,
		func(hosts []string, atTime time.Time) {})
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, signalingCallQueue.Close(), test.ShouldBeNil)
	}()
	testDial(t, signalingCallQueue, logger)
}

//nolint:thelper
func testDial(t *testing.T, signalingCallQueue WebRTCCallQueue, logger utils.ZapCompatibleLogger) {
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

			pubKeyExternal, privKeyExternal, err := ed25519.GenerateKey(rand.Reader)
			test.That(t, err, test.ShouldBeNil)

			externalSignalingHosts := make([]string, len(hosts))
			copy(externalSignalingHosts, hosts)
			externalSignalingHosts = append(externalSignalingHosts, "ext-only")

			internalSignalingHosts := make([]string, len(hosts))
			copy(internalSignalingHosts, hosts)
			internalSignalingHosts = append(internalSignalingHosts, "int-only")

			rpcServer, err := NewServer(
				logger,
				// we are both some UUID and somesub as far as an audience goes
				WithInstanceNames(uuid.NewString(), "somesub"),
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
				WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
					testMu.Lock()
					defer testMu.Unlock()
					if fakeAuthWorks {
						return map[string]string{}, nil
					}
					return nil, errors.New("this auth does not work yet")
				})),
				WithAuthHandler("something", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
					testMu.Lock()
					defer testMu.Unlock()
					if fakeAuthWorks {
						return map[string]string{}, nil
					}
					return nil, errors.New("this auth does not work yet")
				})),
				WithExternalAuthEd25519PublicKeyTokenVerifier(pubKeyExternal),
			)
			test.That(t, err, test.ShouldBeNil)

			echoServer := &echoserver.Server{
				MustContextAuthEntity: func(ctx context.Context) echoserver.RPCEntityInfo {
					ent := MustContextAuthEntity(ctx)
					return echoserver.RPCEntityInfo{
						Entity: ent.Entity,
						Data:   ent.Data,
					}
				},
			}
			err = rpcServer.RegisterServiceServer(
				context.Background(),
				&pb.EchoService_ServiceDesc,
				echoServer,
				pb.RegisterEchoServiceHandlerFromEndpoint,
			)
			test.That(t, err, test.ShouldBeNil)

			var authToFail bool
			acceptedFakeWithKeyEnts := []string{"someotherthing", httpListenerExternal.Addr().String()}
			keyOpt, keyID := WithAuthED25519PrivateKey(privKeyExternal)
			test.That(t, keyID, test.ShouldEqual, base64.RawURLEncoding.EncodeToString(privKeyExternal.Public().(ed25519.PublicKey)))
			rpcServerExternal, err := NewServer(
				logger,
				WithAuthHandler("fakeExtWithKey", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
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
				})),
				keyOpt,
				WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
					if authToFail {
						return nil, errors.New("darn")
					}
					if entity != "somesub" {
						return nil, errors.New("nope")
					}
					return map[string]string{"some": "data"}, nil
				}),
			)
			test.That(t, err, test.ShouldBeNil)

			signalingServer := NewWebRTCSignalingServer(signalingCallQueue, nil, logger, externalSignalingHosts...)
			test.That(t, rpcServerExternal.RegisterServiceServer(
				context.Background(),
				&webrtcpb.SignalingService_ServiceDesc,
				signalingServer,
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
				echoServer.SetAuthorized(true)
				defer echoServer.SetAuthorized(false)
				echoServer.SetExpectedAuthEntity(httpListener.Addr().String())

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

				// TODO(GOUT-11): Once auth is handled, we can expect the same host for both gRPC and
				// WebRTC based connections.
				echoServer.SetExpectedAuthEntity(strings.Join(internalSignalingHosts, ":"))
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

			t.Run("explicit signaling server with external auth but no auth to set", func(t *testing.T) {
				_, err := Dial(context.Background(), host, logger,
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
				test.That(t, err, test.ShouldNotBeNil)
				test.That(t, err.Error(), test.ShouldContainSubstring, "no authenticate to option")
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
						SignalingExternalAuthToEntity: "does not matter",
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
						SignalingExternalAuthToEntity: "somesub",
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
			signalingServer.Close()
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

	pubKeyInternal, privKeyInternal, err := ed25519.GenerateKey(rand.Reader)
	test.That(t, err, test.ShouldBeNil)
	pubKeyExternal, privKeyExternal, err := ed25519.GenerateKey(rand.Reader)
	test.That(t, err, test.ShouldBeNil)
	pubKeyExternal2, privKeyExternal2, err := ed25519.GenerateKey(rand.Reader)
	test.That(t, err, test.ShouldBeNil)

	internalAudience := []string{"int-aud2", "int-aud1", "int-aud3"}
	keyOpt, _ := WithAuthED25519PrivateKey(privKeyInternal)
	rpcServerInternal, err := NewServer(
		logger,
		// we are both some UUID and somesub as far as an audience goes
		WithAuthAudience(internalAudience...),
		keyOpt,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{"yeehaw", internalAddr},
		}),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),

		// manually setting up external so we can look at auth data
		WithEntityDataLoader(CredentialsTypeExternal,
			EntityDataLoaderFunc(func(ctx context.Context, claims Claims) (interface{}, error) {
				if claims.Metadata()["some"] != "data" {
					return nil, errors.New("bad authed data")
				}
				return claims.Entity(), nil
			})),
		WithTokenVerificationKeyProvider(CredentialsTypeExternal,
			MakeEd25519PublicKeyProvider(pubKeyExternal),
		),
	)
	test.That(t, err, test.ShouldBeNil)
	internalExternalAuthSrv := &externalAuthServer{
		privKey:     privKeyExternal,
		expectedAud: internalAudience,
	}
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
	keyOptExternal, _ := WithAuthED25519PrivateKey(privKeyExternal)
	rpcServerExternal, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{"yeehaw"},
		}),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
		WithAuthHandler("fakeWithKey", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
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
		})),
		keyOptExternal,
		WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
			if authToFail {
				return nil, errors.New("darn")
			}
			var ok bool
			for _, ent := range internalAudience {
				if ent == entity {
					ok = true
					break
				}
			}
			if !ok {
				return nil, errors.New("nope")
			}
			return map[string]string{"some": "data"}, nil
		}),
	)
	test.That(t, err, test.ShouldBeNil)

	keyOptExternal2, _ := WithAuthED25519PrivateKey(privKeyExternal2)
	rpcServerExternal2, err := NewServer(
		logger,
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
		WithAuthHandler("fakeWithKey", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
		keyOptExternal2,
		WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
			var ok bool
			for _, ent := range internalAudience {
				if ent == entity {
					ok = true
					break
				}
			}
			if !ok {
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
		logger utils.ZapCompatibleLogger,
		errFunc func(t *testing.T, err error),
	) {
		// t.Helper()

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
			return
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
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud3"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with external auth to localhost", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud2"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, internalAddr, opts, logger, nil)
	})

	t.Run("with external auth bad secret", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "notsosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud1"),
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
			test.That(t, err.Error(), test.ShouldContainSubstring, "no authenticate to option")
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
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud2"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with unexpected external entity", func(t *testing.T) {
		opts := []DialOption{
			WithInsecure(),
			WithEntityCredentials("wrongthing", Credentials{Type: "fakeWithKey"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "somesub"),
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
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud1"),
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
			WithExternalAuth(httpListenerExternal2.Addr().String(), "int-aud2"),
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
			WithExternalAuth(httpListenerInternal.Addr().String(), "int-aud1"),
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
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "invalid audience")
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
			WithExternalAuth(httpListenerInternal.Addr().String(), "int-aud2"),
			WithExternalAuthInsecure(),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Internal)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "bad authed data")
		})
	})

	t.Run("with signaling external auth material", func(t *testing.T) {
		// rpcServerExternal.InstanceNames()[0] is the implicit audience
		accessToken := signTestAuthToken(t, pubKeyExternal, privKeyExternal, rpcServerExternal.InstanceNames()[0], "sub1", "fake")
		opts := []DialOption{
			WithInsecure(),
			WithExternalAuthInsecure(),
			WithStaticExternalAuthenticationMaterial(accessToken),
			WithExternalAuth(httpListenerExternal.Addr().String(), "int-aud3"),
			WithWebRTCOptions(DialWebRTCOptions{
				// disable auto detect, explicitly set external auth for signaler by passing
				// sending external auth static auth material to the signaler.
				AllowAutoDetectAuthOptions:        false,
				SignalingServerAddress:            httpListenerInternal.Addr().String(),
				SignalingExternalAuthAddress:      httpListenerExternal.Addr().String(),
				SignalingAuthEntity:               "test",
				SignalingExternalAuthToEntity:     "int-aud2",
				SignalingExternalAuthInsecure:     true,
				SignalingExternalAuthAuthMaterial: accessToken,
				SignalingInsecure:                 true,
			}),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with external auth material for external auth and signaler", func(t *testing.T) {
		internalExternalAuthSrv.fail = false
		accessToken := signTestAuthToken(t, pubKeyInternal, privKeyInternal, "int-aud1", "sub1", "fake")
		opts := []DialOption{
			WithInsecure(),
			WithExternalAuthInsecure(),
			// used for both signaler and skips AuthenticateTo step
			WithStaticExternalAuthenticationMaterial(accessToken),
			WithExternalAuth(httpListenerInternal.Addr().String(), "int-aud1"),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, nil)
	})

	t.Run("with external auth material for external auth and signaler with invalid key", func(t *testing.T) {
		internalExternalAuthSrv.fail = false
		accessToken := signTestAuthToken(t, pubKeyExternal2, privKeyExternal2, "aud1", "sub1", "fake")
		opts := []DialOption{
			WithInsecure(),
			WithExternalAuthInsecure(),
			// used for both signaler and skips AuthenticateTo step
			WithStaticExternalAuthenticationMaterial(accessToken),
			WithExternalAuth(httpListenerInternal.Addr().String(), "int-aud2"),
		}
		testExternalAuth(t, httpListenerInternal.Addr().String(), opts, logger, func(t *testing.T, err error) {
			t.Helper()
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, " this server did not sign this JWT")
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

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	listenerAddr, ok := listener.Addr().(*net.TCPAddr)
	test.That(t, ok, test.ShouldBeTrue)

	rpcServer, err := NewServer(
		logger,
		WithExternalListenerAddress(listenerAddr),
		WithDisableMulticastDNS(),
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:                 true,
			InternalSignalingHosts: []string{listenerAddr.String()},
		}),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			if entity != "passmethrough" {
				return nil, errors.New("nope")
			}
			return map[string]string{}, nil
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

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(listener)
	}()

	t.Run("auto detect with no signaling server address", func(t *testing.T) {
		conn, err := Dial(
			context.Background(),
			listenerAddr.String(),
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
	})

	t.Run("auto detect with a signaling server address", func(t *testing.T) {
		conn, err := Dial(
			context.Background(),
			listenerAddr.String(),
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithDisableDirectGRPC(),
			WithEntityCredentials("passmethrough", Credentials{Type: "fake"}),
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingServerAddress:     listenerAddr.String(),
				AllowAutoDetectAuthOptions: true,
				SignalingInsecure:          true,
			}),
		)
		test.That(t, err, test.ShouldBeNil)
		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		test.That(t, conn.Close(), test.ShouldBeNil)
	})

	t.Run("no auto detect with a signaling server address", func(t *testing.T) {
		_, err := Dial(
			context.Background(),
			listenerAddr.String(),
			logger,
			WithInsecure(),
			WithDialDebug(),
			WithDisableDirectGRPC(),
			WithEntityCredentials("passmethrough", Credentials{Type: "fake"}),
			WithWebRTCOptions(DialWebRTCOptions{
				SignalingServerAddress: listenerAddr.String(),
				SignalingInsecure:      true,
			}),
		)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "authentication required")
	})

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestDialFixupWebRTCOptionsMDNS(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{Enable: true}),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			if entity != "passmethrough" {
				return nil, errors.New("nope")
			}
			return map[string]string{}, nil
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

	t.Run("fix mdns instance name", func(t *testing.T) {
		rpcServer, err := NewServer(
			logger,
			WithUnauthenticated(),
			WithInstanceNames("this.is.a.test.cloud"),
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

		conn, err = Dial(
			context.Background(),
			strings.ReplaceAll(rpcServer.InstanceNames()[0], ".", "-"),
			logger,
			WithInsecure(),
			WithDialDebug(),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, conn.Close(), test.ShouldBeNil)
		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	})

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
			WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
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
			WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
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
			opts := []ServerOption{WithTLSAuthHandler(tlsNames)}
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

			echoServer := &echoserver.Server{
				MustContextAuthEntity: func(ctx context.Context) echoserver.RPCEntityInfo {
					ent := MustContextAuthEntity(ctx)
					return echoserver.RPCEntityInfo{
						Entity: ent.Entity,
						Data:   ent.Data,
					}
				},
			}
			echoServer.SetExpectedAuthEntity(leaf.Issuer.String() + ":" + leaf.SerialNumber.String())
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

func TestDialForceDirect(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(
		logger,
		WithDisableMulticastDNS(),
		WithWebRTCServerOptions(WebRTCServerOptions{Enable: false}),
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel1()
	conn, err := Dial(ctx1, httpListener.Addr().String(), logger, WithForceDirectGRPC(), WithInsecure())
	test.That(t, err, test.ShouldBeNil)
	cancel1()
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestDialUnix(t *testing.T) {
	logger := golog.NewTestLogger(t)
	rpcServer, err := NewServer(
		logger,
		WithDisableMulticastDNS(),
		WithWebRTCServerOptions(WebRTCServerOptions{Enable: false}),
	)
	test.That(t, err, test.ShouldBeNil)

	dir, err := os.MkdirTemp("", "viam-test-*")
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, os.RemoveAll(dir), test.ShouldBeNil)
	}()
	socketPath := filepath.ToSlash(filepath.Join(dir, "test.sock"))
	if runtime.GOOS == "windows" {
		// on windows, we need to craft a good enough looking URL for gRPC which
		// means we need to take out the volume which will have the current drive
		// be used. In a client server relationship for windows dialing, this must
		// be known. That is, if this is a multi process UDS, then for the purposes
		// of dialing without any resolver modifications to gRPC, they must initially
		// agree on using the same drive.
		socketPath = socketPath[2:]
	}

	httpListener, err := net.Listen("unix", socketPath)
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	conn, err := Dial(ctx, "unix://"+socketPath, logger)
	cancel()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
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
	expectedAud []string
	noMetadata  bool
	privKey     ed25519.PrivateKey
}

func (svc *externalAuthServer) AuthenticateTo(
	ctx context.Context,
	req *rpcpb.AuthenticateToRequest,
) (*rpcpb.AuthenticateToResponse, error) {
	if svc.fail {
		return nil, errors.New("darn 1")
	}
	if svc.expectedEnt != "" {
		if svc.expectedEnt != req.Entity {
			return nil, errors.New("nope unexpected")
		}
	} else if len(svc.expectedAud) != 0 {
		var ok bool
		for _, ent := range svc.expectedAud {
			if ent == req.Entity {
				ok = true
				break
			}
		}
		if !ok {
			return nil, errors.New("nope 2")
		}
	}
	authMetadata := map[string]string{"some": "data"}
	if svc.noMetadata {
		authMetadata = nil
	}

	entity := MustContextAuthEntity(ctx).Entity

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  entity,
			Audience: jwt.ClaimStrings{req.Entity},
		},
		AuthCredentialsType: CredentialsTypeExternal,
		AuthMetadata:        authMetadata,
	})

	tokenString, err := token.SignedString(svc.privKey)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "failed to ext")
	}

	return &rpcpb.AuthenticateToResponse{
		AccessToken: tokenString,
	}, nil
}

func signTestAuthToken(
	t *testing.T,
	pubKey ed25519.PublicKey,
	privKey ed25519.PrivateKey,
	aud, ent string,
	credType CredentialsType,
) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  ent,
			Audience: jwt.ClaimStrings{aud},
		},
		AuthCredentialsType: credType,
		AuthMetadata:        map[string]string{},
	})

	token.Header["kid"] = base64.RawURLEncoding.EncodeToString(pubKey)

	tokenString, err := token.SignedString(privKey)
	test.That(t, err, test.ShouldBeNil)

	return tokenString
}
