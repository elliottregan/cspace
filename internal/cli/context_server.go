package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/elliottregan/cspace/internal/contextstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newContextServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context-server",
		Short: "Run the docs/context MCP server over stdio",
		Long: `Expose docs/context/ (direction, principles, roadmap, decisions, discoveries)
as an MCP server over stdio. Typically invoked by Claude Code via .mcp.json
or the container's Claude MCP config, not by humans directly.`,
		GroupID: "other",
		RunE:    runContextServer,
	}
	cmd.Flags().String("root", "", "Project root (default: current working directory)")
	return cmd
}

func runContextServer(cmd *cobra.Command, args []string) error {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		root = cwd
	}

	store := &contextstore.Store{Root: root}
	server := mcp.NewServer(&mcp.Implementation{Name: "cspace-context", Version: Version}, nil)
	registerContextTools(server, store)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

// registerContextTools is split out so tests can register against an in-memory server.
// Task 7 will fill in the tool registrations.
func registerContextTools(server *mcp.Server, store *contextstore.Store) {
	_ = server
	_ = store
}
