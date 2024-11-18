package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/pkg/errors"
	"go.viam.com/test"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/jwks"
	"go.viam.com/utils/jwks/jwksutils"
	pb "go.viam.com/utils/proto/rpc/examples/echo/v1"
	echoserver "go.viam.com/utils/rpc/examples/echo/server"
)

func TestJWKSKeyProviderAndEmailLoader(t *testing.T) {
	logger := golog.NewTestLogger(t)

	keyset, privKeys, err := jwksutils.NewTestKeySet(1)
	test.That(t, err, test.ShouldBeNil)

	expectedAudience := "api.example.com"
	expectedAudienceOther := "api2.example.com"
	expectedUser := "user@example.com"

	jwkProvider := jwks.NewStaticJWKKeyProvider(keyset)
	credType := CredentialsType("some-oidc")

	rpcServer, err := NewServer(
		logger,
		WithAuthAudience(expectedAudience, expectedAudienceOther),
		WithTokenVerificationKeyProvider(credType, MakeJWKSKeyProvider(jwkProvider)),
		WithEntityDataLoader(credType, EntityDataLoaderFunc(
			func(ctx context.Context, claims Claims) (interface{}, error) {
				if claims.Metadata()["email"] != expectedUser {
					return nil, errors.Errorf("%q != %q", claims.Entity(), expectedUser)
				}
				return claims.Metadata()["email"], nil
			}),
		),
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
	echoServer.SetExpectedAuthEntity("someauthprovider/" + expectedUser)
	echoServer.SetExpectedAuthEntityData(expectedUser)

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
		//nolint:staticcheck
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
			accessToken, err := SignJWKBasedAccessToken(credType, privKeys[0], expectedUser, expectedAudience, "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			echoResp, err := makeAuthRequest(accessToken)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		})

		t.Run("with valid access token using second aud", func(t *testing.T) {
			accessToken, err := SignJWKBasedAccessToken(credType, privKeys[0], expectedUser, expectedAudienceOther, "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			echoResp, err := makeAuthRequest(accessToken)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, echoResp.GetMessage(), test.ShouldEqual, "hello")
		})

		t.Run("with invalid aud access token claim", func(t *testing.T) {
			accessToken, err := SignJWKBasedAccessToken(credType, privKeys[0], expectedUser, "not-valid-aud", "iss", "key-id-1")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid kid access token claim", func(t *testing.T) {
			accessToken, err := SignJWKBasedAccessToken(credType, privKeys[0], expectedUser, expectedAudience, "iss", "not-valid")
			test.That(t, err, test.ShouldBeNil)

			_, err = makeAuthRequest(accessToken)
			gStatus, ok := status.FromError(err)
			test.That(t, ok, test.ShouldBeTrue)
			test.That(t, gStatus.Code(), test.ShouldEqual, codes.Unauthenticated)
		})

		t.Run("with invalid signature access token", func(t *testing.T) {
			invalidPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
			test.That(t, err, test.ShouldBeNil)

			accessToken, err := SignJWKBasedAccessToken(credType, invalidPrivKey, expectedUser, expectedAudience, "iss", "not-valid")
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

// SignJWKBasedAccessToken returns an access jwt access token typically returned by an OIDC provider.
func SignJWKBasedAccessToken(
	credType CredentialsType,
	key *rsa.PrivateKey,
	entity, aud, iss, keyID string,
) (string, error) {
	token := &jwt.Token{
		Header: map[string]interface{}{
			"typ": "JWT",
			"alg": jwt.SigningMethodRS256.Alg(),
			"kid": keyID,
		},
		Claims: JWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Audience: []string{aud},
				Issuer:   iss,
				// In testing make sure we don't confuse the subject for the email used since the subject
				// is considered to be more unique.
				Subject:  fmt.Sprintf("someauthprovider/%s", entity),
				IssuedAt: jwt.NewNumericDate(time.Now()),
			},
			AuthCredentialsType: credType,
			AuthMetadata: map[string]string{
				"email": entity,
			},
		},
		Method: jwt.SigningMethodRS256,
	}

	return token.SignedString(key)
}
