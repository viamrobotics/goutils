// Package jwks provides helpers for working with json key sets.
package jwks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/jwk"
	httphelper "github.com/zitadel/oidc/v3/pkg/http"
	"github.com/zitadel/oidc/v3/pkg/oidc"
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

	// LookupKey should return a public key based on the given key ID. Return an error if not
	// found or any other error.
	LookupKey(ctx context.Context, kid, alg string) (interface{}, error)

	// Fetch returns the full KeySet as a cloned keyset, any modifcations are only applied locally.
	Fetch(ctx context.Context) (KeySet, error)
}

// ParseKeySet parses a JSON keyset string into a KeySet.
func ParseKeySet(input string) (KeySet, error) {
	return jwk.ParseString(input)
}

// cachingKeyProvider is a key provider that looks up jwk's by their kid through the
// configured jwksURI. It auto refreshes in the background and caches the keys found.
type cachingKeyProvider struct {
	cancel     context.CancelFunc
	ar         *jwk.AutoRefresh
	jwksURI    string
	httpClient *http.Client
}

// Close stops the background refresh.
func (cp *cachingKeyProvider) Close() error {
	cp.cancel()
	cp.httpClient.CloseIdleConnections()
	return nil
}

func (cp *cachingKeyProvider) LookupKey(ctx context.Context, kid, alg string) (interface{}, error) {
	// loads keys from cache or refreshes if needed.
	keyset, err := cp.ar.Fetch(ctx, cp.jwksURI)
	if err != nil {
		return nil, err
	}

	return publicKeyFromKeySet(keyset, kid, alg)
}

func (cp *cachingKeyProvider) Fetch(ctx context.Context) (KeySet, error) {
	// loads keys from cache or refreshes if needed.
	keyset, err := cp.ar.Fetch(ctx, cp.jwksURI)
	if err != nil {
		return nil, err
	}

	return keyset.Clone()
}

// ensure interface is met.
var _ KeyProvider = &cachingKeyProvider{}

const (
	// defaultMinRefreshInterval is the least time between background JWKS refreshes.
	defaultMinRefreshInterval = 15 * time.Minute
	// defaultHTTPTimeout bounds each discovery and JWKS request, including reading the body.
	defaultHTTPTimeout = 30 * time.Second
)

// NewCachingOIDCJWKKeyProvider creates a KeyProvider based on the issuer url base domain and
// starts the auto refresh. ctx bounds only the initial discovery and JWKS fetch, so it may carry
// a startup deadline; the background refresh runs until Close is called.
func NewCachingOIDCJWKKeyProvider(ctx context.Context, issuer string) (KeyProvider, error) {
	return newCachingOIDCJWKKeyProvider(ctx, issuer, defaultMinRefreshInterval, defaultHTTPTimeout)
}

func newCachingOIDCJWKKeyProvider(
	ctx context.Context,
	issuer string,
	minRefreshInterval, httpTimeout time.Duration,
) (KeyProvider, error) {
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	// Discovery and every JWKS fetch share this client. jwx runs one background refresh at a time
	// and schedules the next only when the current one returns, so without a timeout a single
	// request stalled on the issuer could stop key rotation indefinitely. Timeout covers reading
	// the body too, which jwx does inside the refresh.
	httpClient := &http.Client{
		Transport: httpTransport,
		Timeout:   httpTimeout,
	}
	defer httpTransport.CloseIdleConnections()

	wellKnown := strings.TrimSuffix(issuer, "/") + oidc.DiscoveryEndpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, err
	}
	discoveryConfig := new(oidc.DiscoveryConfiguration)
	err = httphelper.HttpRequest(httpClient, req, &discoveryConfig)
	if err != nil {
		return nil, err
	}
	if discoveryConfig.Issuer != issuer {
		return nil, oidc.ErrIssuerInvalid
	}

	// The background refresh must outlive ctx, which may carry a startup deadline, so only Close
	// stops it.
	refreshCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	ar := jwk.NewAutoRefresh(refreshCtx)

	// Tell *jwk.AutoRefresh that we only want to refresh this JWKS
	// when it needs to (based on Cache-Control or Expires header from
	// the HTTP response). If the calculated minimum refresh interval is less
	// than 15 minutes, don't go refreshing any earlier than 15 minutes.
	//
	// jwk.WithFetchBackoff is left out on purpose. In jwx v1, a fetch that succeeds returns without
	// stopping the backoff controller it started, and that controller's goroutine then blocks until
	// refreshCtx ends, so every successful refresh would leak a goroutine until Close. Without it,
	// a failed refresh is retried at the next interval while the cached keys stay in use. That only
	// matters if the issuer rotates keys during an outage: tokens signed with the new key are
	// rejected until a refresh succeeds, up to one interval after the issuer is reachable again.
	ar.Configure(discoveryConfig.JwksURI,
		jwk.WithHTTPClient(httpClient),
		jwk.WithMinRefreshInterval(minRefreshInterval),
	)

	// Refresh the JWKS once before we start our service. ctx bounds this fetch.
	if _, err := ar.Refresh(ctx, discoveryConfig.JwksURI); err != nil {
		cancel()
		return nil, err
	}

	return &cachingKeyProvider{
		cancel:     cancel,
		ar:         ar,
		jwksURI:    discoveryConfig.JwksURI,
		httpClient: httpClient,
	}, nil
}

// wraps a static KeySet.
type staticKeySet struct {
	keyset KeySet
}

// ensure interface is met.
var _ KeyProvider = &staticKeySet{}

func (p *staticKeySet) LookupKey(ctx context.Context, kid, alg string) (interface{}, error) {
	return publicKeyFromKeySet(p.keyset, kid, alg)
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

func publicKeyFromKeySet(keyset KeySet, kid, alg string) (interface{}, error) {
	key, ok := keyset.LookupKeyID(kid)
	if !ok {
		return nil, errors.New("kid header does not exist in keyset")
	}

	if key.Algorithm() != alg {
		return nil, errors.New("key from kid has different signing alg")
	}

	var pubKey interface{}
	if err := key.Raw(&pubKey); err != nil {
		return nil, fmt.Errorf("error getting raw key: %w", err)
	}
	return pubKey, nil
}
