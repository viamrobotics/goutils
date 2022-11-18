package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"github.com/lestrrat-go/jwx/jwk"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/jwks"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	"go.viam.com/utils/testutils"
)

func TestWebOauth(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	keyset := jwk.NewSet()
	privKeyForWebAuth, err := rsa.GenerateKey(rand.Reader, 4096)
	test.That(t, err, test.ShouldBeNil)
	publicKeyForWebAuth, err := jwk.New(privKeyForWebAuth.PublicKey)
	test.That(t, err, test.ShouldBeNil)
	publicKeyForWebAuth.Set(jwk.KeyIDKey, "key-id-1")
	test.That(t, keyset.Add(publicKeyForWebAuth), test.ShouldBeTrue)

	expectedAudience := "api.example.com"
	expectedUser := "user@example.com"
	opts := WebOAuthOptions{
		AllowedAudience: expectedAudience,
		KeyProvider:     jwks.NewStaticJWKKeyProvider(keyset),
		EntityVerifier: func(ctx context.Context, entity string) (interface{}, error) {
			test.That(t, entity, test.ShouldEqual, expectedUser)
			return "somespecialinterface", nil
		},
		Logger: logger,
	}

	rpcServer, err := rpc.NewServer(logger, WithWebOAuthTokenAuthHandler(opts))
	test.That(t, err, test.ShouldBeNil)

	echoServer := &echoserver.Server{
		ContextAuthEntity: rpc.MustContextAuthEntity,
		ContextAuthClaims: func(ctx context.Context) echoserver.ClaimsForTest {
			return rpc.ContextAuthClaims(ctx)
		},
		ContextAuthUniqueID: rpc.MustContextAuthUniqueID,
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

		makeAuthRequest := func(accessToken string) (*pb.EchoResponse, error) {
			md := make(metadata.MD)
			bearer := fmt.Sprintf("Bearer %s", accessToken)
			md.Set("authorization", bearer)
			ctx := metadata.NewOutgoingContext(context.Background(), md)

			return client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
		}

		t.Run("with valid access token", func(t *testing.T) {
			accessToken, err := SignWebAuthAccessToken(privKeyForWebAuth, expectedUser, expectedAudience, "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			echoResp, err := makeAuthRequest(accessToken)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		})

		t.Run("with invalid aud access token claim", func(t *testing.T) {
			accessToken, err := SignWebAuthAccessToken(privKeyForWebAuth, expectedUser, "not-valid-aud", "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid kid access token claim", func(t *testing.T) {
			accessToken, err := SignWebAuthAccessToken(privKeyForWebAuth, expectedUser, expectedAudience, "iss", "not-valid")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid signature access token", func(t *testing.T) {
			invalidPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
			test.That(t, err, test.ShouldBeNil)

			accessToken, err := SignWebAuthAccessToken(invalidPrivKey, expectedUser, expectedAudience, "iss", "not-valid")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})
	})

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}

func TestWebOauthWithNilVerifyEntity(t *testing.T) {
	testutils.SkipUnlessInternet(t)
	logger := golog.NewTestLogger(t)

	keyset := jwk.NewSet()
	privKeyForWebAuth, err := rsa.GenerateKey(rand.Reader, 4096)
	test.That(t, err, test.ShouldBeNil)
	publicKeyForWebAuth, err := jwk.New(privKeyForWebAuth.PublicKey)
	test.That(t, err, test.ShouldBeNil)
	publicKeyForWebAuth.Set(jwk.KeyIDKey, "key-id-1")
	test.That(t, keyset.Add(publicKeyForWebAuth), test.ShouldBeTrue)

	expectedAudience := "api.example.com"
	expectedUser := "user@example.com"

	opts := WebOAuthOptions{
		AllowedAudience: expectedAudience,
		KeyProvider:     jwks.NewStaticJWKKeyProvider(keyset),
		Logger:          logger,
	}
	rpcServer, err := rpc.NewServer(logger, WithWebOAuthTokenAuthHandler(opts))
	test.That(t, err, test.ShouldBeNil)

	echoServer := &echoserver.Server{
		ContextAuthEntity: rpc.MustContextAuthEntity,
		ContextAuthClaims: func(ctx context.Context) echoserver.ClaimsForTest {
			return rpc.ContextAuthClaims(ctx)
		},
		ContextAuthUniqueID: rpc.MustContextAuthUniqueID,
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

	accessToken, err := SignWebAuthAccessToken(privKeyForWebAuth, expectedUser, expectedAudience, "iss", "key-id-1")
	test.That(t, err, test.ShouldBeNil)

	md := make(metadata.MD)
	bearer := fmt.Sprintf("Bearer %s", accessToken)
	md.Set("authorization", bearer)
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	_, err = client.Echo(ctx, &pb.EchoRequest{Message: "hello"})
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.Internal)
	test.That(t, gStatus.Message(), test.ShouldContainSubstring, "invalid verify entity configuration")

	test.That(t, rpcServer.Stop(), test.ShouldBeNil)
	err = <-errChan
	test.That(t, err, test.ShouldBeNil)
}
