package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down <name>",
		Short: "Stop and remove a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 30*time.Second)
			defer cancel()

			// Look up the registry entry BEFORE we tear anything down so
			// we know whether there's a sidecar to stop alongside the
			// sandbox. A missing entry is fine — we still try to stop the
			// canonical sandbox container by name (be permissive: the
			// sandbox might not be in the registry but a stale container
			// could still be running).
			path, _ := registry.DefaultPath()
			r := &registry.Registry{Path: path}
			entry, _ := r.Lookup(project, name)

			a := applecontainer.New()
			_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

			// Stop+remove the sidecar AFTER the sandbox so the agent's
			// outstanding CDP connections drain naturally. stopBrowserSidecar
			// is idempotent; safe to call with an empty name.
			if entry.BrowserContainer != "" {
				stopBrowserSidecar(ctx, entry.BrowserContainer)
			}

			_ = r.Unregister(project, name)

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "sandbox %s down\n", name)
			return nil
		},
	}
}
