// Package jwksutils contains helper utilities and tests for the jwks module
package jwksutils

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"github.com/lestrrat-go/jwx/jwk"
	"go.viam.com/test"

	"go.viam.com/utils"
	"go.viam.com/utils/jwks"
)

// ServeFakeOIDCEndpoint is a test helper for serving a OIDC endpoint from a static keyset.
func ServeFakeOIDCEndpoint(t *testing.T, keyset jwks.KeySet) (string, func()) {
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

// NewTestKeySet creates a KeySet with n generated keys with their public keys in the set and returns all rsa.PrivateKey.
// Each key in the JWKS KeySet will be a RSA public key RSA256. Each will have a kid with `key-id-(N+1)`
//
// This should ONLY be used in tests.
func NewTestKeySet(numberOfKeys int) (jwks.KeySet, []*rsa.PrivateKey, error) {
	keyset := jwk.NewSet()

	privKeys := make([]*rsa.PrivateKey, 0, numberOfKeys)
	for i := 0; i < numberOfKeys; i++ {
		// keep keysize small to help make tests faster
		//nolint: gosec
		privKey, err := rsa.GenerateKey(rand.Reader, 512)
		if err != nil {
			return nil, nil, err
		}
		privKeys = append(privKeys, privKey)

		jwkKey, err := jwk.New(privKey.PublicKey)
		if err != nil {
			return nil, nil, err
		}

		err = jwkKey.Set("alg", "RSA256")
		if err != nil {
			return nil, nil, err
		}
		err = jwkKey.Set(jwk.KeyIDKey, fmt.Sprintf("key-id-%d", i+1))
		if err != nil {
			return nil, nil, err
		}

		if !keyset.Add(jwkKey) {
			return nil, nil, errors.New("failed to add key to keyset")
		}
	}

	return keyset, privKeys, nil
}
