package artifact

import (
	"fmt"
	"io"

	"github.com/pkg/errors"
)

// A Store is responsible for loading and storing artifacts by their
// hashes and content.
type Store interface {
	Contains(hash string) error
	Load(hash string) (io.ReadCloser, error)
	Store(hash string, r io.Reader) error
	Close() error
}

// A StoreType identifies a specific type of Store.
type StoreType string

// The set of known store types.
const (
	StoreTypeFileSystem    = StoreType("fs")
	StoreTypeGoogleStorage = StoreType("google_storage")
)

// NewStore returns a new store based on the given config. It errors
// if making the store fails or the underlying type has no constructor.
func NewStore(config StoreConfig) (Store, error) {
	switch v := config.(type) {
	case *FileSystemStoreConfig:
		return newFileSystemStore(v)
	case *GoogleStorageStoreConfig:
		return newGoogleStorageStore(v)
	default:
		return nil, errors.Errorf("unknown store type %q", config.Type())
	}
}

// NewArtifactNotFoundHashError returns an error for when an artifact
// is not found by its hash.
func NewArtifactNotFoundHashError(hash string) error {
	return &NotFoundError{hash: &hash}
}

// NewArtifactNotFoundPathError returns an error for when an artifact
// is not found by its path.
func NewArtifactNotFoundPathError(path string) error {
	return &NotFoundError{path: &path}
}

// IsNotFoundError returns if the given error is any kind of
// artifact not found error.
func IsNotFoundError(err error) bool {
	var errArt *NotFoundError
	return errors.As(err, &errArt)
}

// An NotFoundError is used when an artifact can not be found.
type NotFoundError struct {
	path *string
	hash *string
}

// Error returns an error specific to the way the artifact was searched for.
func (e *NotFoundError) Error() string {
	if e.path != nil {
		return fmt.Sprintf("artifact not found; path=%q", *e.path)
	}
	return fmt.Sprintf("artifact not found; hash=%q", *e.hash)
}

// A ConflictError is used when a write operation fails due to concurrent modification.
// This implements optimistic locking - the resource was modified since it was last read.
type ConflictError struct {
	path            string
	expectedVersion int64
	actualVersion   int64
}

// NewConflictError creates a new conflict error.
func NewConflictError(path string, expectedVersion, actualVersion int64) error {
	return &ConflictError{
		path:            path,
		expectedVersion: expectedVersion,
		actualVersion:   actualVersion,
	}
}

// Error returns a descriptive error message about the conflict.
func (e *ConflictError) Error() string {
	if e.actualVersion == -1 {
		return fmt.Sprintf(
			"conflict detected: %q is being modified by another process. "+
				"Please reload and retry the operation",
			e.path,
		)
	}
	return fmt.Sprintf(
		"conflict detected: %q was modified concurrently (expected version %d, found version %d). "+
			"Please reload and retry the operation",
		e.path, e.expectedVersion, e.actualVersion,
	)
}

// IsConflictError returns true if the error is a conflict error.
func IsConflictError(err error) bool {
	var errConflict *ConflictError
	return errors.As(err, &errConflict)
}
