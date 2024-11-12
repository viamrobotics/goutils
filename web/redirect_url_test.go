package web

import (
	"testing"

	"go.viam.com/test"
)

func TestIsLocalRedirectPath(t *testing.T) {
	t.Run("check valid redirect paths", func(t *testing.T) {
		test.That(t, IsLocalRedirectPath("https://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("//example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("ftp://example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("www.example.com"), test.ShouldBeFalse)
		test.That(t, IsLocalRedirectPath("/local/path?myparam=test"), test.ShouldBeTrue)
	})
}
