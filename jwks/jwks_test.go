package jwks_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/jwk"
	"go.viam.com/test"

	"go.viam.com/utils/jwks"
	"go.viam.com/utils/jwks/jwksutils"
	"go.viam.com/utils/testutils"
)

// fakeIssuer serves OIDC discovery and a JWKS, and can stall either one.
type fakeIssuer struct {
	// url is the issuer as its discovery document names it.
	url  string
	keys jwk.Set

	stallDiscovery atomic.Bool
	stallJWKS      atomic.Bool

	jwksRequests    atomic.Int32
	stalledRequests atomic.Int32
	connections     atomic.Int32
}

func newFakeIssuer(t *testing.T, keys jwk.Set) *fakeIssuer {
	t.Helper()
	issuer := &fakeIssuer{keys: keys}

	release := make(chan struct{})
	// stall holds a request until the client gives up on it or the test ends.
	stall := func(r *http.Request) {
		issuer.stalledRequests.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
	writeJSON := func(w http.ResponseWriter, v any) {
		out, err := json.Marshal(v)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(out)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if issuer.stallDiscovery.Load() {
			stall(r)
			return
		}
		writeJSON(w, map[string]string{"issuer": issuer.url, "jwks_uri": issuer.url + "jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		issuer.jwksRequests.Add(1)
		if issuer.stallJWKS.Load() {
			stall(r)
			return
		}
		writeJSON(w, issuer.keys)
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			issuer.connections.Add(1)
		}
	}
	issuer.url = "http://" + srv.Listener.Addr().String() + "/"
	srv.Start()
	// Cleanups run last in, first out, so stalled handlers are released before Close waits on them.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return issuer
}

// testKeys returns a key set holding key-id-1, plus key-id-2 held back for a test to rotate in.
func testKeys(t *testing.T) (jwk.Set, jwk.Key) {
	t.Helper()
	all, _, err := jwksutils.NewTestKeySet(2)
	test.That(t, err, test.ShouldBeNil)
	first, ok := all.LookupKeyID("key-id-1")
	test.That(t, ok, test.ShouldBeTrue)
	rotated, ok := all.LookupKeyID("key-id-2")
	test.That(t, ok, test.ShouldBeTrue)

	keys := jwk.NewSet()
	test.That(t, keys.Add(first), test.ShouldBeTrue)
	return keys, rotated
}

func TestCachingOIDCJWKKeyProviderOutlivesContext(t *testing.T) {
	keys, rotated := testKeys(t)
	issuer := newFakeIssuer(t, keys)

	ctx, cancel := context.WithCancel(t.Context())
	provider, err := jwks.NewCachingOIDCJWKKeyProviderForTest(ctx, issuer.url, 20*time.Millisecond, 5*time.Second)
	test.That(t, err, test.ShouldBeNil)
	// Like a startup deadline expiring once construction is done.
	cancel()

	test.That(t, keys.Add(rotated), test.ShouldBeTrue)
	// LookupKey only reads the cache, so the rotated key can only come from a background refresh
	// that ran after ctx ended.
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		tb.Helper()
		_, err := provider.LookupKey(t.Context(), "key-id-2", "RS256")
		test.That(tb, err, test.ShouldBeNil)
	})

	// Close is now the only thing that stops the refresh.
	test.That(t, provider.Close(), test.ShouldBeNil)
	time.Sleep(100 * time.Millisecond) // let a refresh already in flight finish
	requests := issuer.jwksRequests.Load()
	time.Sleep(200 * time.Millisecond) // ten refresh intervals
	test.That(t, issuer.jwksRequests.Load(), test.ShouldEqual, requests)
}

func TestCachingOIDCJWKKeyProviderStalledIssuer(t *testing.T) {
	stallDiscovery := func(issuer *fakeIssuer) { issuer.stallDiscovery.Store(true) }
	stallJWKS := func(issuer *fakeIssuer) { issuer.stallJWKS.Store(true) }

	withCallerDeadline := func(t *testing.T, issuer string) (jwks.KeyProvider, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		return jwks.NewCachingOIDCJWKKeyProvider(ctx, issuer)
	}
	// No deadline on ctx, as when a caller passes context.TODO(), so only the client timeout can end it.
	withClientTimeout := func(t *testing.T, issuer string) (jwks.KeyProvider, error) {
		return jwks.NewCachingOIDCJWKKeyProviderForTest(t.Context(), issuer, time.Hour, 100*time.Millisecond)
	}

	for _, tc := range []struct {
		name      string
		stall     func(*fakeIssuer)
		construct func(*testing.T, string) (jwks.KeyProvider, error)
	}{
		{"discovery stalls past caller deadline", stallDiscovery, withCallerDeadline},
		{"jwks stalls past caller deadline", stallJWKS, withCallerDeadline},
		{"discovery stalls past client timeout", stallDiscovery, withClientTimeout},
		{"jwks stalls past client timeout", stallJWKS, withClientTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, _ := testKeys(t)
			issuer := newFakeIssuer(t, keys)
			tc.stall(issuer)

			// Construct in a goroutine so a hang fails here instead of at the test binary's timeout.
			errCh := make(chan error, 1)
			go func() {
				provider, err := tc.construct(t, issuer.url)
				if err == nil {
					provider.Close()
				}
				errCh <- err
			}()
			select {
			case err := <-errCh:
				test.That(t, err, test.ShouldWrap, context.DeadlineExceeded)
			case <-time.After(10 * time.Second):
				t.Fatal("construction still blocked on a stalled issuer after 10s")
			}
		})
	}
}

func TestCachingOIDCJWKKeyProviderRecoversFromStalledRefresh(t *testing.T) {
	keys, rotated := testKeys(t)
	issuer := newFakeIssuer(t, keys)

	provider, err := jwks.NewCachingOIDCJWKKeyProviderForTest(t.Context(), issuer.url, 20*time.Millisecond, 100*time.Millisecond)
	test.That(t, err, test.ShouldBeNil)
	defer provider.Close()

	issuer.stallJWKS.Store(true)
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		tb.Helper()
		test.That(tb, issuer.stalledRequests.Load(), test.ShouldBeGreaterThan, 0)
	})

	// The stalled refresh has to give up for a later one to pick up the rotation.
	test.That(t, keys.Add(rotated), test.ShouldBeTrue)
	issuer.stallJWKS.Store(false)
	testutils.WaitForAssertion(t, func(tb testing.TB) {
		tb.Helper()
		_, err := provider.LookupKey(t.Context(), "key-id-2", "RS256")
		test.That(tb, err, test.ShouldBeNil)
	})
}

func TestCachingOIDCJWKKeyProviderSharesConnection(t *testing.T) {
	keys, _ := testKeys(t)
	issuer := newFakeIssuer(t, keys)

	provider, err := jwks.NewCachingOIDCJWKKeyProvider(t.Context(), issuer.url)
	test.That(t, err, test.ShouldBeNil)
	defer provider.Close()

	// Discovery and the JWKS fetch share one client, so the JWKS fetch reuses discovery's connection.
	test.That(t, issuer.connections.Load(), test.ShouldEqual, int32(1))
}
