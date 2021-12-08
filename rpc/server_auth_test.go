package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/go-errors/errors"
	"github.com/golang-jwt/jwt/v4"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	rpcpb "go.viam.com/utils/proto/rpc/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestServerAuth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	var testMu sync.Mutex
	fakeAuthWorks := false
	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			Insecure:      true,
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

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
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
	t.Run("standard grpc", func(t *testing.T) {
		conn, err := grpc.DialContext(context.Background(), httpListener.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
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
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "authentication required")

		// bad auth headers
		md := make(metadata.MD)
		md.Set("authorization", "")
		ctx := metadata.NewOutgoingContext(context.Background(), md)
		_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok = status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "expected Authorization")

		md = make(metadata.MD)
		md.Set("authorization", "Bearer ")
		ctx = metadata.NewOutgoingContext(context.Background(), md)
		_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldNotBeNil)
		gStatus, ok = status.FromError(err)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "invalid")

		// bad auth scenarios
		authClient := rpcpb.NewAuthServiceClient(conn)
		_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "notfake",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "no way to")

		_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type: "fake",
		}})
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "work yet")

		testMu.Lock()
		fakeAuthWorks = true
		testMu.Unlock()

		// works from here
		authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldBeNil)

		md = make(metadata.MD)
		bearer := fmt.Sprintf("Bearer %s", authResp.AccessToken)
		md.Set("authorization", bearer)
		ctx = metadata.NewOutgoingContext(context.Background(), md)

		echoResp, err := client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
	})

	t.Run("grpc-web", func(t *testing.T) {
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
		test.That(t, httpResp1.Header["Grpc-Message"], test.ShouldResemble, []string{"authentication required"})
		test.That(t, string(rd), test.ShouldResemble, "")

		// bad auth headers
		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/grpc-web-text")
		req.Header.Add("authorization", "")
		httpResp2, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp2.Body.Close()
		test.That(t, httpResp2.StatusCode, test.ShouldEqual, 200)
		rd, err = ioutil.ReadAll(httpResp2.Body)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, httpResp2.Header["Grpc-Message"], test.ShouldResemble, []string{"expected Authorization: Bearer"})
		test.That(t, string(rd), test.ShouldResemble, "")

		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/grpc-web-text")
		req.Header.Add("authorization", "Bearer hello")
		httpResp3, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp3.Body.Close()
		test.That(t, httpResp3.StatusCode, test.ShouldEqual, 200)
		rd, err = ioutil.ReadAll(httpResp3.Body)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, httpResp3.Header["Grpc-Message"], test.ShouldResemble, []string{"unauthenticated: token contains an invalid number of segments"})
		test.That(t, string(rd), test.ShouldResemble, "")

		// works from here
		conn, err := grpc.DialContext(context.Background(), httpListener.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()
		authClient := rpcpb.NewAuthServiceClient(conn)
		authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldBeNil)

		bearer := fmt.Sprintf("Bearer %s", authResp.AccessToken)

		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/grpc-web-text")
		req.Header.Add("authorization", bearer)
		httpResp4, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp4.Body.Close()
		test.That(t, httpResp4.StatusCode, test.ShouldEqual, 200)
		rd, err = ioutil.ReadAll(httpResp4.Body)
		test.That(t, err, test.ShouldBeNil)
		// it says hey!
		test.That(t, string(rd), test.ShouldResemble, "AAAAAAYKBGhleSE=gAAAABBncnBjLXN0YXR1czogMA0K")
	})

	t.Run("JSON", func(t *testing.T) {
		httpURL := fmt.Sprintf("http://%s/rpc/examples/echo/v1/echo", httpListener.Addr().String())
		req, err := http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/json")
		httpResp1, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp1.Body.Close()
		test.That(t, httpResp1.StatusCode, test.ShouldEqual, 401)

		// bad auth headers
		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/json")
		req.Header.Add("authorization", "")
		httpResp2, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp2.Body.Close()
		test.That(t, httpResp2.StatusCode, test.ShouldEqual, 401)

		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/json")
		req.Header.Add("authorization", "Bearer hello")
		httpResp3, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp3.Body.Close()
		test.That(t, httpResp3.StatusCode, test.ShouldEqual, 401)

		// works from here
		conn, err := grpc.DialContext(context.Background(), httpListener.Addr().String(), grpc.WithInsecure(), grpc.WithBlock())
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()
		authClient := rpcpb.NewAuthServiceClient(conn)
		authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldBeNil)

		bearer := fmt.Sprintf("Bearer %s", authResp.AccessToken)

		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(`{"message": "world"}`))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/json")
		req.Header.Add("authorization", bearer)
		httpResp4, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp4.Body.Close()
		test.That(t, httpResp4.StatusCode, test.ShouldEqual, 200)
		dec := json.NewDecoder(httpResp4.Body)
		var echoM map[string]interface{}
		test.That(t, dec.Decode(&echoM), test.ShouldBeNil)
		test.That(t, echoM, test.ShouldResemble, map[string]interface{}{"message": "world"})
	})

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestServerAuthJWTExpiration(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			Insecure:      true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			return nil
		}, func(ctx context.Context, entity string) error {
			return nil
		})),
		WithAuthRSAPrivateKey(privKey),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
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
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()
	client := pb.NewEchoServiceClient(conn)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, rpcClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"does not matter"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
		CredentialsType: CredentialsType("fake"),
	})

	tokenString, err := token.SignedString(privKey)
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	bearer := fmt.Sprintf("Bearer %s", tokenString)
	md.Set("authorization", bearer)
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldNotBeNil)
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
	test.That(t, gStatus.Message(), test.ShouldContainSubstring, "unauthenticated: token is expired")

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestServerAuthJWTAudience(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	expectedEntity := "someent"
	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			Insecure:      true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", MakeFuncAuthHandler(func(ctx context.Context, entity, payload string) error {
			return nil
		}, func(ctx context.Context, entity string) error {
			if entity == expectedEntity {
				return nil
			}
			return errSessionEntityHandlerMismatch
		})),
		WithAuthRSAPrivateKey(privKey),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
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
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()
	client := pb.NewEchoServiceClient(conn)

	t.Cleanup(func() {
		test.That(t, rpcServer.Stop(), test.ShouldBeNil)
		err = <-errChan
		test.That(t, err, test.ShouldBeNil)
	})

	for _, correctEntity := range []bool{false, true} {
		t.Run(fmt.Sprintf("correctEntity=%t", correctEntity), func(t *testing.T) {
			var aud string
			if correctEntity {
				aud = expectedEntity
			} else {
				aud = "actually matters"
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, rpcClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Audience: jwt.ClaimStrings{aud},
				},
				CredentialsType: CredentialsType("fake"),
			})

			tokenString, err := token.SignedString(privKey)
			test.That(t, err, test.ShouldBeNil)

			md := make(metadata.MD)
			bearer := fmt.Sprintf("Bearer %s", tokenString)
			md.Set("authorization", bearer)
			ctx := metadata.NewOutgoingContext(context.Background(), md)

			echoResp, err := client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
			if correctEntity {
				test.That(t, err, test.ShouldBeNil)
				test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
			} else {
				test.That(t, err, test.ShouldNotBeNil)
				gStatus, ok := status.FromError(err)
				test.That(t, ok, test.ShouldBeTrue)
				test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
				test.That(t, gStatus.Message(), test.ShouldContainSubstring, "mismatch")
			}
		})
	}
}

func TestServerAuthKeyFunc(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	var testMu sync.Mutex
	var key interface{}
	rpcServer, err := NewServer(
		logger,
		WithWebRTCServerOptions(WebRTCServerOptions{
			Enable:        true,
			Insecure:      true,
			SignalingHost: "yeehaw",
		}),
		WithAuthHandler("fake", WithTokenVerificationKeyProvider(
			funcAuthHandler{
				auth: func(ctx context.Context, entity, payload string) error {
					return nil
				},
				verify: func(ctx context.Context, entity string) error {
					return nil
				},
			},
			func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
				}

				testMu.Lock()
				defer testMu.Unlock()
				return key, nil
			},
		)),
		WithAuthRSAPrivateKey(privKey),
	)
	test.That(t, err, test.ShouldBeNil)

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		&echoserver.Server{},
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
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()
	client := pb.NewEchoServiceClient(conn)

	authClient := rpcpb.NewAuthServiceClient(conn)
	authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
		Type:    "fake",
		Payload: "something",
	}})
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	bearer := fmt.Sprintf("Bearer %s", authResp.AccessToken)
	md.Set("authorization", bearer)
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	testMu.Lock()
	key = &privKey.PublicKey
	testMu.Unlock()

	_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	// swap tokens
	privKey2, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	testMu.Lock()
	key = &privKey2.PublicKey
	testMu.Unlock()

	_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldNotBeNil)
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
	test.That(t, gStatus.Message(), test.ShouldContainSubstring, "verification error")

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
