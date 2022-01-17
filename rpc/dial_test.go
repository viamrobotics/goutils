package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestDial(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	// pure failure cases
	_, err := Dial(context.Background(), "::", logger, WithInsecure())
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "too many")

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel1()
	_, err = Dial(ctx1, "127.0.0.1:1", logger, WithInsecure())
	test.That(t, err, test.ShouldResemble, context.DeadlineExceeded)
	cancel1()

	// working and fallbacks

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

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)

	hosts := []string{"yeehaw", "woahthere"}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			var testMu sync.Mutex
			fakeAuthWorks := false

			privKeyExternal, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
			test.That(t, err, test.ShouldBeNil)
			rpcServer, err = NewServer(
				logger,
				WithWebRTCServerOptions(WebRTCServerOptions{
					Enable:         true,
					SignalingHosts: hosts,
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

			httpListener, err = net.Listen("tcp", "localhost:0")
			test.That(t, err, test.ShouldBeNil)

			httpListenerExternal, err := net.Listen("tcp", "localhost:0")
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

			errChan = make(chan error)
			go func() {
				errChan <- rpcServer.Serve(httpListener)
			}()
			go func() {
				errChan <- rpcServerExternal.Serve(httpListenerExternal)
			}()

			// unauthenticated

			// will not fail because dialing is okay to do pre-rpc
			conn, err = Dial(context.Background(), httpListener.Addr().String(), logger, WithInsecure())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			testMu.Lock()
			fakeAuthWorks = true
			testMu.Unlock()

			// this fails because WebRTC does some RPC
			_, err = Dial(context.Background(), host, logger,
				WithWebRTCOptions(DialWebRTCOptions{
					SignalingServerAddress: httpListener.Addr().String(),
					SignalingInsecure:      true,
				}))
			test.That(t, err, test.ShouldNotBeNil)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)

			conn, err = Dial(context.Background(), host, logger,
				WithInsecure(),
				WithWebRTCOptions(DialWebRTCOptions{
					SignalingServerAddress: httpListener.Addr().String(),
					SignalingInsecure:      true,
					SignalingCreds:         Credentials{Type: "fake"},
				}),
			)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			port, err := utils.TryReserveRandomPort()
			test.That(t, err, test.ShouldBeNil)
			mux := dns.NewServeMux()
			httpIP := httpListener.Addr().(*net.TCPAddr).IP
			httpPort := httpListener.Addr().(*net.TCPAddr).Port

			var noAnswer bool
			mux.HandleFunc(host, func(rw dns.ResponseWriter, r *dns.Msg) {
				m := &dns.Msg{Compress: false}
				m.SetReply(r)

				if r.Opcode == dns.OpcodeQuery {
					for _, q := range m.Question {
						if q.Qtype == dns.TypeA {
							rr := &dns.A{
								Hdr: dns.RR_Header{
									Name:   q.Name,
									Rrtype: dns.TypeA,
									Class:  dns.ClassINET,
									Ttl:    60,
								},
								A: httpIP,
							}
							m.Answer = append(m.Answer, rr)
						}
					}
				}

				utils.UncheckedError(rw.WriteMsg(m))
			})
			mux.HandleFunc("local.something.", func(rw dns.ResponseWriter, r *dns.Msg) {
				m := &dns.Msg{Compress: false}
				m.SetReply(r)

				if !noAnswer {
					if r.Opcode == dns.OpcodeQuery {
						for _, q := range m.Question {
							if q.Qtype == dns.TypeA {
								rr := &dns.A{
									Hdr: dns.RR_Header{
										Name:   q.Name,
										Rrtype: dns.TypeA,
										Class:  dns.ClassINET,
										Ttl:    60,
									},
									A: httpIP,
								}
								m.Answer = append(m.Answer, rr)
							}
						}
					}
				}

				utils.UncheckedError(rw.WriteMsg(m))
			})
			dnsServer := &dns.Server{
				Addr:    fmt.Sprintf(":%d", port),
				Net:     "udp",
				Handler: mux,
			}
			go dnsServer.ListenAndServe()

			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return net.Dial("udp", dnsServer.Addr)
				},
			}
			ctx := contextWithResolver(context.Background(), resolver)
			ctx = ContextWithDialer(ctx, &staticDialer{httpListener.Addr().String()})
			conn, err = Dial(ctx, fmt.Sprintf("something:%d", httpPort), logger, WithInsecure())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			conn, err = Dial(ctx, fmt.Sprintf("something:%d", httpPort), logger, WithInsecure())
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			// explicit signaling server
			conn, err = Dial(context.Background(), host, logger,
				WithWebRTCOptions(DialWebRTCOptions{
					SignalingServerAddress: httpListener.Addr().String(),
					SignalingInsecure:      true,
					SignalingCreds:         Credentials{Type: "fake"},
				}),
			)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			// explicit signaling server with external auth
			conn, err = Dial(context.Background(), host, logger,
				WithDialDebug(),
				WithWebRTCOptions(DialWebRTCOptions{
					SignalingServerAddress:        httpListener.Addr().String(),
					SignalingInsecure:             true,
					SignalingCreds:                Credentials{Type: "fakeExtWithKey", Payload: "sosecret"},
					SignalingExternalAuthAddress:  httpListenerExternal.Addr().String(),
					SignalingExternalAuthInsecure: true,
				}),
			)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			// explicit signaling server with external auth but bad secret
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
			gStatus, ok = status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong secret")

			// explicit signaling server with external auth plus auth to extension but bad ent
			_, err = Dial(context.Background(), host, logger,
				WithDialDebug(),
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
			gStatus, ok = status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
			test.That(t, gStatus.Message(), test.ShouldContainSubstring, "nope")

			// explicit signaling server with external auth plus auth to extension and good ent
			conn, err = Dial(context.Background(), host, logger,
				WithDialDebug(),
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
			test.That(t, conn.Close(), test.ShouldBeNil)

			noAnswer = true
			mux.HandleFunc(fmt.Sprintf("_webrtc._tcp.%s.", host), func(rw dns.ResponseWriter, r *dns.Msg) {
				m := &dns.Msg{Compress: false}
				m.SetReply(r)

				if r.Opcode == dns.OpcodeQuery {
					for _, q := range m.Question {
						if q.Qtype == dns.TypeSRV {
							rr := &dns.SRV{
								Hdr: dns.RR_Header{
									Name:   q.Name,
									Rrtype: dns.TypeSRV,
									Class:  dns.ClassINET,
									Ttl:    60,
								},
								Target: "localhost.",
								Port:   uint16(httpPort),
							}
							m.Answer = append(m.Answer, rr)
						}
					}
				}

				utils.UncheckedError(rw.WriteMsg(m))
			})
			conn, err = Dial(ctx, host, logger, WithInsecure(), WithCredentials(Credentials{Type: "fake"}))
			test.That(t, err, test.ShouldBeNil)
			test.That(t, conn.Close(), test.ShouldBeNil)

			test.That(t, rpcServer.Stop(), test.ShouldBeNil)
			err = <-errChan
			test.That(t, err, test.ShouldBeNil)
			test.That(t, rpcServerExternal.Stop(), test.ShouldBeNil)
			err = <-errChan
			test.That(t, err, test.ShouldBeNil)
			test.That(t, dnsServer.Shutdown(), test.ShouldBeNil)
		})
	}
}

func TestDialExternalAuth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	httpListenerInternal, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
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
			Enable:         true,
			SignalingHosts: []string{"yeehaw"},
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
			Enable:         true,
			SignalingHosts: []string{"yeehaw"},
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
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:         true,
			SignalingHosts: []string{"yeehaw"},
		}),
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

	t.Run("with external auth should work", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
	})

	//nolint:dupl
	t.Run("with external auth bad secret should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "notsosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong secret")
	})

	//nolint:dupl
	t.Run("with no external auth entity provided should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), ""),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "no auth handler")
	})

	//nolint:dupl
	t.Run("with unknown external entity should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "who"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "nope")
	})

	t.Run("with expected external entity should work", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithEntityCredentials("someotherthing", Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
	})

	t.Run("with unexpected external entity should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithEntityCredentials("wrongthing", Credentials{Type: "fakeWithKey"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.PermissionDenied)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "wrong entity")
	})

	t.Run("with external auth where service fails", func(t *testing.T) {
		prevFail := authToFail
		authToFail = true
		defer func() {
			authToFail = prevFail
		}()
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey", Payload: "sosecret"}),
			WithExternalAuth(httpListenerExternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "darn")
	})

	t.Run("with external auth but mismatched keys should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerExternal2.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "verification error")
	})

	t.Run("with external auth set to same internal should work", func(t *testing.T) {
		prevFail := internalExternalAuthSrv.fail
		internalExternalAuthSrv.fail = false
		defer func() {
			internalExternalAuthSrv.fail = prevFail
		}()
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
	})

	t.Run("with external auth set authenticating to wrong entity should fail", func(t *testing.T) {
		prevFail := internalExternalAuthSrv.fail
		prevEnt := internalExternalAuthSrv.expectedEnt
		internalExternalAuthSrv.fail = false
		internalExternalAuthSrv.expectedEnt = "somethingwrong"
		defer func() {
			internalExternalAuthSrv.fail = prevFail
			internalExternalAuthSrv.expectedEnt = prevEnt
		}()
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), internalExternalAuthSrv.expectedEnt),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "bad authed ent")
	})

	t.Run("with external auth setting wrong metadata should fail", func(t *testing.T) {
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
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String(), "someent"),
			WithExternalAuthInsecure(),
		)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		client := pb.NewEchoServiceClient(conn)
		_, err = client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		gStatus, ok := status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unknown)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "bad authed data")
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

type staticDialer struct {
	address string
}

func (sd *staticDialer) DialDirect(
	ctx context.Context,
	target string,
	keyExtra string,
	onClose func() error,
	opts ...grpc.DialOption,
) (ClientConn, bool, error) {
	conn, err := grpc.DialContext(ctx, sd.address, opts...)
	return conn, false, err
}

func (sd *staticDialer) DialFunc(
	proto string,
	target string,
	keyExtra string,
	dialNew func() (ClientConn, func() error, error),
) (ClientConn, bool, error) {
	conn, _, err := dialNew()
	return conn, false, err
}

func (sd *staticDialer) Close() error {
	return nil
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
