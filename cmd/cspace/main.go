// Command cspace is the main entry point for the cspace CLI.
//
// Build with version injection:
//
//	go build -ldflags "-X github.com/elliottregan/cspace/internal/cli.Version=v0.2.0" ./cmd/cspace
package main

import (
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
