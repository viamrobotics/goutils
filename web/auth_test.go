package web

import (
	"net/http"
	"testing"

	"go.viam.com/test"
)

func TestWebAuth(t *testing.T) {
	t.Run("should return nil when token cookie is not set", func(t *testing.T) {
		r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
		if err != nil {
			t.Fatal(err)
		}

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthToken,
			Value:    "",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthRefresh,
			Value:    "",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthExpiry,
			Value:    "123456",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when refresh cookies causes an error", func(t *testing.T) {
		r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
		if err != nil {
			t.Fatal(err)
		}

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthToken,
			Value:    "abc123",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthExpiry,
			Value:    "123456",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return nil when expiry cookie is not set", func(t *testing.T) {
		r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
		if err != nil {
			t.Fatal(err)
		}

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthToken,
			Value:    "abc123",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthRefresh,
			Value:    "",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthExpiry,
			Value:    "",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		data := getAuthCookieValues(r)
		test.That(t, data, test.ShouldBeNil)
	})

	t.Run("should return token response data when cookies are set", func(t *testing.T) {
		r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
		if err != nil {
			t.Fatal(err)
		}

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthToken,
			Value:    "abc123",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthRefresh,
			Value:    "",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		r.AddCookie(&http.Cookie{
			Name:     ViamAuthExpiry,
			Value:    "123456",
			Path:     "/",
			MaxAge:   10000,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})

		data := getAuthCookieValues(r)
		test.That(t, data.AccessToken, test.ShouldEqual, "abc123")
		test.That(t, data.RefreshToken, test.ShouldEqual, "")
		test.That(t, data.Expiry, test.ShouldEqual, "123456")
	})
}
