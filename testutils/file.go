package testutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.viam.com/test"
)

// TempDirT creates a temporary directory and fails the test if it cannot.
func TempDirT(t *testing.T, dir, pattern string) string {
	t.Helper()
	tempDir, err := TempDir(dir, pattern)
	test.That(t, err, test.ShouldBeNil)
	return tempDir
}

// TempDir creates a temporary directory and fails the test if it cannot.
func TempDir(dir, pattern string) (string, error) {
	var err error

	if os.Getenv("USER") == "" || filepath.IsAbs(dir) {
		dir, err = os.MkdirTemp(dir, pattern)
	} else {
		dir = filepath.Join("/tmp", fmt.Sprintf("viam-test-%s-%s-%s", os.Getenv("USER"), dir, pattern))
		err = os.MkdirAll(dir, 0o750)
	}
	return dir, err
}
