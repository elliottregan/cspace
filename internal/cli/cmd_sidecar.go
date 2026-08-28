package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/sandboxmode"
	"github.com/spf13/cobra"
)

func newSidecarCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "sidecar",
		Short: "Manage a project's compose sidecars",
		Long: `Recover the sidecars a project's compose file declares (a database,
a backend, a dashboard) without tearing down the sandbox next to them.

` + "`cspace sidecar restart <service>`" + ` works from the host and from
inside a sandbox, so an agent whose backend dies can bring it back
itself. Sandboxes address sidecars through daemon DNS, which
re-inspects the container per query, so the new address a restart
produces is invisible to callers.

Restart, not recreate: a compose sidecar's run spec lives in the
project's compose file, which only ` + "`cspace up`" + ` reads. A container
that has been removed outright needs ` + "`cspace down`" + ` + ` + "`cspace up`" + `.`,
	}
	parent.AddCommand(newSidecarRestartCmd())
	return parent
}

func newSidecarRestartCmd() *cobra.Command {
	var sandbox string
	cmd := &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart one compose sidecar (e.g. convex-backend)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service := args[0]
			if sandboxmode.IsInSandbox() {
				return runSidecarRestartInSandbox(cmd.Context(), cmd.OutOrStdout(), service)
			}
			return runSidecarRestartHost(cmd.Context(), cmd.OutOrStdout(), service, sandbox)
		},
	}
	cmd.Flags().StringVar(&sandbox, "sandbox", "",
		"which sandbox's sidecar to restart (required when the project has more than one running)")
	return cmd
}

// runSidecarRestartHost runs the ladder directly — the host owns the
// substrate, so there is no reason to round-trip through the daemon.
func runSidecarRestartHost(ctx context.Context, out io.Writer, service, sandboxFlag string) error {
	project := projectName()

	path, err := registry.DefaultPath()
	if err != nil {
		return err
	}
	entries, err := (&registry.Registry{Path: path}).List()
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	sandbox, err := resolveSidecarSandbox(project, sandboxFlag, entries)
	if err != nil {
		return err
	}

	ip, err := restartSidecarFn(ctx, project, sandbox, service)
	if err != nil {
		return err
	}
	printSidecarRestarted(out, project, sandbox, service, ip)
	return nil
}

// runSidecarRestartInSandbox asks the host daemon to do it, authenticating
// with the token from this sandbox's own registry entry — the same scheme
// `cspace browser restart` uses in-sandbox.
func runSidecarRestartInSandbox(ctx context.Context, out io.Writer, service string) error {
	project := sandboxmode.Project()
	sandbox := sandboxmode.Name()
	registryURL := sandboxmode.RegistryURL()
	if registryURL == "" {
		return fmt.Errorf("CSPACE_REGISTRY_URL not set; cannot reach the host daemon from a sandbox")
	}
	if project == "" || sandbox == "" {
		return fmt.Errorf("CSPACE_PROJECT / CSPACE_SANDBOX_NAME not set; cannot determine which sidecar to restart")
	}

	entry, err := resolveEntry(project, sandbox)
	if err != nil {
		return fmt.Errorf("look up sandbox registry entry: %w", err)
	}

	url := strings.TrimRight(registryURL, "/") + "/sidecar/restart/" + project + "/" + sandbox + "/" + service
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if entry.Token != "" {
		req.Header.Set("Authorization", "Bearer "+entry.Token)
	}

	client := &http.Client{Timeout: browserRestartClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("restart failed: %s", restartErrorText(body))
	}

	var result struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode restart response: %w", err)
	}
	printSidecarRestarted(out, project, sandbox, service, result.IP)
	return nil
}

// resolveSidecarSandbox decides which sandbox's sidecar to act on. Sidecar
// containers are per-sandbox (cspace-<project>-<sandbox>-<service>), so a
// project running two sandboxes has two of each service and the caller has to
// say which — guessing would restart someone else's.
func resolveSidecarSandbox(project, sandboxFlag string, entries []registry.Entry) (string, error) {
	if sandboxFlag != "" {
		return sandboxFlag, nil
	}
	var names []string
	for _, e := range entries {
		if e.Project == project {
			names = append(names, e.Name)
		}
	}
	sort.Strings(names)
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no running sandbox for project %q; start one with `cspace up`", project)
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("project %q has %d sandboxes running (%s); name one with --sandbox",
			project, len(names), strings.Join(names, ", "))
	}
}

// printSidecarRestarted reports the stable name rather than leading with the
// address: the name is what callers use, and it is the thing that does not
// change on the next restart.
func printSidecarRestarted(out io.Writer, project, sandbox, service, ip string) {
	_, _ = fmt.Fprintf(out, "restarted %s\n", projectSidecarName(project, sandbox, service))
	_, _ = fmt.Fprintf(out, "  reachable at %s.%s.%s.cspace.test (now %s)\n", service, sandbox, project, ip)
}
