package utils

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.viam.com/test"

	"go.viam.com/utils/internal"
)

func TestTryReserveRandomPort(t *testing.T) {
	p, err := TryReserveRandomPort()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, p, test.ShouldBeGreaterThan, 0)
}

func TestGetAllLocalIPv4s(t *testing.T) {
	ips, err := GetAllLocalIPv4s()
	test.That(t, err, test.ShouldBeNil)
	test.That(t, ips, test.ShouldNotBeEmpty)
}

func TestNewPossiblySecureTCPListenerFromFile(t *testing.T) {
	t.Run("providing just cert should fail", func(t *testing.T) {
		_, _, err := NewPossiblySecureTCPListenerFromFile("", "cert", "")
		test.That(t, err, test.ShouldBeError, ErrInsufficientX509KeyPair)
	})

	t.Run("providing just key should fail", func(t *testing.T) {
		_, _, err := NewPossiblySecureTCPListenerFromFile("", "", "key")
		test.That(t, err, test.ShouldBeError, ErrInsufficientX509KeyPair)
	})

	t.Run("no cert or key should be insecure", func(t *testing.T) {
		listener, secure, err := NewPossiblySecureTCPListenerFromFile("", "", "")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, secure, test.ShouldBeFalse)
		test.That(t, listener, test.ShouldNotBeNil)
		test.That(t, listener.Addr().String(), test.ShouldStartWith, "127.0.0.1:")

		httpServer := &http.Server{
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

		serveDone := make(chan struct{})
		go func() {
			defer close(serveDone)
			httpServer.Serve(listener)
		}()

		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://%s", listener.Addr().String()))
		test.That(t, err, test.ShouldBeNil)
		defer resp.Body.Close()
		test.That(t, resp.StatusCode, test.ShouldEqual, http.StatusOK)

		test.That(t, httpServer.Shutdown(context.Background()), test.ShouldBeNil)
		<-serveDone
	})

	t.Run("with cert and key should be secure", func(t *testing.T) {
		listener, secure, err := NewPossiblySecureTCPListenerFromFile(
			"",
			internal.ResolveFile("testdata/cert.pem"),
			internal.ResolveFile("testdata/key.pem"),
		)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, secure, test.ShouldBeTrue)
		test.That(t, listener, test.ShouldNotBeNil)

		httpServer := &http.Server{
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

		serveDone := make(chan struct{})
		go func() {
			defer close(serveDone)
			httpServer.Serve(listener)
		}()

		customTransport := http.DefaultTransport.(*http.Transport).Clone()
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client := &http.Client{Transport: customTransport}
		resp, err := client.Get(fmt.Sprintf("https://%s", listener.Addr().String()))
		test.That(t, err, test.ShouldBeNil)
		defer resp.Body.Close()
		test.That(t, resp.StatusCode, test.ShouldEqual, http.StatusOK)

		test.That(t, httpServer.Shutdown(context.Background()), test.ShouldBeNil)
		<-serveDone
	})
}
