package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
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
	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/pkg/errors"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/jwks/jwksutils"
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
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			testMu.Lock()
			defer testMu.Unlock()
			if fakeAuthWorks {
				return map[string]string{"please persist": "need this value"}, nil
			}
			return nil, errors.New("this auth does not work yet")
		})),
		WithEntityDataLoader("fake", EntityDataLoaderFunc(func(ctx context.Context, claims Claims) (interface{}, error) {
			if claims.Metadata()["please persist"] != "need this value" {
				return nil, errors.New("bad metadata")
			}

			return "somespecialinterface", nil
		})),
		WithEntityDataLoader("something_else", EntityDataLoaderFunc(func(ctx context.Context, claims Claims) (interface{}, error) {
			panic("never called")
		})),
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
	echoServer.SetAuthorized(true)
	echoServer.SetExpectedAuthEntityData("somespecialinterface")
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		echoServer,
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
		conn, err := grpc.DialContext(
			context.Background(),
			httpListener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
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
		test.That(t, gStatus.Message(), test.ShouldContainSubstring, "expected authorization header with prefix")

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
		_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo"})
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "credentials required")

		_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "notfake",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "do not know how")

		_, err = authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
			Type:    "something_else",
			Payload: "something",
		}})
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "direct authentication not supporte")

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
		rd, err := io.ReadAll(httpResp1.Body)
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
		rd, err = io.ReadAll(httpResp2.Body)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, httpResp2.Header["Grpc-Message"], test.ShouldResemble, []string{"expected authorization header with prefix: Bearer"})
		test.That(t, string(rd), test.ShouldResemble, "")

		req, err = http.NewRequest(http.MethodPost, httpURL, strings.NewReader(grpcWebReq))
		test.That(t, err, test.ShouldBeNil)
		req.Header.Add("content-type", "application/grpc-web-text")
		req.Header.Add("authorization", "Bearer hello")
		httpResp3, err := http.DefaultClient.Do(req)
		test.That(t, err, test.ShouldBeNil)
		defer httpResp3.Body.Close()
		test.That(t, httpResp3.StatusCode, test.ShouldEqual, 200)
		rd, err = io.ReadAll(httpResp3.Body)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, httpResp3.Header["Grpc-Message"],
			test.ShouldResemble, []string{"unauthenticated: token contains an invalid number of segments"})
		test.That(t, string(rd), test.ShouldResemble, "")

		// works from here

		testMu.Lock()
		fakeAuthWorks = true
		testMu.Unlock()

		conn, err := grpc.DialContext(
			context.Background(),
			httpListener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
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
		rd, err = io.ReadAll(httpResp4.Body)
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
		conn, err := grpc.DialContext(
			context.Background(),
			httpListener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
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
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
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

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()
	client := pb.NewEchoServiceClient(conn)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{"does not matter"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
		AuthCredentialsType: CredentialsType("fake"),
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

func TestServerAuthJWTAudienceAndID(t *testing.T) {
	t.Skip()
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)

	expectedEntity := "yeehaw"
	expectedAudience := "someaud"
	rpcServer, err := NewServer(
		logger,
		WithInstanceNames(expectedAudience),
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
		WithEntityDataLoader("fake", EntityDataLoaderFunc(func(ctx context.Context, claims Claims) (interface{}, error) {
			if claims.Entity() == expectedEntity {
				return expectedEntity, nil
			}
			return nil, errCannotAuthEntity
		})),
		WithAuthRSAPrivateKey(privKey),
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
	echoServer.SetAuthorized(true)
	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		echoServer,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
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

	for _, entity := range []string{"", "really actually matters", expectedEntity} {
		var testName string
		if entity == "" {
			testName = "noEntity"
		} else {
			testName = fmt.Sprintf("correctEntity=%t", expectedEntity == entity)
		}
		t.Run(testName, func(t *testing.T) {
			for _, correctAudience := range []bool{false, true} {
				t.Run(fmt.Sprintf("correctAudience=%t", correctAudience), func(t *testing.T) {
					var aud string
					if correctAudience {
						aud = expectedAudience
					} else {
						aud = "actually matters"
					}
					token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
						RegisteredClaims: jwt.RegisteredClaims{
							Subject:  entity,
							Audience: jwt.ClaimStrings{aud},
						},
						AuthCredentialsType: CredentialsType("fake"),
					})

					tokenString, err := token.SignedString(privKey)
					test.That(t, err, test.ShouldBeNil)

					md := make(metadata.MD)
					bearer := fmt.Sprintf("Bearer %s", tokenString)
					md.Set("authorization", bearer)
					ctx := metadata.NewOutgoingContext(context.Background(), md)

					echoResp, err := client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
					if correctAudience && expectedEntity == entity {
						test.That(t, err, test.ShouldBeNil)
						test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
					} else {
						test.That(t, err, test.ShouldNotBeNil)
						gStatus, ok := status.FromError(err)
						test.That(t, ok, test.ShouldBeTrue)
						test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
						switch {
						case !correctAudience:
							test.That(t, gStatus.Message(), test.ShouldContainSubstring, "invalid audience")
						case entity == "":
							test.That(t, gStatus.Message(), test.ShouldContainSubstring, "expected entity (sub) in claims")
						default:
							test.That(t, gStatus.Message(), test.ShouldContainSubstring, "cannot authenticate")
						}
					}
				})
			}
		})
	}
}

func TestServerPublicMethods(t *testing.T) {
	logger := golog.NewTestLogger(t)

	t.Run("NoAuthSet", func(t *testing.T) {
		// this is an authenticated server - using the default auth service on server
		rpcServer, err := NewServer(logger,
			WithPublicMethods([]string{
				"/proto.rpc.examples.echo.v1.EchoService/Echo",
				"/proto.rpc.examples.echo.v1.EchoService/EchoMultiple",
			}),
		)
		defer rpcServer.Stop()
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
		grpcOpts := []grpc.DialOption{
			grpc.WithBlock(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}

		errChan := make(chan error)
		go func() {
			errChan <- rpcServer.Serve(listener)
		}()

		conn, err := grpc.DialContext(context.Background(), listener.Addr().String(), grpcOpts...)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()
		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(context.Background(), &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp, test.ShouldNotBeNil)
		test.That(t, echoResp.Message, test.ShouldEqual, "hello")

		// test the stream service
		_, err = client.EchoMultiple(context.Background(), &pb.EchoMultipleRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		err = <-errChan
		test.That(t, err, test.ShouldBeNil)
	})

	t.Run("Given an authenticated client, they can still access the public API", func(t *testing.T) {
		testPrivKey, err := rsa.GenerateKey(rand.Reader, 512)
		test.That(t, err, test.ShouldBeNil)

		rpcServer, err := NewServer(logger,
			// this is the main echo method
			WithPublicMethods([]string{
				"/proto.rpc.examples.echo.v1.EchoService/Echo",
				"/proto.rpc.examples.echo.v1.EchoService/EchoMultiple",
			}),
			WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
				return map[string]string{}, nil
			})),
			WithAuthRSAPrivateKey(testPrivKey),
		)

		defer rpcServer.Stop()
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
		grpcOpts := []grpc.DialOption{
			grpc.WithBlock(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}

		errChan := make(chan error)
		go func() {
			errChan <- rpcServer.Serve(listener)
		}()

		conn, err := grpc.DialContext(context.Background(), listener.Addr().String(), grpcOpts...)
		test.That(t, err, test.ShouldBeNil)
		defer func() {
			test.That(t, conn.Close(), test.ShouldBeNil)
		}()

		// setup for auth stuff
		authClient := rpcpb.NewAuthServiceClient(conn)
		authResp, err := authClient.Authenticate(
			context.Background(), &rpcpb.AuthenticateRequest{Entity: "foo", Credentials: &rpcpb.Credentials{
				Type:    "fake",
				Payload: "something",
			}})
		test.That(t, err, test.ShouldBeNil)
		_, err = jwt.Parse(authResp.AccessToken, func(token *jwt.Token) (interface{}, error) {
			return &testPrivKey.PublicKey, nil
		})
		test.That(t, err, test.ShouldBeNil)

		md := make(metadata.MD)
		bearer := fmt.Sprintf("Bearer %s", authResp.AccessToken)
		md.Set("authorization", bearer)
		ctx := metadata.NewOutgoingContext(context.Background(), md)

		client := pb.NewEchoServiceClient(conn)
		echoResp, err := client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		test.That(t, echoResp, test.ShouldNotBeNil)
		test.That(t, echoResp.Message, test.ShouldEqual, "hello")

		// test the stream service
		_, err = client.EchoMultiple(context.Background(), &pb.EchoMultipleRequest{Message: "hello"})
		test.That(t, err, test.ShouldBeNil)
		err = <-errChan
		test.That(t, err, test.ShouldBeNil)
	})
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
		WithAuthHandler("fake", AuthHandlerFunc(func(ctx context.Context, entity, payload string) (map[string]string, error) {
			return map[string]string{}, nil
		})),
		WithTokenVerificationKeyProvider("fake",
			TokenVerificationKeyProviderFunc(func(ctx context.Context, token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
				}

				testMu.Lock()
				defer testMu.Unlock()
				return key, nil
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

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
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

func TestServerAuthToHandler(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)
	thumbprint, err := RSAPublicKeyThumbprint(&privKey.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	rpcServer, err := NewServer(
		logger,
		WithAuthRSAPrivateKey(privKey),
		WithAuthHandler("fake", MakeSimpleAuthHandler([]string{"entity1", "entity2"}, "mypayload")),
		// Our audience members are a random name and an extra to test with
		WithAuthAudience(uuid.NewString(), "entity2"),
		WithExternalAuthPublicKeyTokenVerifier(&privKey.PublicKey),
		WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
			test.That(t, entity, test.ShouldEqual, "entity2")
			return map[string]string{"test": "value"}, nil
		}),
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
	echoServer.SetAuthorized(true)
	echoServer.SetExpectedAuthEntity("entity1")

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		echoServer,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()

	// First authenticate using the fake auth handler.
	authClient := rpcpb.NewAuthServiceClient(conn)
	authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{
		Entity: "entity1",
		Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "mypayload",
		},
	},
	)
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authResp.AccessToken))
	authCtx := metadata.NewOutgoingContext(context.Background(), md)

	// Use the credential bearer token from the Authenticate request to the AuthenticateTo the "foo" entity.
	authToClient := rpcpb.NewExternalAuthServiceClient(conn)
	authToResp, err := authToClient.AuthenticateTo(authCtx, &rpcpb.AuthenticateToRequest{Entity: "entity2"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authToResp.AccessToken, test.ShouldNotBeEmpty)

	// Verify the resulting claims match the expected values.
	var claims JWTClaims
	token, err := jwt.ParseWithClaims(authToResp.AccessToken, &claims, func(token *jwt.Token) (interface{}, error) {
		return &privKey.PublicKey, nil
	})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, claims.Entity(), test.ShouldEqual, "entity1")
	test.That(t, claims.Audience, test.ShouldContain, "entity2")
	test.That(t, token.Header["kid"], test.ShouldEqual, thumbprint)

	md = make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authToResp.AccessToken))
	authCtx = metadata.NewOutgoingContext(context.Background(), md)

	client := pb.NewEchoServiceClient(conn)
	_, err = client.Echo(authCtx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestServerOptionWithAuthIssuer(t *testing.T) {
	t.Skip()
	testutils.SkipUnlessInternet(t)

	privKey, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)

	aud1 := uuid.NewString()

	t.Run("empty issuer", func(t *testing.T) {
		logger := golog.NewTestLogger(t)
		_, err := NewServer(
			logger,
			WithAuthRSAPrivateKey(privKey),
			WithAuthHandler("fake", MakeSimpleAuthHandler([]string{"entity1", "entity2"}, "mypayload")),
			// Our audience members are a random name and an extra to test with
			WithAuthAudience(aud1, "entity2"),
			WithAuthIssuer(""),
			WithExternalAuthPublicKeyTokenVerifier(&privKey.PublicKey),
			WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
				test.That(t, entity, test.ShouldEqual, "entity2")
				return map[string]string{"test": "value"}, nil
			}),
		)
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "auth issuer must be non-empty")
	})

	for _, audSet := range []bool{false, true} {
		t.Run(fmt.Sprintf("aud set=%t", audSet), func(t *testing.T) {
			for _, issSet := range []bool{false, true} {
				t.Run(fmt.Sprintf("iss set=%t", issSet), func(t *testing.T) {
					logger := golog.NewTestLogger(t)
					opts := []ServerOption{
						WithAuthRSAPrivateKey(privKey),
						WithAuthHandler("fake", MakeSimpleAuthHandler([]string{"entity1", "entity2"}, "mypayload")),
						WithExternalAuthPublicKeyTokenVerifier(&privKey.PublicKey),
						WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
							test.That(t, entity, test.ShouldEqual, "entity2")
							return map[string]string{"test": "value"}, nil
						}),
					}

					if audSet {
						// Our audience members are a random name and an extra to test with
						opts = append(opts, WithAuthAudience(aud1, "entity2"))
					}

					var expectedIss string
					if issSet {
						expectedIss = uuid.NewString()
						opts = append(opts, WithAuthIssuer(expectedIss))
					} else if audSet {
						expectedIss = aud1
					}
					rpcServer, err := NewServer(
						logger,
						opts...,
					)
					test.That(t, err, test.ShouldBeNil)

					if !issSet && !audSet {
						expectedIss = rpcServer.InstanceNames()[0]
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
					echoServer.SetAuthorized(true)
					echoServer.SetExpectedAuthEntity("entity1")

					err = rpcServer.RegisterServiceServer(
						context.Background(),
						&pb.EchoService_ServiceDesc,
						echoServer,
						pb.RegisterEchoServiceHandlerFromEndpoint,
					)
					test.That(t, err, test.ShouldBeNil)

					httpListener, err := net.Listen("tcp", "localhost:0")
					test.That(t, err, test.ShouldBeNil)

					errChan := make(chan error)
					go func() {
						errChan <- rpcServer.Serve(httpListener)
					}()

					conn, err := grpc.DialContext(
						context.Background(),
						httpListener.Addr().String(),
						grpc.WithTransportCredentials(insecure.NewCredentials()),
						grpc.WithBlock(),
					)
					test.That(t, err, test.ShouldBeNil)
					defer func() {
						test.That(t, conn.Close(), test.ShouldBeNil)
					}()

					// First authenticate using the fake auth handler.
					authClient := rpcpb.NewAuthServiceClient(conn)
					authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{
						Entity: "entity1",
						Credentials: &rpcpb.Credentials{
							Type:    "fake",
							Payload: "mypayload",
						},
					},
					)
					test.That(t, err, test.ShouldBeNil)

					// Verify the resulting claims match the expected values.
					var claims JWTClaims
					_, err = jwt.ParseWithClaims(authResp.AccessToken, &claims, func(token *jwt.Token) (interface{}, error) {
						return &privKey.PublicKey, nil
					})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, claims.Issuer, test.ShouldEqual, expectedIss)

					md := make(metadata.MD)
					md.Set("authorization", fmt.Sprintf("Bearer %s", authResp.AccessToken))
					authCtx := metadata.NewOutgoingContext(context.Background(), md)

					// Use the credential bearer token from the Authenticate request to the AuthenticateTo the "foo" entity.
					authToClient := rpcpb.NewExternalAuthServiceClient(conn)
					authToResp, err := authToClient.AuthenticateTo(authCtx, &rpcpb.AuthenticateToRequest{Entity: "entity2"})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, authToResp.AccessToken, test.ShouldNotBeEmpty)

					// Verify the resulting claims match the expected values.
					claims = JWTClaims{}
					_, err = jwt.ParseWithClaims(authToResp.AccessToken, &claims, func(token *jwt.Token) (interface{}, error) {
						return &privKey.PublicKey, nil
					})
					test.That(t, err, test.ShouldBeNil)
					test.That(t, claims.Issuer, test.ShouldEqual, expectedIss)

					md = make(metadata.MD)
					md.Set("authorization", fmt.Sprintf("Bearer %s", authToResp.AccessToken))
					authCtx = metadata.NewOutgoingContext(context.Background(), md)

					client := pb.NewEchoServiceClient(conn)
					_, err = client.Echo(authCtx, &pb.EchoRequest{Message: "hello"})
					if audSet {
						test.That(t, err, test.ShouldBeNil)
					} else {
						// we are not set up for this case since we dual use the server and we do not
						// have the entity set up as an audience member
						test.That(t, err, test.ShouldNotBeNil)
						test.That(t, err.Error(), test.ShouldContainSubstring, "invalid aud")
					}

					test.That(t, rpcServer.Stop(), test.ShouldBeNil)
					err = <-errChan
					test.That(t, err, test.ShouldBeNil)
				})
			}
		})
	}
}

func TestServerAuthToHandlerWithJWKSetTokenVerifier(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)
	thumbprint, err := RSAPublicKeyThumbprint(&privKey.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	jwkKey, err := jwk.New(&privKey.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	// must set the kid manually so it can be looked up in the set later
	test.That(t, jwkKey.Set(jwk.KeyIDKey, thumbprint), test.ShouldBeNil)
	test.That(t, jwkKey.Set(jwk.AlgorithmKey, jwt.SigningMethodRS256.Name), test.ShouldBeNil)

	keyset := jwk.NewSet()

	test.That(t, keyset.Add(jwkKey), test.ShouldBeTrue)

	rpcServer, err := NewServer(
		logger,
		WithAuthRSAPrivateKey(privKey),
		WithAuthHandler("fake", MakeSimpleAuthHandler([]string{"entity1", "entity2"}, "mypayload")),
		// Our audience members are a random name and an extra to test with
		WithAuthAudience(uuid.NewString(), "entity2"),
		WithExternalAuthJWKSetTokenVerifier(keyset),
		WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
			test.That(t, entity, test.ShouldEqual, "entity2")
			return map[string]string{"test": "value"}, nil
		}),
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
	echoServer.SetAuthorized(true)
	echoServer.SetExpectedAuthEntity("entity1")

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		echoServer,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()

	// First authenticate using the fake auth handler.
	authClient := rpcpb.NewAuthServiceClient(conn)
	authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{
		Entity: "entity1",
		Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "mypayload",
		},
	},
	)
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authResp.AccessToken))
	authCtx := metadata.NewOutgoingContext(context.Background(), md)

	// Use the credential bearer token from the Authenticate request to the AuthenticateTo the "foo" entity.
	authToClient := rpcpb.NewExternalAuthServiceClient(conn)
	authToResp, err := authToClient.AuthenticateTo(authCtx, &rpcpb.AuthenticateToRequest{Entity: "entity2"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authToResp.AccessToken, test.ShouldNotBeEmpty)

	// Verify the resulting claims match the expected values.
	var claims JWTClaims
	token, err := jwt.ParseWithClaims(authToResp.AccessToken, &claims, func(token *jwt.Token) (interface{}, error) {
		return &privKey.PublicKey, nil
	})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, claims.Entity(), test.ShouldEqual, "entity1")
	test.That(t, claims.Audience, test.ShouldContain, "entity2")
	test.That(t, token.Header["kid"], test.ShouldEqual, thumbprint)

	md = make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authToResp.AccessToken))
	authCtx = metadata.NewOutgoingContext(context.Background(), md)

	client := pb.NewEchoServiceClient(conn)
	_, err = client.Echo(authCtx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestServerAuthToHandlerWithExternalAuthOIDCTokenVerifier(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	privKey, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)
	thumbprint, err := RSAPublicKeyThumbprint(&privKey.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	jwkKey, err := jwk.New(&privKey.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	// must set the kid manually so it can be looked up in the set later
	test.That(t, jwkKey.Set(jwk.KeyIDKey, thumbprint), test.ShouldBeNil)
	test.That(t, jwkKey.Set(jwk.AlgorithmKey, jwt.SigningMethodRS256.Name), test.ShouldBeNil)

	keyset := jwk.NewSet()

	test.That(t, keyset.Add(jwkKey), test.ShouldBeTrue)

	address, closeFakeOIDC := jwksutils.ServeFakeOIDCEndpoint(t, keyset)
	defer closeFakeOIDC()

	verifier, closeVerifier, err := WithExternalAuthOIDCTokenVerifier(context.Background(), address)
	test.That(t, err, test.ShouldBeNil)
	defer closeVerifier(context.Background())

	rpcServer, err := NewServer(
		logger,
		WithAuthRSAPrivateKey(privKey),
		WithAuthHandler("fake", MakeSimpleAuthHandler([]string{"entity1", "entity2"}, "mypayload")),
		// Our audience members are a random name and an extra to test with
		WithAuthAudience(uuid.NewString(), "entity2"),
		verifier,
		WithAuthenticateToHandler(func(ctx context.Context, entity string) (map[string]string, error) {
			test.That(t, entity, test.ShouldEqual, "entity2")
			return map[string]string{"test": "value"}, nil
		}),
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
	echoServer.SetAuthorized(true)
	echoServer.SetExpectedAuthEntity("entity1")

	err = rpcServer.RegisterServiceServer(
		context.Background(),
		&pb.EchoService_ServiceDesc,
		echoServer,
		pb.RegisterEchoServiceHandlerFromEndpoint,
	)
	test.That(t, err, test.ShouldBeNil)

	httpListener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	errChan := make(chan error)
	go func() {
		errChan <- rpcServer.Serve(httpListener)
	}()

	conn, err := grpc.DialContext(
		context.Background(),
		httpListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	test.That(t, err, test.ShouldBeNil)
	defer func() {
		test.That(t, conn.Close(), test.ShouldBeNil)
	}()

	// First authenticate using the fake auth handler.
	authClient := rpcpb.NewAuthServiceClient(conn)
	authResp, err := authClient.Authenticate(context.Background(), &rpcpb.AuthenticateRequest{
		Entity: "entity1",
		Credentials: &rpcpb.Credentials{
			Type:    "fake",
			Payload: "mypayload",
		},
	},
	)
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authResp.AccessToken))
	authCtx := metadata.NewOutgoingContext(context.Background(), md)

	// Use the credential bearer token from the Authenticate request to the AuthenticateTo the "foo" entity.
	authToClient := rpcpb.NewExternalAuthServiceClient(conn)
	authToResp, err := authToClient.AuthenticateTo(authCtx, &rpcpb.AuthenticateToRequest{Entity: "entity2"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authToResp.AccessToken, test.ShouldNotBeEmpty)

	// Verify the resulting claims match the expected values.
	var claims JWTClaims
	token, err := jwt.ParseWithClaims(authToResp.AccessToken, &claims, func(token *jwt.Token) (interface{}, error) {
		return &privKey.PublicKey, nil
	})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, claims.Entity(), test.ShouldEqual, "entity1")
	test.That(t, claims.Audience, test.ShouldContain, "entity2")
	test.That(t, token.Header["kid"], test.ShouldEqual, thumbprint)

	md = make(metadata.MD)
	md.Set("authorization", fmt.Sprintf("Bearer %s", authToResp.AccessToken))
	authCtx = metadata.NewOutgoingContext(context.Background(), md)

	client := pb.NewEchoServiceClient(conn)
	_, err = client.Echo(authCtx, &pb.EchoRequest{Message: "hello"})
	test.That(t, err, test.ShouldBeNil)

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
