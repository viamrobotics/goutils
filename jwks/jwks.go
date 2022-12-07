// Package jwks provides helpers for working with json key sets.
package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/lestrrat-go/jwx/jwk"
	"go.viam.com/test"

	"go.viam.com/utils"
)

// KeySet represents json key set object, a collection of jwk.Key objects.
// See jwk docs. github.com/lestrrat-go/jwx/jwk.
type KeySet jwk.Set

// KeyProvider provides an interface to lookup keys based on a key ID.
// Providers may have a background process to refresh keys and allows
// it to be closed.
type KeyProvider interface {
	// allow users to stop any background process in a key provider.
	io.Closer

	// LookupKey should return a PublicKey based on the given key ID. Return an error if not
	// found or any other error.
	LookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error)

	// Fetch returns the full KeySet as a cloned keyset, any modifcations are only applied locally.
	Fetch(ctx context.Context) (KeySet, error)
}

// ParseKeySet parses a JSON keyset string into a KeySet.
func ParseKeySet(input string) (KeySet, error) {
	return jwk.ParseString(input)
}

// cachingKeyProvider is a key provider that looks up jwk url based on our auth0 config and
// auto refreshes in the background and caches the keys found.
type cachingKeyProvider struct {
	cancel   context.CancelFunc
	ar       *jwk.AutoRefresh
	certsURL string
}

// Stop cancels the auto refresh.
func (cp *cachingKeyProvider) Close() error {
	cp.cancel()
	return nil
}

func (cp *cachingKeyProvider) LookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// loads keys from cache or refreshes if needed.
	keyset, err := cp.ar.Fetch(ctx, cp.certsURL)
	if err != nil {
		return nil, err
	}

	return publicKeyFromKeySet(keyset, kid)
}

func (cp *cachingKeyProvider) Fetch(ctx context.Context) (KeySet, error) {
	// loads keys from cache or refreshes if needed.
	keyset, err := cp.ar.Fetch(ctx, cp.certsURL)
	if err != nil {
		return nil, err
	}

	return keyset.Clone()
}

// ensure interface is met.
var _ KeyProvider = &cachingKeyProvider{}

// NewCachingOIDCJWKKeyProvider creates a CachingKeyProvider based on the auth0 url and starts the auto refresh.
// must call CachingKeyProvider.Stop() to stop background goroutine.
// Use {baseUrl}.well-known/jwks.json.
func NewCachingOIDCJWKKeyProvider(ctx context.Context, baseURL string) (KeyProvider, error) {
	ctx, cancel := context.WithCancel(ctx)

	certsURL := fmt.Sprintf("%s.well-known/jwks.json", baseURL)
	ar := jwk.NewAutoRefresh(ctx)

	// Tell *jwk.AutoRefresh that we only want to refresh this JWKS
	// when it needs to (based on Cache-Control or Expires header from
	// the HTTP response). If the calculated minimum refresh interval is less
	// than 15 minutes, don't go refreshing any earlier than 15 minutes.
	ar.Configure(certsURL, jwk.WithMinRefreshInterval(15*time.Minute))

	// Refresh the JWKS once before we start our service.
	_, err := ar.Refresh(ctx, certsURL)
	if err != nil {
		cancel()
		return nil, err
	}

	return &cachingKeyProvider{
		cancel:   cancel,
		ar:       ar,
		certsURL: certsURL,
	}, nil
}

// wraps a static KeySet.
type staticKeySet struct {
	keyset KeySet
}

// ensure interface is met.
var _ KeyProvider = &staticKeySet{}

func (p *staticKeySet) LookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	return publicKeyFromKeySet(p.keyset, kid)
}

func (p *staticKeySet) Close() error {
	return nil
}

func (p *staticKeySet) Fetch(ctx context.Context) (KeySet, error) {
	// clone to avoid any consumers making changes to the underlying keyset.
	return p.keyset.Clone()
}

// NewStaticJWKKeyProvider create static key provider based on the keyset given.
func NewStaticJWKKeyProvider(keyset KeySet) KeyProvider {
	return &staticKeySet{
		keyset: keyset,
	}
}

func publicKeyFromKeySet(keyset KeySet, kid string) (*rsa.PublicKey, error) {
	key, ok := keyset.LookupKeyID(kid)
	if !ok {
		return nil, errors.New("kid not valid")
	}

	var pubKey rsa.PublicKey
	err := key.Raw(&pubKey)
	if err != nil {
		return nil, errors.New("invalid key type")
	}

	return &pubKey, nil
}

// ServeFakeOIDCEndpoint is a test helper for serving a OIDC endpoint from a static keyset.
func ServeFakeOIDCEndpoint(t *testing.T, keyset KeySet) (string, func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "localhost:0")
	test.That(t, err, test.ShouldBeNil)

	mux := http.NewServeMux()

	baseURL := fmt.Sprintf("http://%s", listener.Addr().String())
	oidcJSON := oidcProviderJSON{
		Issuer:      fmt.Sprintf("%s/", baseURL),
		AuthURL:     fmt.Sprintf("%s/authorize", baseURL),
		TokenURL:    fmt.Sprintf("%s/oauth/token", baseURL),
		JWKSURL:     fmt.Sprintf("%s/.well-known/jwks.json", baseURL),
		UserInfoURL: fmt.Sprintf("%s/userinfo", baseURL),
		Algorithms:  []string{"HS256", "RS256"},
	}

	mux.Handle("/.well-known/openid-configuration", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Typew", "application/json")
		out, err := json.Marshal(oidcJSON)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, err = w.Write(out)
		test.That(t, err, test.ShouldBeNil)
	}))

	mux.Handle("/.well-known/jwks.json", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Typew", "application/json")
		out, err := json.Marshal(keyset)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, err = w.Write(out)
		test.That(t, err, test.ShouldBeNil)
	}))

	logger := golog.NewTestLogger(t)

	server := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: time.Second * 5,
	}

	var exitWg sync.WaitGroup
	exitWg.Add(1)
	utils.PanicCapturingGo(func() {
		defer exitWg.Done()

		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warnf("Error shutting down test OIDC server", "error", err)
		}
	})

	// close listen and wait for goroutine to finish
	closeFunc := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		err := server.Shutdown(ctx)
		if err != nil {
			logger.Warnf("Error shutting down test OIDC server", "error", err)
		}
		exitWg.Wait()
	}

	return fmt.Sprintf("http://%s/", listener.Addr()), closeFunc
}

// Only used for testing.
type oidcProviderJSON struct {
	Issuer      string   `json:"issuer"`
	AuthURL     string   `json:"authorization_endpoint"`
	TokenURL    string   `json:"token_endpoint"`
	JWKSURL     string   `json:"jwks_uri"`
	UserInfoURL string   `json:"userinfo_endpoint"`
	Algorithms  []string `json:"id_token_signing_alg_values_supported"`
}
