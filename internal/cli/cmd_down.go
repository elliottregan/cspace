package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "down [<name>]",
		Short: "Stop and remove a sandbox (or all sandboxes with --all)",
		Long: `Stop and remove a sandbox.

With --all (or -a), tear down every sandbox in the current project.
Without it, exactly one <name> argument is required.`,
		Args: func(_ *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return fmt.Errorf("--all does not take a sandbox name")
			}
			if !all && len(args) != 1 {
				return fmt.Errorf("requires exactly one sandbox name (or use --all)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			project := projectName()

			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, 60*time.Second)
			defer cancel()

			path, _ := registry.DefaultPath()
			r := &registry.Registry{Path: path}

			var names []string
			if all {
				entries, err := r.List()
				if err != nil {
					return fmt.Errorf("registry list: %w", err)
				}
				for _, e := range entries {
					if e.Project == project {
						names = append(names, e.Name)
					}
				}
				if len(names) == 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no sandboxes registered for this project")
					return nil
				}
			} else {
				names = []string{args[0]}
			}

			a := applecontainer.New()
			for _, name := range names {
				teardownSandbox(ctx, a, r, project, name, cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false,
		"tear down every sandbox in the current project")
	return cmd
}

// teardownSandbox stops the canonical container, stops a browser sidecar
// if registered, and unregisters the entry. Permissive on missing entries
// — a stale container could still be running, so we always issue Stop by
// name regardless of registry state.
func teardownSandbox(
	ctx context.Context,
	a *applecontainer.Adapter,
	r *registry.Registry,
	project, name string,
	out io.Writer,
) {
	entry, _ := r.Lookup(project, name)

	_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

	// Stop the sidecar AFTER the sandbox so the agent's outstanding
	// CDP connections drain naturally. stopBrowserSidecar is idempotent.
	if entry.BrowserContainer != "" {
		stopBrowserSidecar(ctx, entry.BrowserContainer)
	}

	_ = r.Unregister(project, name)
	_, _ = fmt.Fprintf(out, "sandbox %s down\n", name)
}
