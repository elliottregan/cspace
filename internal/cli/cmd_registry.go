package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/spf13/cobra"
)

func newRegistryCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "registry",
		Short: "Inspect and prune the cspace sandbox registry",
		Long: `cspace up registers each sandbox in ~/.cspace/sandbox-registry.json with
its control URL, IP, token, and (when applicable) browser-sidecar name.

Stale entries accumulate when cspace down doesn't run cleanly (Ctrl-C
mid-teardown, host reboot, externally stopped containers). These subcommands
inspect the registry against live container state and clean up entries whose
containers are gone.`,
	}
	parent.AddCommand(newRegistryListCmd())
	parent.AddCommand(newRegistryPruneCmd())
	return parent
}

func newRegistryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registry entries with alive/dead status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryList(cmd.OutOrStdout())
		},
	}
}

func newRegistryPruneCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove registry entries whose containers no longer exist",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryPrune(cmd.OutOrStdout(), dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be removed without changing the registry")
	return cmd
}

// containerExists reports whether a container with the given name exists.
//
// Apple Container's `container inspect <name>` exits 0 even when the container
// is missing — it just prints `[]`. So we check both: non-zero exit OR empty
// JSON array means "dead". Any other shape (a populated array) means alive.
func containerExists(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "container", "inspect", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return false
	}
	// "[]" (with optional internal whitespace) means no matching containers.
	if bytes.Equal(trimmed, []byte("[]")) {
		return false
	}
	// Defensive: any JSON array starting with "[{" indicates at least one entry.
	return strings.HasPrefix(string(trimmed), "[{") || trimmed[0] == '{'
}

// containerNameForEntry constructs the canonical sandbox container name from
// the registry entry. Matches cspace up's containerName template.
func containerNameForEntry(e registry.Entry) string {
	return fmt.Sprintf("cspace-%s-%s", e.Project, e.Name)
}

func runRegistryList(out io.Writer) error {
	path, err := registry.DefaultPath()
	if err != nil {
		return err
	}
	r := &registry.Registry{Path: path}
	entries, err := r.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "no sandboxes registered")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROJECT\tSANDBOX\tIP\tBROWSER\tSTATE\tLIFECYCLE\tSTARTED")
	for _, e := range entries {
		alive := containerExists(ctx, containerNameForEntry(e))
		lifecycleState := "dead"
		if alive {
			lifecycleState = "alive"
		}
		browserCol := "—"
		if e.BrowserContainer != "" {
			if containerExists(ctx, e.BrowserContainer) {
				browserCol = "alive"
			} else {
				browserCol = "dead"
				if alive {
					lifecycleState = "alive (browser:dead)"
				}
			}
		}
		// Empty State on legacy entries (written before the field existed)
		// is treated as ready — those sandboxes were registered post-boot
		// under the old single-write flow.
		entryState := e.State
		if entryState == "" {
			entryState = "ready"
		}
		started := "—"
		if !e.StartedAt.IsZero() {
			started = e.StartedAt.Local().Format("2006-01-02 15:04")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Project, e.Name, ipOrDash(e.IP), browserCol, entryState, lifecycleState, started)
	}
	return tw.Flush()
}

func ipOrDash(ip string) string {
	if ip == "" {
		return "—"
	}
	return ip
}

func runRegistryPrune(out io.Writer, dryRun bool) error {
	path, err := registry.DefaultPath()
	if err != nil {
		return err
	}
	r := &registry.Registry{Path: path}
	entries, err := r.List()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "no sandboxes registered")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pruneCount := 0
	clearedBrowserCount := 0
	stuckBootingCount := 0
	for _, e := range entries {
		sandboxAlive := containerExists(ctx, containerNameForEntry(e))
		switch {
		case !sandboxAlive:
			// Dead container — remove the entry. This catches both
			// state=ready entries whose sandbox went away, and orphaned
			// state=starting entries from a boot that crashed.
			if dryRun {
				_, _ = fmt.Fprintf(out, "would remove: %s:%s\n", e.Project, e.Name)
			} else {
				if err := r.Unregister(e.Project, e.Name); err != nil {
					return fmt.Errorf("unregister %s:%s: %w", e.Project, e.Name, err)
				}
				_, _ = fmt.Fprintf(out, "removed: %s:%s\n", e.Project, e.Name)
			}
			pruneCount++
		case e.State == "starting":
			// Container alive but the entry never reached state=ready.
			// Boot might still be in progress, or cspace up may have
			// died after Run returned but before /health responded. Don't
			// auto-remove; report so the user can decide. Once the
			// container exits (or is stopped), a future prune will reap
			// the entry via the !sandboxAlive branch above.
			if dryRun {
				_, _ = fmt.Fprintf(out, "stuck booting: %s:%s (alive but never reached ready)\n", e.Project, e.Name)
			} else {
				_, _ = fmt.Fprintf(out, "warning: %s:%s is alive but state=starting (boot may still be in progress; not removing)\n", e.Project, e.Name)
			}
			stuckBootingCount++
		case e.BrowserContainer != "" && !containerExists(ctx, e.BrowserContainer):
			// Sandbox alive but browser dead — clear the browser_container field.
			if dryRun {
				_, _ = fmt.Fprintf(out, "would clear browser: %s:%s (was %s)\n", e.Project, e.Name, e.BrowserContainer)
			} else {
				e2 := e
				e2.BrowserContainer = ""
				if err := r.Register(e2); err != nil {
					return fmt.Errorf("clear browser %s:%s: %w", e.Project, e.Name, err)
				}
				_, _ = fmt.Fprintf(out, "cleared browser_container: %s:%s\n", e.Project, e.Name)
			}
			clearedBrowserCount++
		}
	}

	switch {
	case pruneCount == 0 && clearedBrowserCount == 0 && stuckBootingCount == 0:
		_, _ = fmt.Fprintln(out, "no dead entries to prune")
	case pruneCount == 0 && clearedBrowserCount == 0:
		// Only stuck-booting entries — nothing to prune, but the warnings
		// above already informed the user. Add a one-line summary.
		_, _ = fmt.Fprintf(out, "no dead entries to prune (%d stuck booting; not removed)\n", stuckBootingCount)
	case dryRun:
		switch {
		case clearedBrowserCount == 0:
			_, _ = fmt.Fprintf(out, "would prune %d entries\n", pruneCount)
		case pruneCount == 0:
			_, _ = fmt.Fprintf(out, "would clear browser_container on %d alive entries\n", clearedBrowserCount)
		default:
			_, _ = fmt.Fprintf(out, "would prune %d dead entries; would clear browser_container on %d alive entries\n",
				pruneCount, clearedBrowserCount)
		}
	default:
		switch {
		case clearedBrowserCount == 0:
			_, _ = fmt.Fprintf(out, "pruned %d dead entries\n", pruneCount)
		case pruneCount == 0:
			_, _ = fmt.Fprintf(out, "cleared browser_container on %d alive entries\n", clearedBrowserCount)
		default:
			_, _ = fmt.Fprintf(out, "pruned %d dead entries; cleared browser_container on %d alive entries\n",
				pruneCount, clearedBrowserCount)
		}
	}
	return nil
}
