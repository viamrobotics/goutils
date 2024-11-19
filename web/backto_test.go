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
		test.That(t, IsValidBacktoURL("http://app.viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://app.viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://app.viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//app.viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//app.viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("app.viam.com"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("app.viam.com/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("www.app.viam.com"), test.ShouldBeFalse)
	})

	t.Run("accepts valid production URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://app.viam.com"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://app.viam.com/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid staging URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("http://app.viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://app.viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://app.viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//app.viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//app.viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("app.viam.dev"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("app.viam.dev/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("www.app.viam.dev"), test.ShouldBeFalse)
	})

	t.Run("accepts valid staging URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://app.viam.dev"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://app.viam.dev/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid local URLs", func(t *testing.T) {
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
		test.That(t, IsValidBacktoURL("http://localhost"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("http://localhost/some/path"), test.ShouldBeTrue)
	})

	t.Run("rejects invalid temp PR env URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("http://pr-1-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("ftp://pr-12-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("://pr-123-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//pr-1234-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("//pr-12345-appmain-bplesliplq-uc.a.run.app/some/path"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("pr-1234-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeFalse)
		test.That(t, IsValidBacktoURL("pr-123-appmain-bplesliplq-uc.a.run.app/some/path"), test.ShouldBeFalse)
	})

	t.Run("accepts valid temp PR env URLs", func(t *testing.T) {
		test.That(t, IsValidBacktoURL("https://pr-12345-appmain-bplesliplq-uc.a.run.app"), test.ShouldBeTrue)
		test.That(t, IsValidBacktoURL("https://pr-6789-appmain-bplesliplq-uc.a.run.app/some/path"), test.ShouldBeTrue)
	})
}
