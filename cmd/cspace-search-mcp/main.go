// Package main implements cspace-search-mcp, a Model Context Protocol server
// that exposes semantic search tools (search_code, list_clusters) over stdio.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elliottregan/cspace/search/config"
	searchmcp "github.com/elliottregan/cspace/search/mcp"

	mcpSDK "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	root := projectRoot()
	cfg, err := config.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := mcpSDK.NewServer(&mcpSDK.Implementation{Name: "cspace-search", Version: "0.1.0"}, nil)
	(&searchmcp.Server{ProjectRoot: root, Config: cfg}).Register(server)
	if err := server.Run(context.Background(), &mcpSDK.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func projectRoot() string {
	cwd, _ := os.Getwd()
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}
