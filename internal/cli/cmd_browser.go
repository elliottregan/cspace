package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/sandboxmode"
	"github.com/spf13/cobra"
)

// newBrowserCmd groups the shared browser sidecar's operator commands.
// Works both on the host (talks to the restart ladder / substrate directly)
// and in-sandbox (talks back to the host daemon over CSPACE_REGISTRY_URL) —
// see runBrowserRestartHost/runBrowserRestartInSandbox and
// newBrowserStatusCmd's RunE for the split.
func newBrowserCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "browser",
		Short: "Manage the project's shared browser sidecar",
	}
	parent.AddCommand(newBrowserRestartCmd())
	parent.AddCommand(newBrowserStatusCmd())
	return parent
}

// browserRestartClientTimeout bounds the in-sandbox HTTP client's wait for
// POST /browser/restart/<project> to complete. Set comfortably above the
// daemon handler's own browserRestartTimeout (150s, cmd_daemon.go) so the
// client never gives up before the server's own bound would.
const browserRestartClientTimeout = browserRestartTimeout + 15*time.Second

func newBrowserRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the project's shared browser sidecar",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}
			out := cmd.OutOrStdout()

			if sandboxmode.IsInSandbox() {
				return runBrowserRestartInSandbox(ctx, out)
			}
			return runBrowserRestartHost(ctx, out)
		},
	}
}

// runBrowserRestartHost drives the restart ladder in-process via
// restartBrowserFn — the same seam the daemon's HTTP handler uses — so the
// host CLI doesn't need a daemon round-trip for its own machine's sidecar.
func runBrowserRestartHost(ctx context.Context, out io.Writer) error {
	project, err := browserHostProject()
	if err != nil {
		return err
	}
	bs, err := restartBrowserFn(ctx, project, "")
	if err != nil {
		return fmt.Errorf("restart browser sidecar: %w", err)
	}
	printBrowserEndpoints(out, project, bs.IP, bs.CDPURL, bs.RunServerWSURL)
	return nil
}

// runBrowserRestartInSandbox restarts the project's shared sidecar from
// inside a sandbox by POSTing to the host daemon. Auth mirrors
// cmd_send.go's resolveEntry: the sandbox looks up its own registry entry
// (GET /lookup/<project>/<sandbox>) to fetch the Bearer token the daemon's
// browserRestartAuthorized checks against a same-project entry.
func runBrowserRestartInSandbox(ctx context.Context, out io.Writer) error {
	project := sandboxmode.Project()
	sandbox := sandboxmode.Name()
	registryURL := sandboxmode.RegistryURL()
	if registryURL == "" {
		return fmt.Errorf("CSPACE_REGISTRY_URL not set; cannot reach the host daemon from a sandbox")
	}
	if project == "" {
		return fmt.Errorf("CSPACE_PROJECT not set; cannot determine project in sandbox mode")
	}

	entry, err := resolveEntry(project, sandbox)
	if err != nil {
		return fmt.Errorf("look up sandbox registry entry: %w", err)
	}

	url := strings.TrimRight(registryURL, "/") + "/browser/restart/" + project
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
		return fmt.Errorf("restart failed: status %d: %s", resp.StatusCode, restartErrorText(body))
	}

	var result struct {
		IP             string `json:"ip"`
		CDPURL         string `json:"cdpUrl"`
		RunServerWSURL string `json:"runServerWsUrl"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode restart response: %w", err)
	}
	printBrowserEndpoints(out, project, result.IP, result.CDPURL, result.RunServerWSURL)
	return nil
}

// restartErrorText extracts the meaningful error text from a non-2xx
// /browser/restart/<project> response body. browserRestartHandler's failure
// shape (cmd_daemon.go) is JSON {"ok":false,"error":"..."}; when body parses
// as that shape, return just the error field so the CLI doesn't dump a raw
// JSON envelope at the user. Anything else (e.g. a plain-text http.Error
// body from the auth/bad-request paths) falls back to the trimmed raw body.
func restartErrorText(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(body))
}

// printBrowserEndpoints reports a successful restart's refreshed endpoints
// in a fixed, human-readable shape shared by both the host and in-sandbox
// restart paths.
func printBrowserEndpoints(out io.Writer, project, ip, cdpURL, runServerWSURL string) {
	_, _ = fmt.Fprintf(out, "browser sidecar restarted for project %q\n", project)
	_, _ = fmt.Fprintf(out, "  ip:            %s\n", ip)
	_, _ = fmt.Fprintf(out, "  cdp:           %s\n", cdpURL)
	_, _ = fmt.Fprintf(out, "  run-server ws: %s\n", runServerWSURL)
}

// browserHostProject resolves the project name for host-side browser
// commands. Unlike projectName() (cmd_up.go), which falls back to "default"
// for convenience, restart/status target a specific project's shared
// sidecar — guessing wrong would restart or probe the wrong container, so an
// unresolvable project must be a clear error rather than a silent default.
func browserHostProject() (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("no cspace project config loaded (run from a project with .cspace.json, or inside a cspace sandbox)")
	}
	if cfg.Project.Name == "" {
		return "", fmt.Errorf("project name not set in .cspace.json")
	}
	return cfg.Project.Name, nil
}

// browserStatusProbeBound bounds each of the two status probes (CDP,
// run-server WS). Short and fixed — status is a liveness check, not a
// startup wait — per the brief's "e.g. 5s".
const browserStatusProbeBound = 5 * time.Second

func newBrowserStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Probe the project's shared browser sidecar endpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = cmd.Root().Context()
			}

			cdpURL, wsAddr, err := browserStatusTargets(ctx, sandboxmode.IsInSandbox())
			if err != nil {
				return err
			}

			results := probeBrowserEndpoints(ctx, cdpURL, wsAddr, browserStatusProbeBound)
			allOK := printBrowserStatus(cmd.OutOrStdout(), results)
			if !allOK {
				return fmt.Errorf("browser sidecar status check failed")
			}
			return nil
		},
	}
}

// browserStatusTargets resolves the CDP URL and run-server WS address to
// probe, for either context. In-sandbox, both endpoints hang off the DNS
// name browser.<project>.<suffix> — the sandbox's own resolver routes that
// through the host daemon (see daemonDNSHandler's "browser" 2-label case).
// On the host, the sidecar's IP comes from inspecting the container
// directly via waitForBrowserIP (which itself goes through the
// browserExecCmd seam).
func browserStatusTargets(ctx context.Context, inSandbox bool) (cdpURL, wsAddr string, err error) {
	if inSandbox {
		project := sandboxmode.Project()
		if project == "" {
			return "", "", fmt.Errorf("CSPACE_PROJECT not set; cannot determine project in sandbox mode")
		}
		host := browserSandboxHost(project)
		return fmt.Sprintf("http://%s:%d", host, browserCDPPort),
			fmt.Sprintf("%s:%d", host, browserRunServerPort), nil
	}

	project, err := browserHostProject()
	if err != nil {
		return "", "", err
	}
	name := browserSingletonName(project)
	ip, err := waitForBrowserIP(ctx, name, browserStatusProbeBound)
	if err != nil {
		return "", "", fmt.Errorf("resolve browser sidecar IP for %s: %w", name, err)
	}
	return fmt.Sprintf("http://%s:%d", ip, browserCDPPort),
		fmt.Sprintf("%s:%d", ip, browserRunServerPort), nil
}

// browserSandboxHost is the DNS name the shared browser sidecar answers to
// from inside a sandbox: browser.<project>.<suffix>, where suffix is
// daemonDNSDomain (cmd_daemon.go) with its canonical trailing dot stripped
// (a bare hostname lookup, unlike the wire-format FQDN daemonDNSDomain
// represents, doesn't carry one).
func browserSandboxHost(project string) string {
	return "browser." + project + "." + strings.TrimSuffix(daemonDNSDomain, ".")
}

// browserEndpointStatus is one line of `cspace browser status` output: a
// human label plus the outcome of probing it.
type browserEndpointStatus struct {
	label string
	err   error
}

// probeBrowserEndpoints probes the CDP endpoint (cdpURL, a full
// http://host:port base) and the run-server WS endpoint (wsAddr, a
// host:port TCP address), each bounded by bound. Extracted as a helper
// (rather than inlined at the RunE call site) so tests can inject fixture
// addresses instead of hardcoding DNS names in the probe call site.
func probeBrowserEndpoints(ctx context.Context, cdpURL, wsAddr string, bound time.Duration) []browserEndpointStatus {
	return []browserEndpointStatus{
		{label: fmt.Sprintf("CDP :%d", browserCDPPort), err: waitForCDP(ctx, cdpURL, bound)},
		{label: fmt.Sprintf("run-server :%d", browserRunServerPort), err: waitForRunServerWS(ctx, wsAddr, bound)},
	}
}

// printBrowserStatus writes one "<label> ok" / "<label> FAIL (<detail>)"
// line per result and reports whether every probe succeeded.
func printBrowserStatus(out io.Writer, results []browserEndpointStatus) bool {
	allOK := true
	for _, r := range results {
		if r.err == nil {
			_, _ = fmt.Fprintf(out, "%s ok\n", r.label)
			continue
		}
		allOK = false
		_, _ = fmt.Fprintf(out, "%s FAIL (%s)\n", r.label, r.err)
	}
	return allOK
}
