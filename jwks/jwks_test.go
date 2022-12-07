package jwks

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/lestrrat-go/jwx/jwk"
	"go.viam.com/test"
)

func TestStaticKeySet(t *testing.T) {
	set := jwk.NewSet()
	ctx := context.Background()

	key1 := createKeyToKeySet(t, set, "my-keyid-1")
	key2 := createKeyToKeySet(t, set, "my-keyid-2")

	keyProvider := NewStaticJWKKeyProvider(set)

	publicKey1, err := keyProvider.LookupKey(ctx, "my-keyid-1")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey1.N, test.ShouldResemble, key1.PublicKey.N)

	publicKey2, err := keyProvider.LookupKey(ctx, "my-keyid-2")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey2.N, test.ShouldResemble, key2.PublicKey.N)

	_, err = keyProvider.LookupKey(ctx, "not-a-key")
	test.That(t, err.Error(), test.ShouldContainSubstring, "kid not valid")

	test.That(t, keyProvider.Close(), test.ShouldBeNil)
}

func TestOIDCRefreshingKeySet(t *testing.T) {
	set := jwk.NewSet()
	ctx := context.Background()

	key1 := createKeyToKeySet(t, set, "my-keyid-1")
	key2 := createKeyToKeySet(t, set, "my-keyid-2")

	address, closeFakeOIDC := ServeFakeOIDCEndpoint(t, set)
	defer closeFakeOIDC()

	keyProvider, err := NewCachingOIDCJWKKeyProvider(ctx, address)
	test.That(t, err, test.ShouldBeNil)

	defer keyProvider.Close()

	publicKey1, err := keyProvider.LookupKey(ctx, "my-keyid-1")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey1.N, test.ShouldResemble, key1.PublicKey.N)

	publicKey2, err := keyProvider.LookupKey(ctx, "my-keyid-2")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, publicKey2.N, test.ShouldResemble, key2.PublicKey.N)

	_, err = keyProvider.LookupKey(ctx, "not-a-key")
	test.That(t, err.Error(), test.ShouldContainSubstring, "kid not valid")
}

func createKeyToKeySet(t *testing.T, set jwk.Set, kid string) *rsa.PrivateKey {
	t.Helper()

	raw, err := rsa.GenerateKey(rand.Reader, 4096)
	test.That(t, err, test.ShouldBeNil)

	key, err := jwk.New(raw.PublicKey)
	test.That(t, err, test.ShouldBeNil)

	key.Set(jwk.KeyIDKey, kid)
	test.That(t, set.Add(key), test.ShouldBeTrue)

	return raw
}

func TestFetch(t *testing.T) {
	set := jwk.NewSet()
	ctx := context.Background()

	createKeyToKeySet(t, set, "my-keyid-1")
	createKeyToKeySet(t, set, "my-keyid-2")

	address, closeFakeOIDC := ServeFakeOIDCEndpoint(t, set)
	defer closeFakeOIDC()

	keyProvider, err := NewCachingOIDCJWKKeyProvider(ctx, address)
	test.That(t, err, test.ShouldBeNil)

	defer keyProvider.Close()

	keyset, err := keyProvider.Fetch(ctx)
	test.That(t, err, test.ShouldBeNil)

	_, ok := keyset.LookupKeyID("my-keyid-1")
	test.That(t, ok, test.ShouldBeTrue)

	_, ok = keyset.LookupKeyID("my-keyid-2")
	test.That(t, ok, test.ShouldBeTrue)
}
