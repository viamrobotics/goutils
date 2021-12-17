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
	"github.com/go-errors/errors"
	"github.com/miekg/dns"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"

	"go.viam.com/utils"
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

	var testMu sync.Mutex
	fakeAuthWorks := false
	rpcServer, err = NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			testMu.Lock()
			defer testMu.Unlock()
			if fakeAuthWorks {
				return nil
			}
			return errors.New("this auth does not work yet")
		}, func(ctx context.Context, entity string) error {
			return nil
		})),
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err = net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan = make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
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
	_, err = Dial(context.Background(), "yeehaw", logger,
		WithInsecure(),
		WithWebRTCOptions(DialWebRTCOptions{
			SignalingServer: httpListener.Addr().String(),
		}))
	test.That(t, err, test.ShouldNotBeNil)
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)

	conn, err = Dial(context.Background(), "yeehaw", logger,
		WithInsecure(),
		WithWebRTCOptions(DialWebRTCOptions{
			SignalingServer: httpListener.Addr().String(),
		}),
		WithCredentials(Credentials{Type: "fake"}),
	)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	port, err := utils.TryReserveRandomPort()
	test.That(t, err, test.ShouldBeNil)
	mux := dns.NewServeMux()
	httpIP := httpListener.Addr().(*net.TCPAddr).IP
	httpPort := httpListener.Addr().(*net.TCPAddr).Port

	var noAnswer bool
	mux.HandleFunc("yeehaw", func(rw dns.ResponseWriter, r *dns.Msg) {
		m := &dns.Msg{Compress: false}
		m.SetReply(r)

		switch r.Opcode {
		case dns.OpcodeQuery:
			for _, q := range m.Question {
				switch q.Qtype {
				case dns.TypeA:
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
			switch r.Opcode {
			case dns.OpcodeQuery:
				for _, q := range m.Question {
					switch q.Qtype {
					case dns.TypeA:
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

	noAnswer = true
	mux.HandleFunc("_webrtc._tcp.yeehaw.", func(rw dns.ResponseWriter, r *dns.Msg) {
		m := &dns.Msg{Compress: false}
		m.SetReply(r)

		switch r.Opcode {
		case dns.OpcodeQuery:
			for _, q := range m.Question {
				switch q.Qtype {
				case dns.TypeSRV:
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
	conn, err = Dial(ctx, "yeehaw", logger, WithInsecure(), WithCredentials(Credentials{Type: "fake"}))
	test.That(t, err, test.ShouldBeNil)
	test.That(t, conn.Close(), test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
	test.That(t, dnsServer.Shutdown(), test.ShouldBeNil)
}

func TestDialExternalAuth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKeyExternal, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	rpcServerInternal, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			return nil
		}, func(ctx context.Context, entity string) error {
			return nil
		})),
		WithAuthHandler("fakeWithKey", WithPublicKeyProvider(
			funcAuthHandler{
				auth: func(ctx context.Context, entity, payload string) error {
					return errors.New("go auth externally")
				},
				verify: func(ctx context.Context, entity string) error {
					return nil
				},
			},
			&privKeyExternal.PublicKey,
		)),
	)
	test.That(t, err, test.ShouldBeNil)

	rpcServerExternal, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			return nil
		}, func(ctx context.Context, entity string) error {
			return nil
		})),
		WithAuthHandler("fakeWithKey", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			return nil
		}, func(ctx context.Context, entity string) error {
			return nil
		})),
		WithAuthRSAPrivateKey(privKeyExternal),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServerInternal.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListenerInternal, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)
	httpListenerExternal, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServerInternal.Serve(httpListenerInternal)
	}()
	go func() {
		errChan <- rpcServerExternal.Serve(httpListenerExternal)
	}()

	t.Run("with external auth should work", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fakeWithKey"}),
			WithExternalAuth(httpListenerExternal.Addr().String()),
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

	t.Run("with external auth but mismatched keys should fail", func(t *testing.T) {
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerExternal.Addr().String()),
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
		conn, err := Dial(context.Background(), httpListenerInternal.Addr().String(), logger,
			WithInsecure(),
			WithCredentials(Credentials{Type: "fake"}),
			WithExternalAuth(httpListenerInternal.Addr().String()),
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

	test.That(t, rpcServerInternal.Stop(), test.ShouldBeNil)
	test.That(t, rpcServerExternal.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

type staticDialer struct {
	address string
}

func (sd *staticDialer) DialDirect(ctx context.Context, target string, onClose func() error, opts ...grpc.DialOption) (ClientConn, error) {
	return grpc.DialContext(ctx, sd.address, opts...)
}

func (sd *staticDialer) DialFunc(proto string, target string, f func() (ClientConn, func() error, error)) (ClientConn, error) {
	conn, _, err := f()
	return conn, err
}

func (sd *staticDialer) Close() error {
	return nil
}
