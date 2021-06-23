package tools

import (
	"go.viam.com/utils/artifact"
)

// Push pushes any artifacts not present in global cache tree
// to the underlying store of the cache.
func Push() error {
	cache, err := artifact.GlobalCache()
	if err != nil {
		return err
	}

	return cache.WriteThroughUser()
}
