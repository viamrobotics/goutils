package web

import (
	"net/url"
	"strings"
)

var hostnameWhitelist = map[string]bool{
	"localhost": true,
	"viam.dev":  true,
	"viam.com":  true,
}

func isWhitelisted(hostname string) bool {
	return hostnameWhitelist[hostname]
}

// IsLocalRedirectPath returns true if the passed string is a secure URL to a whitelisted
// hostname or a valid pathname for the local server. The whitelisted hostnames are:
// "localhost", "viam.dev", and "viam.com".
//
//   - https://example.com -> false
//   - http://viam.com/path/name -> false
//   - https://viam.com/path/name -> true
//   - /local/path/name -> true
func IsLocalRedirectPath(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	url, err := url.ParseRequestURI(normalized)
	if err != nil {
		// ignore invalid URLs/URL components
		return false
	}

	if url.Scheme != "" && url.Scheme != "https" {
		// ignore non-secure URLs
		return false
	}

	if isWhitelisted(url.Hostname()) {
		// ignore non-whitelisted hosts
		return true
	}

	// allow local app paths
	candidate := url.String()
	return strings.HasPrefix(candidate, "/") && !strings.HasPrefix(candidate, "//")
}
