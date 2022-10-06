package protojson

import (
	"testing"

	"go.viam.com/test"

	rpcpb "go.viam.com/utils/proto/rpc/v1"
)

type testData struct {
	NameWithSnake string `json:"name_with_snake"`
	ID            string `json:"-"`
	OtherName     string
}

func TestMarshal(t *testing.T) {
	t.Run("Marshal proto enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}

		validateMarshal(t, Marshaler{Opts: opts}, in, `{"accessToken":"access-token"}`)
	})

	t.Run("Marshal proto disabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: false}
		in := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}

		validateMarshal(t, Marshaler{Opts: opts}, in, `{"access_token":"access-token"}`)
	})

	t.Run("Marshal proto enabled with field names", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true, JSONOptions: JSONOptions{UseProtoNames: true}}
		in := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}

		validateMarshal(t, Marshaler{Opts: opts}, in, `{"access_token":"access-token"}`)
	})

	t.Run("Marshal struct enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true, JSONOptions: JSONOptions{UseProtoNames: true}}
		in := &testData{NameWithSnake: "name", ID: "id", OtherName: "other"}

		validateMarshal(t, Marshaler{Opts: opts}, in, `{"name_with_snake":"name","OtherName":"other"}`)
	})

	t.Run("Marshal proto slice enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}

		validateMarshal(t, Marshaler{Opts: opts}, in, `[{"accessToken":"access-token1"},{"accessToken":"access-token2"}]`)
	})

	t.Run("Marshal proto slice enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := []*testData{
			{NameWithSnake: "name1", ID: "id", OtherName: "other"},
			{NameWithSnake: "name2", ID: "id", OtherName: "other"},
		}

		validateMarshal(t, Marshaler{Opts: opts}, in,
			`[{"name_with_snake":"name1","OtherName":"other"},{"name_with_snake":"name2","OtherName":"other"}]`)
	})

	t.Run("Marshal empty slice", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := []*testData{}

		validateMarshal(t, Marshaler{Opts: opts}, in, `[]`)
	})

	t.Run("Marshal empty proto slice", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := []*rpcpb.AuthenticateResponse{}

		validateMarshal(t, Marshaler{Opts: opts}, in, `[]`)
	})
}

func TestMarshalToInterface(t *testing.T) {
	t.Run("Marshal proto enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		expected := map[string]interface{}{
			"accessToken": "access-token",
		}

		validateMarshalToInterface(t, Marshaler{Opts: opts}, in, expected)
	})

	t.Run("Marshal proto disabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: false}
		in := &rpcpb.AuthenticateResponse{AccessToken: "access-token"}
		expected := map[string]interface{}{
			"access_token": "access-token",
		}

		validateMarshalToInterface(t, Marshaler{Opts: opts}, in, expected)
	})

	t.Run("Marshal proto slice enabled", func(t *testing.T) {
		opts := MarshalingOptions{EnableProtoJSON: true}
		in := []*rpcpb.AuthenticateResponse{
			{AccessToken: "access-token1"},
			{AccessToken: "access-token2"},
		}

		expected := []interface{}{
			map[string]interface{}{"accessToken": "access-token1"},
			map[string]interface{}{"accessToken": "access-token2"},
		}

		validateMarshalToInterface(t, Marshaler{Opts: opts}, in, expected)
	})
}

func validateMarshal(t *testing.T, m Marshaler, data interface{}, expected string) {
	t.Helper()

	out, err := m.Marshal(data)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, string(out), test.ShouldEqual, expected)
}

func validateMarshalToInterface(t *testing.T, m Marshaler, data, expected interface{}) {
	t.Helper()

	out, err := m.MarshalToInterface(data)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, out, test.ShouldResemble, expected)
}
