package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMakeFuncAuthHandler(t *testing.T) {
	expectedEntity := "foo"
	expectedPayload := "bar"
	err1 := errors.New("nope1")
	err2 := errors.New("nope2")
	handler := MakeFuncAuthHandler(
		func(ctx context.Context, entity, payload string) (map[string]string, error) {
			if entity == expectedEntity && payload == expectedPayload {
				return map[string]string{"hello": "world"}, nil
			}
			return nil, err1
		},
		func(ctx context.Context, entity string) (interface{}, error) {
			if entity == expectedEntity {
				return entity, nil
			}
			return nil, err2
		},
	)

	_, err := handler.Authenticate(context.Background(), "one", "two")
	test.That(t, err, test.ShouldBeError, err1)
	_, err = handler.Authenticate(context.Background(), expectedEntity, "two")
	test.That(t, err, test.ShouldBeError, err1)
	authMD, err := handler.Authenticate(context.Background(), expectedEntity, expectedPayload)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authMD, test.ShouldResemble, map[string]string{"hello": "world"})

	_, err = handler.VerifyEntity(context.Background(), "one")
	test.That(t, err, test.ShouldBeError, err2)
	authEntity, err := handler.VerifyEntity(context.Background(), expectedEntity)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, authEntity, test.ShouldResemble, "foo")
}

func TestMakeSimpleAuthHandler(t *testing.T) {
	t.Run("with no entities should always fail", func(t *testing.T) {
		handler := MakeSimpleAuthHandler(nil, "something")
		_, err := handler.Authenticate(context.Background(), "", "something")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.Authenticate(context.Background(), "entity", "something")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.VerifyEntity(context.Background(), "")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.VerifyEntity(context.Background(), "entity")
		test.That(t, err, test.ShouldNotBeNil)
	})

	t.Run("should validate entities and key", func(t *testing.T) {
		expectedEntities := []string{"one", "two", "three"}
		expectedKey := "mykey"
		handler := MakeSimpleAuthHandler(expectedEntities, expectedKey)

		for _, ent := range expectedEntities {
			_, err := handler.Authenticate(context.Background(), ent, expectedKey)
			test.That(t, err, test.ShouldBeNil)
			_, err = handler.Authenticate(context.Background(), ent, expectedKey+"1")
			test.That(t, err, test.ShouldEqual, errInvalidCredentials)
			ret, err := handler.VerifyEntity(context.Background(), ent)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, ret, test.ShouldEqual, ent)
		}
		_, err := handler.Authenticate(context.Background(), "notent", expectedKey)
		test.That(t, err, test.ShouldBeError, errInvalidCredentials)
		_, err = handler.VerifyEntity(context.Background(), "notent")
		test.That(t, err, test.ShouldBeError, errCannotAuthEntity)
	})
}

func TestMakeSimpleMultiAuthHandler(t *testing.T) {
	test.That(t, func() {
		MakeSimpleMultiAuthHandler([]string{"hey"}, nil)
	}, test.ShouldPanicWith, "expected at least one payload")

	t.Run("with no entities should always fail", func(t *testing.T) {
		handler := MakeSimpleMultiAuthHandler(nil, []string{"something"})
		_, err := handler.Authenticate(context.Background(), "", "something")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.Authenticate(context.Background(), "entity", "something")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.VerifyEntity(context.Background(), "")
		test.That(t, err, test.ShouldNotBeNil)
		_, err = handler.VerifyEntity(context.Background(), "entity")
		test.That(t, err, test.ShouldNotBeNil)
	})

	t.Run("should validate entities and key", func(t *testing.T) {
		expectedEntities := []string{"one", "two", "three"}
		expectedKeys := []string{"mykey", "somethingelse"}
		handler := MakeSimpleMultiAuthHandler(expectedEntities, expectedKeys)

		for _, expectedKey := range expectedKeys {
			t.Run(expectedKey, func(t *testing.T) {
				for _, ent := range expectedEntities {
					_, err := handler.Authenticate(context.Background(), ent, expectedKey)
					test.That(t, err, test.ShouldBeNil)
					_, err = handler.Authenticate(context.Background(), ent, expectedKey+"1")
					test.That(t, err, test.ShouldEqual, errInvalidCredentials)
					ret, err := handler.VerifyEntity(context.Background(), ent)
					test.That(t, err, test.ShouldBeNil)
					test.That(t, ret, test.ShouldEqual, ent)
				}
				_, err := handler.Authenticate(context.Background(), "notent", expectedKey)
				test.That(t, err, test.ShouldBeError, errInvalidCredentials)
				_, err = handler.VerifyEntity(context.Background(), "notent")
				test.That(t, err, test.ShouldBeError, errCannotAuthEntity)
			})
		}
	})
}

func TestWithTokenVerificationKeyProvider(t *testing.T) {
	handler := MakeSimpleAuthHandler([]string{"one"}, "key")
	err1 := errors.New("whoops")
	wrappedHandler := WithTokenVerificationKeyProvider(handler, func(token *jwt.Token) (interface{}, error) {
		return nil, err1
	})
	_, err := wrappedHandler.Authenticate(context.Background(), "one", "key")
	test.That(t, err, test.ShouldBeNil)
	_, err = wrappedHandler.VerifyEntity(context.Background(), "one")
	test.That(t, err, test.ShouldBeNil)
	_, err = wrappedHandler.(TokenVerificationKeyProvider).TokenVerificationKey(nil)
	test.That(t, err, test.ShouldEqual, err1)
}

func TestWithPublicKeyProvider(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)
	pubKey := &privKey.PublicKey
	wrappedHandler := WithPublicKeyProvider(MakeSimpleVerifyEntity([]string{"one"}), pubKey)
	_, err = wrappedHandler.Authenticate(context.Background(), "one", "key")
	test.That(t, err, test.ShouldNotBeNil)
	gStatus, ok := status.FromError(err)
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, gStatus.Code(), test.ShouldEqual, codes.InvalidArgument)
	test.That(t, gStatus.Message(), test.ShouldContainSubstring, "go auth externally")
	_, err = wrappedHandler.VerifyEntity(context.Background(), "one")
	test.That(t, err, test.ShouldBeNil)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  uuid.NewString(),
			Audience: jwt.ClaimStrings{"does not matter"},
		},
		AuthCredentialsType: CredentialsType("fake"),
	})

	provder, ok := wrappedHandler.(TokenVerificationKeyProvider)
	test.That(t, ok, test.ShouldBeTrue)

	verificationKey, err := provder.TokenVerificationKey(token)
	test.That(t, err, test.ShouldBeNil)

	badToken := jwt.NewWithClaims(jwt.SigningMethodHS256, JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  uuid.NewString(),
			Audience: jwt.ClaimStrings{"does not matter"},
		},
		AuthCredentialsType: CredentialsType("fake"),
	})

	_, err = provder.TokenVerificationKey(badToken)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "unexpected signing method")

	tokenString, err := token.SignedString(privKey)
	test.That(t, err, test.ShouldBeNil)

	var claims JWTClaims
	_, err = jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
		return verificationKey, nil
	})
	test.That(t, err, test.ShouldBeNil)
}

func TestRSAPublicKeyThumbprint(t *testing.T) {
	privKey1, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)

	privKey2, err := rsa.GenerateKey(rand.Reader, 512)
	test.That(t, err, test.ShouldBeNil)

	thumbPrint1, err := RSAPublicKeyThumbprint(&privKey1.PublicKey)
	test.That(t, err, test.ShouldBeNil)
	thumbPrint2, err := RSAPublicKeyThumbprint(&privKey2.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, thumbPrint1, test.ShouldNotResemble, thumbPrint2)
}
