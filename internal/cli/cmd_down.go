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
	var keepState bool
	cmd := &cobra.Command{
		Use:   "down [<name>]",
		Short: "Stop and remove a sandbox (or all sandboxes with --all)",
		Long: `Stop and remove a sandbox.

By default, ` + "`cspace down`" + ` is destructive: it stops the container,
wipes the per-sandbox clone at ~/.cspace/clones/<project>/<name>/,
wipes sessions at ~/.cspace/sessions/<project>/<name>/, and removes
every substrate-managed volume named cspace-<project>-<name>-*. The
next ` + "`cspace up <name>`" + ` therefore starts from a fresh clone of host
HEAD with empty volumes.

Pass --keep-state to preserve clone, sessions, and volumes — useful
when you want to suspend a sandbox and resume the same name later
without losing in-progress state. Note that an existing clone is NOT
auto-pulled on the next ` + "`up`" + `; if you keep state, you keep that
exact tree.

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
				teardownSandbox(ctx, a, r, project, name, cmd.OutOrStdout(), keepState)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false,
		"tear down every sandbox in the current project")
	cmd.Flags().BoolVar(&keepState, "keep-state", false,
		"preserve the workspace clone, sessions, and per-sandbox volumes (default: wipe)")
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
// if registered, and unregisters the entry. When wipeState is true (the
// `cspace down` default), it also reclaims the per-sandbox clone,
// sessions, and substrate-managed volumes so the next `cspace up <same
// name>` starts fresh. Permissive on missing entries — a stale container
// could still be running, so we always issue Stop by name regardless of
// registry state.
func teardownSandbox(
	ctx context.Context,
	a *applecontainer.Adapter,
	r *registry.Registry,
	project, name string,
	out io.Writer,
	wipeState bool,
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

	if wipeState {
		wipeSandboxState(ctx, a, project, name, out)
	}

	_, _ = fmt.Fprintf(out, "sandbox %s down\n", name)
}

// wipeSandboxState reclaims everything `cspace up` materialized for a
// sandbox: substrate-managed volumes, the workspace clone, and the
// host-side sessions tree. Each step is best-effort; the caller already
// reported `down` and we don't want a leaked session dir to mask the
// successful container stop.
func wipeSandboxState(
	ctx context.Context,
	a *applecontainer.Adapter,
	project, name string,
	out io.Writer,
) {
	// Substrate-managed volumes: cspace-<project>-<name>-<compose-volume>.
	// Listing first (instead of trying every compose-declared name) means
	// we also catch volumes from a previous cspace up that referenced a
	// compose service we no longer have, and orphans from interrupted runs.
	prefix := fmt.Sprintf("cspace-%s-%s-", project, name)
	if vols, err := a.ListVolumes(ctx, prefix); err == nil {
		for _, v := range vols {
			if err := a.RemoveVolume(ctx, v); err != nil {
				_, _ = fmt.Fprintf(out, "[cspace] warning: remove volume %s: %v\n", v, err)
			}
		}
	} else {
		_, _ = fmt.Fprintf(out, "[cspace] warning: list volumes: %v\n", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: resolve home dir for state wipe: %v\n", err)
		return
	}

	clonePath := filepath.Join(home, ".cspace", "clones", project, name)
	if err := os.RemoveAll(clonePath); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: remove clone %s: %v\n", clonePath, err)
	}

	sessionsPath := filepath.Join(home, ".cspace", "sessions", project, name)
	if err := os.RemoveAll(sessionsPath); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: remove sessions %s: %v\n", sessionsPath, err)
	}
}
