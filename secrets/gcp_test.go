package secrets

import (
	"context"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	"go.viam.com/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockSecretClient struct {
	calls    int
	failFor  int
	failErr  error
	response *secretmanagerpb.AccessSecretVersionResponse
}

func (m *mockSecretClient) AccessSecretVersion(
	ctx context.Context,
	req *secretmanagerpb.AccessSecretVersionRequest,
	opts ...gax.CallOption,
) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	m.calls++
	if m.calls <= m.failFor {
		return nil, m.failErr
	}
	return m.response, nil
}

func (m *mockSecretClient) Close() error {
	return nil
}

func TestGCPSourceGet_RetriesTransientUnauthenticated(t *testing.T) {
	// Simulates the GKE metadata server race: per-RPC creds fail with
	// Unauthenticated on a fresh node, then succeed once the metadata
	// server is ready.
	mock := &mockSecretClient{
		failFor: 3,
		failErr: status.Error(
			codes.Unauthenticated,
			"per-RPC creds failed: cannot fetch token",
		),
		response: &secretmanagerpb.AccessSecretVersionResponse{
			Payload: &secretmanagerpb.SecretPayload{
				Data: []byte("mongodb://localhost:27017"),
			},
		},
	}
	source := &GCPSource{client: mock, id: "test-project"}

	val, err := source.Get(context.Background(), "mongourl")
	test.That(t, err, test.ShouldBeNil)
	test.That(t, val, test.ShouldEqual, "mongodb://localhost:27017")
	test.That(t, mock.calls, test.ShouldEqual, 4)
}
