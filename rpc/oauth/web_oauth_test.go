package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/jwks"
	"go.viam.com/utils/jwks/jwksutils"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	"go.viam.com/utils/rpc"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
	oauthtest "go.viam.com/utils/rpc/oauth/testutils"
)

func TestWebOauth(t *testing.T) {
	logger := golog.NewTestLogger(t)

	keyset, privKeys, err := jwksutils.NewTestKeySet(1)
	test.That(t, err, test.ShouldBeNil)

	expectedAudience := "api.example.com"
	expectedAudienceOther := "api2.example.com"
	expectedUser := "user@example.com"
	opts := WebOAuthOptions{
		AllowedAudiences: []string{expectedAudience, expectedAudienceOther},
		KeyProvider:      jwks.NewStaticJWKKeyProvider(keyset),
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
		ContextAuthClaims: func(ctx context.Context) interface{} {
			return rpc.ContextAuthClaims(ctx)
		},
		ContextAuthSubject: func(ctx context.Context) string {
			// ensure the subject is always the expected user and not the actual `sub` claim.
			subject := rpc.MustContextAuthSubject(ctx)
			test.That(t, subject, test.ShouldResemble, expectedUser)
			return subject
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
			accessToken, err := oauthtest.SignWebAuthAccessToken(privKeys[0], expectedUser, expectedAudience, "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			echoResp, err := makeAuthRequest(accessToken)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		})

		t.Run("with valid access token using second aud", func(t *testing.T) {
			accessToken, err := oauthtest.SignWebAuthAccessToken(privKeys[0], expectedUser, expectedAudienceOther, "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			echoResp, err := makeAuthRequest(accessToken)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		})

		t.Run("with invalid aud access token claim", func(t *testing.T) {
			accessToken, err := oauthtest.SignWebAuthAccessToken(privKeys[0], expectedUser, "not-valid-aud", "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid kid access token claim", func(t *testing.T) {
			accessToken, err := oauthtest.SignWebAuthAccessToken(privKeys[0], expectedUser, expectedAudience, "iss", "not-valid")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid signature access token", func(t *testing.T) {
			invalidPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
			test.That(t, err, test.ShouldBeNil)

			accessToken, err := oauthtest.SignWebAuthAccessToken(invalidPrivKey, expectedUser, expectedAudience, "iss", "not-valid")
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
	logger := golog.NewTestLogger(t)

	keyset, privKeys, err := jwksutils.NewTestKeySet(1)
	test.That(t, err, test.ShouldBeNil)

	expectedAudience := "api.example.com"
	expectedUser := "user@example.com"

	opts := WebOAuthOptions{
		AllowedAudiences: []string{expectedAudience},
		KeyProvider:      jwks.NewStaticJWKKeyProvider(keyset),
		Logger:           logger,
	}
	rpcServer, err := rpc.NewServer(logger, WithWebOAuthTokenAuthHandler(opts))
	test.That(t, err, test.ShouldBeNil)

	echoServer := &echoserver.Server{
		ContextAuthEntity: rpc.MustContextAuthEntity,
		ContextAuthClaims: func(ctx context.Context) interface{} {
			return rpc.ContextAuthClaims(ctx)
		},
		ContextAuthSubject: rpc.MustContextAuthSubject,
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

	accessToken, err := oauthtest.SignWebAuthAccessToken(privKeys[0], expectedUser, expectedAudience, "iss", "key-id-1")
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
