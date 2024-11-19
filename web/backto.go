package web

import (
	"net/url"
	"strings"
)

var hostnameWhitelist = map[string]bool{
	"localhost":    true,
	"app.viam.dev": true,
	"app.viam.com": true,
}

func isWhitelisted(hostname string) bool {
	return hostnameWhitelist[hostname]
}

// IsValidBacktoURL returns true if the passed string is a secure URL to a whitelisted
// hostname. The whitelisted hostnames are: "localhost", "app.viam.dev", and "app.viam.com".
//
//   - https://example.com -> false
//   - http://app.viam.com/path/name -> false
//   - https://app.viam.com/path/name -> true
func IsValidBacktoURL(path string) bool {
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

	return false
}
