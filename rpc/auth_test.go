package rpc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/go-errors/errors"
	"github.com/golang-jwt/jwt/v4"
	"go.viam.com/test"
)

func TestMakeFuncAuthHandler(t *testing.T) {
	expectedEntity := "foo"
	expectedPayload := "bar"
	err1 := errors.New("nope1")
	err2 := errors.New("nope2")
	handler := MakeFuncAuthHandler(
		func(ctx context.Context, entity, payload string) error {
			if entity == expectedEntity && payload == expectedPayload {
				return nil
			}
			return err1
		},
		func(ctx context.Context, entity string) error {
			if entity == expectedEntity {
				return nil
			}
			return err2
		},
	)

	err := handler.Authenticate(context.Background(), "one", "two")
	test.That(t, err, test.ShouldBeError, err1)
	err = handler.Authenticate(context.Background(), expectedEntity, "two")
	test.That(t, err, test.ShouldBeError, err1)
	err = handler.Authenticate(context.Background(), expectedEntity, expectedPayload)
	test.That(t, err, test.ShouldBeNil)

	err = handler.VerifyEntity(context.Background(), "one")
	test.That(t, err, test.ShouldBeError, err2)
	err = handler.VerifyEntity(context.Background(), expectedEntity)
	test.That(t, err, test.ShouldBeNil)
}

func TestMakeSimpleAuthHandler(t *testing.T) {
	t.Run("with no entities should always fail", func(t *testing.T) {
		handler := MakeSimpleAuthHandler(nil, "something")
		test.That(t, handler.Authenticate(context.Background(), "", "something"), test.ShouldNotBeNil)
		test.That(t, handler.Authenticate(context.Background(), "entity", "something"), test.ShouldNotBeNil)
		test.That(t, handler.VerifyEntity(context.Background(), ""), test.ShouldNotBeNil)
		test.That(t, handler.VerifyEntity(context.Background(), "entity"), test.ShouldNotBeNil)
	})

	t.Run("should validate entities and key", func(t *testing.T) {
		expectedEntities := []string{"one", "two", "three"}
		expectedKey := "mykey"
		handler := MakeSimpleAuthHandler(expectedEntities, expectedKey)

		for _, ent := range expectedEntities {
			test.That(t, handler.Authenticate(context.Background(), ent, expectedKey), test.ShouldBeNil)
			test.That(t, handler.Authenticate(context.Background(), ent, expectedKey+"1"), test.ShouldEqual, errInvalidCredentials)
			test.That(t, handler.VerifyEntity(context.Background(), ent), test.ShouldBeNil)
		}
		test.That(t, handler.Authenticate(context.Background(), "notent", expectedKey), test.ShouldBeError, errInvalidCredentials)
		test.That(t, handler.VerifyEntity(context.Background(), "notent"), test.ShouldBeError, errSessionEntityHandlerMismatch)
	})
}

func TestWithTokenVerificationKeyProvider(t *testing.T) {
	handler := MakeSimpleAuthHandler([]string{"one"}, "key")
	err1 := errors.New("whoops")
	wrappedHandler := WithTokenVerificationKeyProvider(handler, func(token *jwt.Token) (interface{}, error) {
		return nil, err1
	})
	test.That(t, wrappedHandler.Authenticate(context.Background(), "one", "key"), test.ShouldBeNil)
	test.That(t, wrappedHandler.VerifyEntity(context.Background(), "one"), test.ShouldBeNil)
	_, err := wrappedHandler.(TokenVerificationKeyProvider).TokenVerificationKey(nil)
	test.That(t, err, test.ShouldEqual, err1)
}

func TestWithPublicKeyProvider(t *testing.T) {
	handler := MakeSimpleAuthHandler([]string{"one"}, "key")
	privKey, err := rsa.GenerateKey(rand.Reader, generatedRSAKeyBits)
	test.That(t, err, test.ShouldBeNil)
	pubKey := &privKey.PublicKey
	wrappedHandler := WithPublicKeyProvider(handler, pubKey)
	test.That(t, wrappedHandler.Authenticate(context.Background(), "one", "key"), test.ShouldBeNil)
	test.That(t, wrappedHandler.VerifyEntity(context.Background(), "one"), test.ShouldBeNil)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, rpcClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{"does not matter"},
		},
		CredentialsType: CredentialsType("fake"),
	})

	provder := wrappedHandler.(TokenVerificationKeyProvider)

	verificationKey, err := provder.TokenVerificationKey(token)
	test.That(t, err, test.ShouldBeNil)

	badToken := jwt.NewWithClaims(jwt.SigningMethodHS256, rpcClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{"does not matter"},
		},
		CredentialsType: CredentialsType("fake"),
	})

	_, err = provder.TokenVerificationKey(badToken)
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, err.Error(), test.ShouldContainSubstring, "unexpected signing method")

	tokenString, err := token.SignedString(privKey)
	test.That(t, err, test.ShouldBeNil)

	var claims rpcClaims
	_, err = jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
		return verificationKey, nil
	})
	test.That(t, err, test.ShouldBeNil)
}
