package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func getProjectID(ctx context.Context) (string, error) {
	credentials, err := google.FindDefaultCredentials(ctx)
	if err != nil {
		return "", err
	}
	return credentials.ProjectID, nil
}

// secretManagerClient is the subset of the Secret Manager API used by GCPSource.
type secretManagerClient interface {
	AccessSecretVersion(
		ctx context.Context,
		req *secretmanagerpb.AccessSecretVersionRequest,
		opts ...gax.CallOption,
	) (*secretmanagerpb.AccessSecretVersionResponse, error)
	Close() error
}

// GCPSource provides secrets via GCP secrets.
type GCPSource struct {
	client secretManagerClient
	id     string
}

// NewGCPSource returns a new GCP secret source that derives its information from
// the given context.
func NewGCPSource(ctx context.Context) (*GCPSource, error) {
	// 5 retries with 1s timeout
	// exponential backoff with a base of 50ms and a +/- 10% jitter
	retryInterceptor := grpc_retry.UnaryClientInterceptor(
		grpc_retry.WithPerRetryTimeout(time.Second),
		grpc_retry.WithMax(5),
		grpc_retry.WithBackoff(grpc_retry.BackoffExponentialWithJitter(50*time.Millisecond, 0.1)),
	)

	c, err := secretmanager.NewClient(ctx, option.WithGRPCDialOption(grpc.WithChainUnaryInterceptor(retryInterceptor)))
	if err != nil {
		return nil, err
	}

	id, err := getProjectID(ctx)
	if err != nil {
		return nil, err
	}

	return &GCPSource{c, id}, nil
}

// Close closes the underlying GCP client.
func (g *GCPSource) Close() error {
	return g.client.Close()
}

// nonRetriableError returns true for errors that indicate the request itself is
// invalid and retrying will not help (e.g. secret not found, permission denied).
func nonRetriableError(err error) bool {
	code := status.Code(err)
	return code == codes.NotFound ||
		code == codes.PermissionDenied ||
		code == codes.InvalidArgument
}

const (
	getMaxRetries     = 10
	getInitialBackoff = 500 * time.Millisecond
	getMaxBackoff     = 5 * time.Second
)

// Get looks up the given name as a secret in GCP.
func (g *GCPSource) Get(ctx context.Context, name string) (string, error) {
	accessRequest := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", g.id, name),
	}

	var lastErr error
	backoff := getInitialBackoff
	for attempt := 0; attempt <= getMaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("failed to access secret version: %w", lastErr)
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > getMaxBackoff {
				backoff = getMaxBackoff
			}
		}

		result, err := g.client.AccessSecretVersion(ctx, accessRequest)
		if err == nil {
			return string(result.GetPayload().GetData()), nil
		}

		code := status.Code(err)
		if code == codes.OK {
			code = status.Convert(errors.Unwrap(err)).Code()
		}
		if code == codes.NotFound {
			return "", ErrNotFound
		}
		if nonRetriableError(err) {
			return "", fmt.Errorf("failed to access secret version: %w", err)
		}
		lastErr = err
	}
	return "", fmt.Errorf("failed to access secret version: %w", lastErr)
}

// Type returns the type of this source (gcp).
func (g *GCPSource) Type() SourceType {
	return SourceTypeGCP
}
