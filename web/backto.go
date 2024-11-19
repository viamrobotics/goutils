package web

import (
	"net/url"
	"regexp"
	"strings"
)

var hostnameWhitelist = map[string]bool{
	"localhost":    true,
	"app.viam.dev": true,
	"app.viam.com": true,
}

var prTempEnvPattern = "pr-(\\d+)-appmain-bplesliplq-uc.a.run.app"

// isWhitelisted returns true if the passed hostname is whitelisted or a temporary PR environment
func isWhitelisted(hostname string) bool {
	isPRTempEnv, err := regexp.MatchString(prTempEnvPattern, hostname)
	if err != nil {
		return false
	}

	if isPRTempEnv {
		return true
	}

	return hostnameWhitelist[hostname]
}

// isAllowedURLScheme returns true if the passed URL is using a "https" schema, or "http" for "localhost" URLs
func isAllowedURLScheme(url *url.URL) bool {
	if url.Scheme == "https" {
		return true
	}

	if url.Hostname() == "localhost" && url.Scheme == "http" {
		return true
	}

	return false
}

// IsValidBacktoURL returns true if the passed string is a secure URL to a whitelisted
// hostname. The whitelisted hostnames are: "localhost", "app.viam.dev", and "app.viam.com".
//
//   - https://example.com -> false
//   - http://app.viam.com/path/name -> false
//   - https://app.viam.com/path/name -> true
//   - http://localhost/path/name -> true
func IsValidBacktoURL(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	url, err := url.Parse(normalized)
	if err != nil {
		// ignore invalid URLs/URL components
		return false
	}

	if !isAllowedURLScheme(url) {
		// ignore non-secure URLs
		return false
	}

	if isWhitelisted(url.Hostname()) {
		// ignore non-whitelisted hosts
		return true
	}

	return false
}
