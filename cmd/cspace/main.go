// Command cspace is the main entry point for the cspace CLI.
//
// Build with version injection:
//
//	go build -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=v0.2.0" ./cmd/cspace
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// A child's exit status is not a cspace error: exit with it silently.
		var exitErr cli.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
