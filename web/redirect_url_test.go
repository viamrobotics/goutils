package web

import (
	"testing"

	"go.viam.com/test"
)

func TestIsLocalRedirectPath(t *testing.T) {
	t.Run("rejects external URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("https://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("http://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("ftp://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("www.example.com"), test.ShouldBeFalse)
	})

	t.Run("rejects invalid production URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("http://viam.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("ftp://viam.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("://viam.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//viam.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("viam.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("www.viam.com"), test.ShouldBeFalse)
	})

	t.Run("accepts valid production URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("https://viam.com"), test.ShouldBeTrue)
		test.That(t, IsLocalRedirectPath("https://viam.com/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid staging URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("http://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("ftp://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//viam.dev"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("viam.dev"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("www.viam.dev"), test.ShouldBeFalse)
	})

	t.Run("accepts valid staging URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("https://viam.dev"), test.ShouldBeTrue)
		test.That(t, IsLocalRedirectPath("https://viam.dev/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid local URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("http://localhost"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("ftp://localhost"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("://localhost"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//localhost"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//localhost/some/path"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("localhost"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("localhost/some/path"), test.ShouldBeFalse)
	})

	t.Run("accepts valid local URLs", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("https://localhost"), test.ShouldBeTrue)
		test.That(t, IsLocalRedirectPath("https://localhost/some/path"), test.ShouldBeTrue)
	})
}
