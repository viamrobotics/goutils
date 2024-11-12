package web

import (
	"net/url"
	"strings"
)

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
