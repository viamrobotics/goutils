package web

import (
	"testing"

	"go.viam.com/test"
)

func TestIsValidBacktoURL(t *testing.T) {
	t.Run("rejects external URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("http://example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("example.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("www.example.com"), test.ShouldBeFalse)
	})

	t.Run("rejects invalid production URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("http://viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("www.viam.com"), test.ShouldBeFalse)
	})

	t.Run("accepts valid production URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://viam.com"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://viam.com/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid staging URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("http://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("www.viam.dev"), test.ShouldBeFalse)
	})

	t.Run("accepts valid staging URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://viam.dev"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://viam.dev/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid local URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("http://localhost"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://localhost"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://localhost"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//localhost"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//localhost/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("localhost"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("localhost/some/path"), test.ShouldBeFalse)
	})

	t.Run("accepts valid local URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://localhost"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://localhost/some/path"), test.ShouldBeTrue)
	})
}
