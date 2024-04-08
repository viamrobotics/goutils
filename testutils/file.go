package testutils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
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
	tb.Helper()
	tempFile := filepath.Join(tb.TempDir(), "something.txt")
	//nolint:gosec
	f, err := os.Create(tempFile)
	test.That(tb, err, test.ShouldBeNil)

	return f, func() {
		test.That(tb, f.Close(), test.ShouldBeNil)
		// Since the file was placed in a directory that was created via TB.TempDir, it
		// will automatically be deleted after the test and all its subtests complete, so
		// we do not need to remove it manually.
	}
}

// WatchedFiles creates a file watcher and n unique temporary files all named
// "something.txt", or fails the test if it cannot. It returns the watcher, a slice of
// files, and a clean-up function.
//
// For safety, this function will not create more than 50 files.
func WatchedFiles(tb testing.TB, n int) (*fsnotify.Watcher, []*os.File, func()) {
	tb.Helper()

	if n > 50 {
		tb.Fatal("will not create more than 50 temporary files, sorry")
	}

	watcher, err := fsnotify.NewWatcher()
	test.That(tb, err, test.ShouldBeNil)

	var tempFiles []*os.File
	var cleanupTFs []func()

	for i := 0; i < n; i++ {
		f, cleanup := TempFile(tb)
		tempFiles = append(tempFiles, f)
		cleanupTFs = append(cleanupTFs, cleanup)

		watcher.Add(f.Name())
	}

	return watcher, tempFiles, func() {
		test.That(tb, watcher.Close(), test.ShouldBeNil)
		for _, cleanup := range cleanupTFs {
			cleanup()
		}
	}
}

// WatchedFile creates a file watcher and a unique temporary file named "something.txt",
// or fails the test if it cannot. It returns the watcher, the file, and a clean-up
// function.
func WatchedFile(tb testing.TB) (*fsnotify.Watcher, *os.File, func()) {
	tb.Helper()

	watcher, tempFiles, cleanup := WatchedFiles(tb, 1)

	count := len(tempFiles)
	if count != 1 {
		defer cleanup()
		tb.Fatalf("expected to create exactly 1 temporary file but created %d", count)
	}

	return watcher, tempFiles[0], cleanup
}
