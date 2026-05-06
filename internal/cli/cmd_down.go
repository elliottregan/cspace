package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/orchestrator"
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

// substrateDowner is a minimal substrate adapter for orchestrator.Down that only stops containers.
type substrateDowner struct {
	adapter *applecontainer.Adapter
}

func (s *substrateDowner) Run(ctx context.Context, spec orchestrator.ServiceSpec) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *substrateDowner) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *substrateDowner) Stop(ctx context.Context, name string) error {
	return s.adapter.Stop(ctx, name)
}

func (s *substrateDowner) IP(ctx context.Context, name string) (string, error) {
	return "", fmt.Errorf("not implemented")
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

	// Tear down devcontainer-defined sidecars (e.g., database, cache) before
	// stopping the main sandbox. Non-fatal; print a warning but continue.
	if cfg != nil && cfg.ProjectRoot != "" {
		dcPath := filepath.Join(cfg.ProjectRoot, ".devcontainer", "devcontainer.json")
		if _, err := os.Stat(dcPath); err == nil {
			if c, err := devcontainer.Load(dcPath); err == nil {
				if plan, err := devcontainer.Merge(c, filepath.Dir(dcPath)); err == nil {
					orch := &orchestrator.Orchestration{
						Sandbox:   name,
						Project:   project,
						Plan:      plan,
						Substrate: &substrateDowner{adapter: a},
					}
					if err := orch.Down(ctx); err != nil {
						_, _ = fmt.Fprintf(out, "[cspace] warning: sidecar teardown: %v\n", err)
					}
				}
			}
		}
	}

	_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

	// Stop the sidecar AFTER the sandbox so the agent's outstanding
	// CDP connections drain naturally. stopBrowserSidecar is idempotent.
	if entry.BrowserContainer != "" {
		stopBrowserSidecar(ctx, entry.BrowserContainer)
	}

	_ = r.Unregister(project, name)
	_, _ = fmt.Fprintf(out, "sandbox %s down\n", name)
}
