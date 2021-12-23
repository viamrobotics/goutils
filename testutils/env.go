package testutils

import (
	"net"
	"os"
	"testing"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	"go.viam.com/test"
)

var (
	logger            = golog.Global.Named("test")
	noSkip            = false
	internetConnected *bool
)

func skipWithError(t *testing.T, err error) {
	t.Helper()
	if noSkip {
		t.Fatal(err)
		return
	}
	t.Skip(err)
}

// SkipUnlessInternet verifies there is an internet connection.
func SkipUnlessInternet(t *testing.T) {
	t.Helper()
	if internetConnected == nil {
		var connected bool
		conn, err := net.Dial("tcp", "mozilla.org:80")
		if err == nil {
			test.That(t, conn.Close(), test.ShouldBeNil)
			connected = true
		}
		internetConnected = &connected
	}
	if *internetConnected {
		return
	}
	skipWithError(t, errors.New("internet not connected"))
}

func artifactGoogleCreds() (string, error) {
	creds, ok := os.LookupEnv("ARTIFACT_GOOGLE_APPLICATION_CREDENTIALS")
	if !ok || creds == "" {
		return "", errors.New("no artifact google credentials found")
	}
	return creds, nil
}

// SkipUnlessArtifactGoogleCreds verifies google credentials are available for artifact.
func SkipUnlessArtifactGoogleCreds(t *testing.T) {
	t.Helper()
	_, err := artifactGoogleCreds()
	if err == nil {
		return
	}
	skipWithError(t, err)
}

// ArtifactGoogleCreds returns the google credentials for artifact.
func ArtifactGoogleCreds(t *testing.T) string {
	t.Helper()
	creds, err := artifactGoogleCreds()
	if err != nil {
		skipWithError(t, err)
		return ""
	}
	return creds
}

func backingMongoDBURI() (string, error) {
	mongoURI, ok := os.LookupEnv("TEST_MONGODB_URI")
	if !ok || mongoURI == "" {
		return "", errors.New("no MongoDB URI found")
	}
	randomizeMongoDBNamespaces()
	return mongoURI, nil
}

// SkipUnlessBackingMongoDBURI verifies there is a backing MongoDB URI to use.
func SkipUnlessBackingMongoDBURI(t *testing.T) {
	t.Helper()
	_, err := backingMongoDBURI()
	if err == nil {
		return
	}
	skipWithError(t, err)
}

// BackingMongoDBURI returns the backing MongoDB URI to use.
func BackingMongoDBURI(t *testing.T) string {
	t.Helper()
	mongoURI, err := backingMongoDBURI()
	if err != nil {
		skipWithError(t, err)
		return ""
	}
	return mongoURI
}
