package web

import (
	"net/http"
	"testing"

	"go.viam.com/test"
)

func createRequest(t *testing.T) *http.Request {
	r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
	if err != nil {
		t.Fatal(err)
		return nil
	}

	return r
}

func setCookie(r *http.Request, key, value string) {
	r.AddCookie(&http.Cookie{
		Name:     key,
		Value:    value,
		Path:     "/",
		MaxAge:   10000,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})
}

func TestWebAuth(t *testing.T) {
	t.Run("should return nil when token cookie is not present", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamRefreshCookie, "")
		setCookie(r, ViamExpiryCookie, "123456")

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when token cookie is empty", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamTokenCookie, "")
		setCookie(r, ViamRefreshCookie, "")
		setCookie(r, ViamExpiryCookie, "123456")

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when refresh cookies is not present", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamTokenCookie, "abc123")
		setCookie(r, ViamExpiryCookie, "123456")

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when expiry cookie is not present", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamTokenCookie, "abc123")
		setCookie(r, ViamRefreshCookie, "")

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when expiry cookie is empty", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamTokenCookie, "abc123")
		setCookie(r, ViamRefreshCookie, "")
		setCookie(r, ViamExpiryCookie, "")

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return token response data when cookies are set", func(t *testing.T) {
		r := createRequest(t)
		setCookie(r, ViamTokenCookie, "abc123")
		setCookie(r, ViamRefreshCookie, "")
		setCookie(r, ViamExpiryCookie, "123456")

		data := getAuthCookieValues(r)
		test.That(t, data.AccessToken, test.ShouldEqual, "abc123")
		test.That(t, data.RefreshToken, test.ShouldEqual, "")
		test.That(t, data.Expiry, test.ShouldEqual, "123456")
	})
}