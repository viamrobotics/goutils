// +build tools

// Package tools defines helper build time tooling needed by the codebase.
package tools

import (
	_ "github.com/edaniels/golinters/cmd/combined"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/polyfloyd/go-errorlint"
	_ "golang.org/x/tools/cmd/goimports"
)
