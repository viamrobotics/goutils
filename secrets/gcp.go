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

const (
	maxRetries     = 10
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 5 * time.Second
)

// withRetry runs fn up to maxRetries times with exponential backoff.
// fn should return nil error on success, or a non-nil error to retry.
// To stop retrying early, fn should call the provided stop function
// with the final error before returning.
func withRetry(ctx context.Context, fn func(stop func(error)) error) error {
	var finalErr error
	stopped := false
	stop := func(err error) {
		finalErr = err
		stopped = true
	}

	backoff := initialBackoff
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		err := fn(stop)
		if err == nil {
			return nil
		}
		if stopped {
			return finalErr
		}
		finalErr = err
	}
	return finalErr
}

func getProjectID(ctx context.Context) (string, error) {
	credentials, err := google.FindDefaultCredentials(ctx)
	if err != nil {
		return "", err
	}
	if credentials.ProjectID == "" {
		return "", errors.New("credentials returned empty project ID")
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
// the given context. Initialization is retried with exponential backoff to handle
// GKE environments where the metadata server may not be ready on freshly scaled nodes.
func NewGCPSource(ctx context.Context) (*GCPSource, error) {
	// 5 retries with 1s timeout
	// exponential backoff with a base of 50ms and a +/- 10% jitter
	retryInterceptor := grpc_retry.UnaryClientInterceptor(
		grpc_retry.WithPerRetryTimeout(time.Second),
		grpc_retry.WithMax(5),
		grpc_retry.WithBackoff(grpc_retry.BackoffExponentialWithJitter(50*time.Millisecond, 0.1)),
	)

	var source *GCPSource
	err := withRetry(ctx, func(_ func(error)) error {
		c, err := secretmanager.NewClient(ctx, option.WithGRPCDialOption(grpc.WithChainUnaryInterceptor(retryInterceptor)))
		if err != nil {
			return err
		}

		id, err := getProjectID(ctx)
		if err != nil {
			return errors.Join(err, c.Close())
		}

		source = &GCPSource{c, id}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GCP secret source: %w", err)
	}
	return source, nil
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

// Get looks up the given name as a secret in GCP.
func (g *GCPSource) Get(ctx context.Context, name string) (string, error) {
	accessRequest := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", g.id, name),
	}

	var secret string
	err := withRetry(ctx, func(stop func(error)) error {
		result, err := g.client.AccessSecretVersion(ctx, accessRequest)
		if err == nil {
			secret = string(result.GetPayload().GetData())
			return nil
		}

		if status.Code(err) == codes.NotFound {
			stop(ErrNotFound)
			return err
		}
		if nonRetriableError(err) {
			stop(fmt.Errorf("failed to access secret version: %w", err))
			return err
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

// Type returns the type of this source (gcp).
func (g *GCPSource) Type() SourceType {
	return SourceTypeGCP
}
