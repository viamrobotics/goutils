package secrets

import (
	"context"
	"errors"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"golang.org/x/oauth2/google"
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

// GCPSource provides secrets via GCP secrets.
type GCPSource struct {
	client *secretmanager.Client
	id     string
}

// NewGCPSource returns a new GCP secret source that derives its information from
// the given context.
func NewGCPSource(ctx context.Context) (*GCPSource, error) {
	c, err := secretmanager.NewClient(ctx)
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

// Get looks up the given name as a secret in GCP.
func (g *GCPSource) Get(ctx context.Context, name string) (string, error) {
	accessRequest := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", g.id, name),
	}
	result, err := g.client.AccessSecretVersion(ctx, accessRequest)
	if err != nil {
		if status.Convert(errors.Unwrap(err)).Code() == codes.NotFound {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}
	return string(result.GetPayload().GetData()), nil
}

// Type returns the type of this source (gcp).
func (g *GCPSource) Type() SourceType {
	return SourceTypeGCP
}
