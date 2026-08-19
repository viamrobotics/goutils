//go:build tools
// +build tools

package tools

import (
	_ "github.com/edaniels/golinters/cmd/combined"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
)
