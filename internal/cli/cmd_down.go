package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/sidecars"
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

With --all (or -a), tear down every sandbox in the current project —
except the ones a previous --keep-state left behind. Those have no
container to stop, and purging the state they were kept for is not
something a batch command should do on its own: each is named in the
output, and ` + "`cspace down <name>`" + ` purges one deliberately.
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
				mine := 0
				for _, e := range entries {
					if e.Project != project {
						continue
					}
					mine++
					// A kept entry has nothing to tear down: --keep-state
					// already stopped and removed its container. What it
					// still has is the clone, the sessions and the volumes
					// the operator asked cspace to hold, which the default
					// (non --keep-state) teardown below would wipe on its
					// way past. Skipping is the only reading of --all that
					// cannot silently destroy state someone asked to keep;
					// naming each one keeps the skip visible.
					if e.State == registry.StateStopped {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(),
							"kept: %s (stopped; run `cspace down %s` to purge it)\n", e.Name, e.Name)
						continue
					}
					names = append(names, e.Name)
				}
				if mine == 0 {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no sandboxes registered for this project")
					return nil
				}
			} else {
				names = []string{args[0]}
			}

			// Every name here is about to be joined into two host paths that
			// wipeSandboxState removes outright. Registry-derived names are
			// no excuse to skip the check: the registry is a file on disk,
			// and this is the last point before the joins. `project` is the
			// cwd-derived one, used only for the "browser" message's wording
			// — the shape check itself does not depend on it, and the
			// per-name project resolution happens further down.
			//
			// Under --all a bad name is skipped rather than fatal. These
			// names come from registry entries, which were never shape-
			// checked before this change, so one legacy entry with a dot in
			// it would otherwise make `cspace down --all` refuse to tear
			// down anything at all. The single-name form still fails hard:
			// there the name came from the caller, and the callers are no
			// longer only cspace's own code.
			kept := names[:0]
			for _, name := range names {
				if err := validateSandboxName(project, name); err != nil {
					if !all {
						return err
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"[cspace] skipping %q: %v\n"+
							"[cspace]   run `cspace registry prune` once its container is gone to drop the entry, "+
							"and delete ~/.cspace/clones/%[3]s/%[1]s/ and ~/.cspace/sessions/%[3]s/%[1]s/ by hand\n",
						name, err, project)
					continue
				}
				kept = append(kept, name)
			}
			names = kept

			if all && len(names) == 0 {
				// Every entry this project had was filtered out — kept by
				// a previous --keep-state, or skipped for its shape. Both
				// said so above, on their own streams; without this the
				// command would tear nothing down and say nothing about it.
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "nothing to tear down")
				return nil
			}

			a := applecontainer.New()
			for _, name := range names {
				// Resolve the sandbox's actual project. The cwd-derived
				// `project` is the right answer when the user is running
				// `cspace down` from inside the project they spun the
				// sandbox up from, but it silently sends teardown at the
				// wrong project namespace when the user is anywhere else
				// (or when `cspace down --all` is invoked from a script).
				// Fall back to a registry-wide lookup by name: if exactly
				// one sandbox in the registry matches, use that project.
				targetProject := project
				if !all && r != nil {
					if _, err := r.Lookup(project, name); err != nil {
						entries, _ := r.List()
						var matches []registry.Entry
						for _, e := range entries {
							if e.Name == name {
								matches = append(matches, e)
							}
						}
						switch len(matches) {
						case 1:
							targetProject = matches[0].Project
							_, _ = fmt.Fprintf(cmd.OutOrStdout(),
								"resolving %s to project %s (from registry)\n",
								name, targetProject)
						case 0:
							// Nothing in the registry — proceed with
							// cwd-derived project; teardown is best-effort
							// against possibly-stale containers anyway.
						default:
							names := make([]string, 0, len(matches))
							for _, m := range matches {
								names = append(names, m.Project)
							}
							return fmt.Errorf(
								"sandbox %q exists in multiple projects (%s); cd into one or use a unique name",
								name, strings.Join(names, ", "))
						}
					}
				}
				teardownSandbox(ctx, a, r, targetProject, name, cmd.OutOrStdout(), !keepState)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false,
		"tear down every sandbox in the current project (entries kept by --keep-state are skipped)")
	cmd.Flags().BoolVar(&keepState, "keep-state", false,
		"preserve the workspace clone, sessions, and per-sandbox volumes (default: wipe)")
	return cmd
}

// downSubstrate is the substrate surface cspace down uses: the container
// teardown, and the two volume calls behind the default (non-`--keep-state`)
// wipe. *applecontainer.Adapter satisfies it as written, so neither
// production caller — cmd_down.go's RunE and control_host.go — changes.
//
// It exists so a unit test can inject a no-op. `teardownSandbox` is
// best-effort and has no error return, which made it easy to call from a
// test with a real adapter; that test would then run
// `container rm --force cspace-<project>-<sandbox>` against the developer's
// own machine and be harmless only by luck of the names.
type downSubstrate interface {
	Stop(ctx context.Context, name string) error
	ListVolumes(ctx context.Context, prefix string) ([]string, error)
	RemoveVolume(ctx context.Context, name string) error
}

// stopSidecarContainer is stopBrowserSidecar behind a variable, for the same
// reason: it runs `container stop` and `container rm` by name and is a
// package function rather than an adapter method, so an interface cannot
// reach it. Only teardownSandbox goes through the variable; every other
// caller of stopBrowserSidecar is unchanged.
var stopSidecarContainer = stopBrowserSidecar

// substrateDowner is a minimal substrate adapter for sidecars.Down that only stops containers.
type substrateDowner struct {
	adapter downSubstrate
}

func (s *substrateDowner) Run(ctx context.Context, spec sidecars.ServiceSpec) (string, error) {
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

// teardownSandbox stops the canonical container, stops the per-instance
// browser sidecar (a no-op in the shared-singleton case), unregisters
// the entry, then ref-counts the shared browser singleton — stopping it
// only when this was the project's last sandbox. When wipeState is true
// (the `cspace down` default), it also reclaims the per-sandbox clone,
// sessions, and substrate-managed volumes so the next `cspace up <same
// name>` starts fresh. Permissive on missing entries — a stale container
// could still be running, so we always issue Stop by name regardless of
// registry state.
func teardownSandbox(
	ctx context.Context,
	a downSubstrate,
	r *registry.Registry,
	project, name string,
	out io.Writer,
	wipeState bool,
) {
	// Tear down devcontainer-defined sidecars (e.g., database, cache) before
	// stopping the main sandbox. Non-fatal; print a warning but continue.
	//
	// Prefer the per-sandbox clone's devcontainer.json over the cwd's: when
	// `cspace down` runs from anywhere other than the original project dir
	// (or from a script with no relevant cwd), cfg.ProjectRoot points at
	// the wrong tree and the sidecar list is empty — leaking convex /
	// db / cache containers from the previous run. The clone at
	// ~/.cspace/clones/<project>/<sandbox>/ is what was bind-mounted as
	// /workspace and is the authoritative source for what was started.
	dcPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		clonePath := filepath.Join(home, ".cspace", "clones", project, name)
		candidate := filepath.Join(clonePath, ".devcontainer", "devcontainer.json")
		if _, err := os.Stat(candidate); err == nil {
			dcPath = candidate
		}
	}
	if dcPath == "" && cfg != nil && cfg.ProjectRoot != "" {
		candidate := filepath.Join(cfg.ProjectRoot, ".devcontainer", "devcontainer.json")
		if _, err := os.Stat(candidate); err == nil {
			dcPath = candidate
		}
	}
	if dcPath != "" {
		if c, err := devcontainer.Load(dcPath); err == nil {
			if plan, err := devcontainer.Merge(c, filepath.Dir(dcPath)); err == nil {
				orch := &sidecars.Orchestration{
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

	_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

	// Attach bookkeeping: the lock file and any client records this
	// sandbox's attaches ever wrote. Unlike sessions/clone/volumes this is
	// not gated by wipeState — once the container is stopped there is no
	// tmux server left for a record to describe, and the lock exists only
	// to serialize concurrent attaches while the sandbox is up. A sandbox
	// that never had an attach has no such directory, which is fine.
	removeControlPlaneDir(out, project, name)

	// Per-instance (opt-out / --no-shared-browser) sidecar: stop this sandbox's
	// own browser. Idempotent and a no-op in the shared case (no such container).
	stopSidecarContainer(ctx, browserContainerName(project, name))

	// The registry entry goes only when the state does. --keep-state promises
	// a sandbox that can be resumed under the same name, and a sandbox the
	// registry has forgotten is one the dashboard cannot show — let alone
	// offer its boot key on, which is only offered on stopped rows. Marking
	// it stopped rather than leaving it alone matters too: Correlate reads a
	// StateStarting entry as booting, so a sandbox torn down mid-boot would
	// otherwise keep its ◐ forever.
	// (cs-finding:2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard)
	if wipeState {
		_ = r.Unregister(project, name)
	} else {
		_ = r.MarkStopped(project, name)
	}

	// Shared browser sidecar: ref-counted — stop it only when the project has
	// no sandbox left that could use it. The count is now by STATE rather
	// than by identity, because a kept entry holds no claim on the browser:
	// its container is gone. That is just as true of the siblings an earlier
	// `cspace down --all --keep-state` marked stopped, which is why
	// discounting only this one is not enough — CountForProject counts every
	// entry regardless of state (its own comment says so), so with three
	// sandboxes and --all --keep-state the tally never reaches zero and the
	// sidecar is left running for a project with nothing left to use it.
	// Idempotent and a no-op when no singleton exists.
	if entries, err := r.List(); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: registry list during browser teardown: %v\n", err)
	} else {
		live := 0
		for _, e := range entries {
			if e.Project == project && e.State != registry.StateStopped {
				live++
			}
		}
		if live == 0 {
			stopSidecarContainer(ctx, browserSingletonName(project))
		}
	}

	if wipeState {
		wipeSandboxState(ctx, a, project, name, out)
	}

	_, _ = fmt.Fprintf(out, "sandbox %s down\n", name)
}

// removeControlPlaneDir deletes a sandbox's attach bookkeeping —
// ~/.cspace/controlplane/<project>/<name>/, the attach lock and any client
// records — regardless of --keep-state: once the container this function's
// caller just stopped is gone, there is no tmux server left for a record to
// describe and the lock has nothing left to serialize. Best-effort like the
// rest of teardown: os.RemoveAll already treats a directory that never
// existed as success, so a sandbox that was never attached to produces no
// warning here.
func removeControlPlaneDir(out io.Writer, project, name string) {
	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: resolve home dir for control-plane cleanup: %v\n", err)
		return
	}
	dir := control.ControlPlaneDir(home, project, name)
	if err := os.RemoveAll(dir); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: remove control-plane dir %s: %v\n", dir, err)
	}
}

// wipeSandboxState reclaims everything `cspace up` materialized for a
// sandbox: substrate-managed volumes, the workspace clone, and the
// host-side sessions tree. Each step is best-effort; the caller already
// reported `down` and we don't want a leaked session dir to mask the
// successful container stop.
func wipeSandboxState(
	ctx context.Context,
	a downSubstrate,
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
