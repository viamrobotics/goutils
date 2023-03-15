package jwksutils

import (
	"context"
	"testing"

	"go.viam.com/test"

	"go.viam.com/utils/jwks"
)

func TestFetch(t *testing.T) {
	ctx := context.Background()

	set, _, err := NewTestKeySet(2)
	test.That(t, err, test.ShouldBeNil)

	address, closeFakeOIDC := ServeFakeOIDCEndpoint(t, set)
	defer closeFakeOIDC()

	keyProvider, err := jwks.NewCachingOIDCJWKKeyProvider(ctx, address)
	test.That(t, err, test.ShouldBeNil)

	defer keyProvider.Close()

	keyset, err := keyProvider.Fetch(ctx)
	test.That(t, err, test.ShouldBeNil)

	_, ok := keyset.LookupKeyID("key-id-1")
	test.That(t, ok, test.ShouldBeTrue)

	_, ok = keyset.LookupKeyID("key-id-2")
	test.That(t, ok, test.ShouldBeTrue)
}

func TestStaticKeySet(t *testing.T) {
	ctx := context.Background()

	set, keys, err := NewTestKeySet(2)
	test.That(t, err, test.ShouldBeNil)

	keyProvider := jwks.NewStaticJWKKeyProvider(set)

	publicKey1, err := keyProvider.LookupKey(ctx, "key-id-1")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey1.N, test.ShouldResemble, keys[0].PublicKey.N)

	publicKey2, err := keyProvider.LookupKey(ctx, "key-id-2")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey2.N, test.ShouldResemble, keys[1].PublicKey.N)

	_, err = keyProvider.LookupKey(ctx, "not-a-key")
	test.That(t, err.Error(), test.ShouldContainSubstring, "kid header does not exist")

	test.That(t, keyProvider.Close(), test.ShouldBeNil)
}

func TestOIDCRefreshingKeySet(t *testing.T) {
	ctx := context.Background()

	set, keys, err := NewTestKeySet(2)
	test.That(t, err, test.ShouldBeNil)

	address, closeFakeOIDC := ServeFakeOIDCEndpoint(t, set)
	defer closeFakeOIDC()

	keyProvider, err := jwks.NewCachingOIDCJWKKeyProvider(ctx, address)
	test.That(t, err, test.ShouldBeNil)

	defer keyProvider.Close()

	publicKey1, err := keyProvider.LookupKey(ctx, "key-id-1")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey1.N, test.ShouldResemble, keys[0].PublicKey.N)

	publicKey2, err := keyProvider.LookupKey(ctx, "key-id-2")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey2.N, test.ShouldResemble, keys[1].PublicKey.N)

	_, err = keyProvider.LookupKey(ctx, "not-a-key")
	test.That(t, err.Error(), test.ShouldContainSubstring, "kid header does not exist")
}
