package tools

import (
	"go.viam.com/utils/artifact"
)

// Status inspects the root and returns a git like status of what is to
// be added.
func Status() (*artifact.Status, error) {
	cache, err := artifact.GlobalCache()
	if err != nil {
		return nil, err
	}

	return cache.Status()
}
