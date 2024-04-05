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

// TempFile creates a unique temporary file named "something.txt" or fails the test if it
// cannot. It returns the file and a clean-up function.
func TempFile(tb testing.TB) (*os.File, func()) {
	return tempFileNamed(tb, "something.txt")
}

// tempFileNamed creates a unique temporary file with the given name or fails the test if
// it cannot. It returns the file and a clean-up function.
func tempFileNamed(tb testing.TB, name string) (*os.File, func()) {
	tb.Helper()
	tempFile := filepath.Join(tb.TempDir(), name)
	//nolint:gosec
	f, err := os.Create(tempFile)
	test.That(tb, err, test.ShouldBeNil)

	return f, func() {
		test.That(tb, f.Close(), test.ShouldBeNil)
	}
}
