package secrets

import (
	"context"
	"errors"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"golang.org/x/oauth2/google"
	secretmanagerpb "google.golang.org/genproto/googleapis/cloud/secretmanager/v1"
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

type GCPSecrets struct {
	client *secretmanager.Client
	id     string
}

func NewGCPSecrets(ctx context.Context) (*GCPSecrets, error) {
	c, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	id, err := getProjectID(ctx)
	if err != nil {
		return nil, err
	}

	return &GCPSecrets{c, id}, nil
}

func (g *GCPSecrets) Get(ctx context.Context, name string) (string, error) {
	accessRequest := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", g.id, name),
	}
	result, err := g.client.AccessSecretVersion(ctx, accessRequest)
	if err != nil {
		if status.Convert(errors.Unwrap(err)).Code() == codes.NotFound {
			return "", ErrSecretNotFound
		}
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}
	return string(result.Payload.Data), nil
}

func (g *GCPSecrets) Type() SecretSourceType {
	return SecretSourceTypeGCP
}
