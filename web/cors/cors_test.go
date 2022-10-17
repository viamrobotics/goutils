package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.viam.com/test"
)

func TestCors(t *testing.T) {
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("without cors handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Add("Access-Control-Request-Method", "GET")
		req.Header.Add("Origin", "http://test.viam.com")
		w := httptest.NewRecorder()

		apiHandler.ServeHTTP(w, req)
		test.That(t, w.Header().Get("Access-Control-Allow-Origin"), test.ShouldEqual, "")
	})

	t.Run("with AllowAll cors", func(t *testing.T) {
		allowAll := AllowAll()

		req := httptest.NewRequest(http.MethodOptions, "/test", nil)
		req.Header.Add("Access-Control-Request-Method", "GET")
		req.Header.Add("Origin", "http://test.viam.com")
		w := httptest.NewRecorder()

		allowAll.Handler(apiHandler).ServeHTTP(w, req)
		test.That(t, w.Header().Get("Access-Control-Allow-Origin"), test.ShouldEqual, "*")
		test.That(t, w.Header().Get("Access-Control-Allow-Private-Network"), test.ShouldEqual, "")
		test.That(t, w.Header().Get("Access-Control-Allow-Methods"), test.ShouldEqual, http.MethodGet)
	})

	t.Run("with AllowAll cors, api request doesn't have CORs", func(t *testing.T) {
		allowAll := AllowAll()

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("Origin", "http://test.viam.com")
		w := httptest.NewRecorder()

		allowAll.Handler(apiHandler).ServeHTTP(w, req)
		test.That(t, w.Header().Get("Access-Control-Allow-Origin"), test.ShouldEqual, "*")
		test.That(t, w.Header().Get("Access-Control-Allow-Methods"), test.ShouldEqual, "")
		test.That(t, w.Header().Get("Access-Control-Allow-Headers"), test.ShouldEqual, "")
	})
}
