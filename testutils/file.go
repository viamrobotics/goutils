package testutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.viam.com/test"
)

// TempDirT creates a temporary directory and fails the test if it cannot.
func TempDirT(tb testing.TB, dir, pattern string) string {
	tb.Helper()
	tempDir, err := TempDir(dir, pattern)
	test.That(tb, err, test.ShouldBeNil)
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

// TempFile returns a new unique temporary file using the given name.
func TempFile(tb testing.TB, name string) *os.File {
	tb.Helper()
	tempFile := filepath.Join(tb.TempDir(), name)
	//nolint:gosec
	f, err := os.Create(tempFile)
	test.That(tb, err, test.ShouldBeNil)
	return f
}

// TempFileAndDir created a new unique temporary file in a temporary directory using the
// given name, and fails the test if it cannot. It returns the file, the temporary
// directory path, and a cleanup function.
func TempFileAndDir(tb testing.TB, name string) (*os.File, string, func()) {
	tb.Helper()

	dir := tb.TempDir()
	tempFile := filepath.Join(tb.TempDir(), name)
	//nolint:gosec
	f, err := os.Create(tempFile)
	test.That(tb, err, test.ShouldBeNil)

	return f, dir, func() {
		test.That(tb, f.Close(), test.ShouldBeNil)
	}
}
