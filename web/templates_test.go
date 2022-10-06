package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/edaniels/golog"
	"go.viam.com/test"

	rpcpb "go.viam.com/utils/proto/rpc/v1"
	"go.viam.com/utils/web/protojson"
)

func TestTemlpateProtoJson(t *testing.T) {
	t.Run("test template rendering with protoJson with proto.Message", func(t *testing.T) {
		resp := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		validateTemplateRendering(t, "protoJson.html", resp, `{"accessToken":"access-token"}`)
	})

	t.Run("test template rendering with protoJson with array of proto.Message", func(t *testing.T) {
		resp := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}
		validateTemplateRendering(t, "protoJson.html", resp, `[{"accessToken":"access-token1"},{"accessToken":"access-token2"}]`)
	})

	t.Run("test template rendering with protoJson non proto.Message", func(t *testing.T) {
		resp := struct {
			NameWithSnake string `json:"name_with_snake"`
			ID            string `json:"-"`
			OtherName     string
		}{
			NameWithSnake: "snake",
			ID:            "id-will-be-missing",
			OtherName:     "otherName",
		}
		validateTemplateRendering(t, "protoJson.html", resp, `{"OtherName":"otherName","name_with_snake":"snake"}`)
	})
}

func TestTemlpateJson(t *testing.T) {
	t.Run("test template rendering with json with proto.Message", func(t *testing.T) {
		resp := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		validateTemplateRendering(t, "nonProtoJson.html", resp, `{"access_token":"access-token"}`)
	})

	t.Run("test template rendering with json with array of proto.Message", func(t *testing.T) {
		resp := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}
		validateTemplateRendering(t, "nonProtoJson.html", resp, `[{"access_token":"access-token1"},{"access_token":"access-token2"}]`)
	})

	t.Run("test template rendering with protoJson non proto.Message", func(t *testing.T) {
		resp := struct {
			NameWithSnake string `json:"name_with_snake"`
			ID            string `json:"-"`
			OtherName     string
		}{
			NameWithSnake: "snake",
			ID:            "id-will-be-missing",
			OtherName:     "otherName",
		}
		validateTemplateRendering(t, "nonProtoJson.html", resp, `{"name_with_snake":"snake","OtherName":"otherName"}`)
	})
}

func validateTemplateRendering(t *testing.T, template string, resp interface{}, expectedBody string) {
	t.Helper()

	logger := golog.NewTestLogger(t)

	opts := protojson.MarshalingOptions{EnableProtoJSON: true}
	tm, err := NewTemplateManagerFSWithOptions("testdata/templates", opts)
	test.That(t, err, test.ShouldBeNil)

	mw := NewTemplateMiddleware(tm, staticHandler(template, resp, nil), logger)

	req, err := http.NewRequest(http.MethodGet, "/test", nil)
	test.That(t, err, test.ShouldBeNil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	test.That(t, rr.Code, test.ShouldEqual, 200)
	test.That(t, rr.Body.String(), test.ShouldContainSubstring, expectedBody)
}

func staticHandler(template string, data interface{}, err error) TemplateHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error) {
		return NamedTemplate(template), data, err
	}
}
