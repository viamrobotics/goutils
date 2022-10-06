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

func TestAPIPRotoJSONEncoding(t *testing.T) {
	t.Run("protojson enabled with proto resp", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: true}
		resp := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		testAPIWithOptionsAndResponse(t, opts, resp, `{"accessToken":"access-token"}`)
	})

	t.Run("protojson enabled with proto array resp", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: true}
		resp := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}

		testAPIWithOptionsAndResponse(t, opts, resp, `[{"accessToken":"access-token1"},{"accessToken":"access-token2"}]`)
	})

	t.Run("protojson enabled with proto array resp with proto names", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: true, JSONOptions: protojson.JSONOptions{UseProtoNames: true}}
		resp := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}

		testAPIWithOptionsAndResponse(t, opts, resp, `[{"access_token":"access-token1"},{"access_token":"access-token2"}]`)
	})

	t.Run("protojson enabled with proto array resp with proto names", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: false}
		resp := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}

		testAPIWithOptionsAndResponse(t, opts, resp, `[{"access_token":"access-token1"},{"access_token":"access-token2"}]`)
	})

	t.Run("protojson enabled with proto field names with proto resp", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: true, JSONOptions: protojson.JSONOptions{UseProtoNames: true}}
		resp := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		testAPIWithOptionsAndResponse(t, opts, resp, `{"access_token":"access-token"}`)
	})

	t.Run("protojson disabled with proto resp", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: false}
		resp := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		testAPIWithOptionsAndResponse(t, opts, resp, `{"access_token":"access-token"}`)
	})

	t.Run("protojson enabled with non-proto resp", func(t *testing.T) {
		opts := protojson.MarshalingOptions{EnableProtoJSON: true}
		resp := struct {
			NameWithSnake string `json:"name_with_snake"`
			ID            string `json:"-"`
			OtherName     string
		}{
			NameWithSnake: "snake",
			ID:            "id-will-be-missing",
			OtherName:     "otherName",
		}
		testAPIWithOptionsAndResponse(t, opts, resp, `{"name_with_snake":"snake","OtherName":"otherName"}`)
	})
}

func testAPIWithOptionsAndResponse(t *testing.T, opts protojson.MarshalingOptions, resp interface{}, expectedBody string) {
	t.Helper()

	logger := golog.NewTestLogger(t)

	mw := APIMiddleware{
		MarshalingOptions: opts,
		Handler:           staticResponseHandler(resp, nil),
		Logger:            logger,
	}

	req, err := http.NewRequest(http.MethodGet, "/test", nil)
	test.That(t, err, test.ShouldBeNil)
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	test.That(t, rr.Body.String(), test.ShouldEqual, expectedBody)
}

func staticResponseHandler(data interface{}, err error) APIHandler {
	return &testHandler{
		h: func(w http.ResponseWriter, r *http.Request) (interface{}, error) {
			return data, err
		},
	}
}

type testHandler struct {
	h func(http.ResponseWriter, *http.Request) (interface{}, error)
}

func (t *testHandler) ServeAPI(w http.ResponseWriter, r *http.Request) (interface{}, error) {
	return t.h(w, r)
}
