package artifact

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"go.viam.com/utils"
)

// newFileSystemStore returns a new fileSystemStore based on the given config.
func newFileSystemStore(config *FileSystemStoreConfig) (*fileSystemStore, error) {
	dirStat, err := os.Stat(config.Path)
	if err == nil && !dirStat.IsDir() {
		return nil, errors.Errorf("expected path to be directory %q", config.Path)
	} else if err != nil {
		if err := os.MkdirAll(config.Path, 0o750); err != nil {
			return nil, err
		}
	}
	return &fileSystemStore{dir: config.Path}, nil
}

// A fileSystemStore stores artifacts in a local file system. It generally
// stores artifacts by their node hash but can also emplace those same files
// in a directory structure.
type fileSystemStore struct {
	dir string
}

func (s *fileSystemStore) Contains(hash string) error {
	fileInfos, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, info := range fileInfos {
		if info.Name() == hash {
			return nil
		}
	}
	return NewArtifactNotFoundHashError(hash)
}

func (s *fileSystemStore) pathToHashFile(hash string) string {
	return filepath.Join(s.dir, hash)
}

func (s *fileSystemStore) Load(hash string) (io.ReadCloser, error) {
	if err := s.Contains(hash); err != nil {
		return nil, err
	}
	return os.Open(s.pathToHashFile(hash))
}

// AtomicStore writes reader contents to a temp file and then renames to
// path, ensuring safer, atomic file writes.
func AtomicStore(path string, r io.Reader, hash string) (err error) {
	tempFile, err := os.CreateTemp(filepath.Dir(path), hash)
	if err != nil {
		return err
	}

	var successful bool
	defer func() {
		if !successful {
			utils.UncheckedError(os.Remove(tempFile.Name()))
		}
	}()
	if err := os.Chmod(tempFile.Name(), 0o600); err != nil {
		return err
	}
	if _, err = io.Copy(tempFile, r); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempFile.Name(), path); err != nil {
		// Leave the fully written temp file on disk so the caller can inspect it. The caller
		// is responsible for removing it.
		successful = true
		return &RenameError{TempPath: tempFile.Name(), Path: path, Err: err}
	}
	successful = true
	return nil
}

// A RenameError is returned by AtomicStore when the final rename of the temp file onto the
// destination path fails. The temp file is closed, fully written, and left on disk at TempPath.
// Callers can compare it with the file at Path (e.g. by size or content hash) to decide whether
// the failure is benign. Callers must remove TempPath when they are done with it.
type RenameError struct {
	TempPath string
	Path     string
	Err      error
}

// Error implements error.
func (e *RenameError) Error() string {
	return fmt.Sprintf("failed to rename %q to %q: %v", e.TempPath, e.Path, e.Err)
}

// Unwrap returns the underlying rename error.
func (e *RenameError) Unwrap() error {
	return e.Err
}

func (s *fileSystemStore) Store(hash string, r io.Reader) (err error) {
	path := s.pathToHashFile(hash)

	err = AtomicStore(path, r, hash)
	var renameErr *RenameError
	if !errors.As(err, &renameErr) {
		return err
	}
	// Artifacts are content addressed. On Windows, a rename can fail if another writer/reader is
	// using the destination. If the destination exists with the same size, assume it is the same.
	defer func() { utils.UncheckedError(os.Remove(renameErr.TempPath)) }()
	tempInfo, tempErr := os.Stat(renameErr.TempPath)
	destInfo, destErr := os.Stat(renameErr.Path)
	if tempErr == nil && destErr == nil && tempInfo.Size() == destInfo.Size() {
		return nil
	}
	return err
}

func (s *fileSystemStore) Close() error {
	return nil
}
