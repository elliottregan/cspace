//go:build tools

package tools

import (
	_ "github.com/evilmartians/lefthook"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "golang.org/x/tools/cmd/goimports"
)
