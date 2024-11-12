package web

import (
	"net/url"
	"strings"
)

// IsLocalRedirectPath returns true if the passed string is a valid local pathname for the local server:
//   - https://example.com -> false
//   - /local/path/name -> true
func IsLocalRedirectPath(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	url, err := url.Parse(normalized)
	if err != nil {
		return false
	}

	if url.IsAbs() {
		return false
	}

	candidate := url.String()
	return strings.HasPrefix(candidate, "/") && !strings.HasPrefix(candidate, "//")
}
