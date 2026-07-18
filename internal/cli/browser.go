package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// browserExecCmd is the package seam through which the restart ladder issues
// every substrate (`container ...`), `pgrep`, and `kill` invocation — the same
// function-var pattern as validateGitHubToken — so tests script each outcome
// without touching real containers or host processes. Default: run the named
// binary and return ONLY its stdout (stderr noise on an otherwise-successful
// call must never corrupt the stdout-JSON parsers — waitForBrowserIP,
// sidecarVersion, containerStateRunning — that read this seam's return
// value). On failure, the stderr text is folded into the returned error so
// the ladder's error-string matching (e.g. the "not running" split-brain
// signature) still sees it.
var browserExecCmd = func(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(exitErr.Stderr))
		}
		return string(out), err
	}
	return string(out), nil
}

// verifyBrowserFn probes a freshly (re)started sidecar for real liveness. The
// default composes waitForBrowserIP → waitForCDP → waitForRunServerWS within
// the caller's deadline, mutating bs with the freshly-inspected IP and URLs.
// Seamed (like browserExecCmd) so the restart-ladder tests stay hermetic; the
// default gets its own focused test.
var verifyBrowserFn = func(ctx context.Context, bs *BrowserSidecar) error {
	ip, err := waitForBrowserIP(ctx, bs.ContainerName, remainingBudget(ctx, 30*time.Second))
	if err != nil {
		return fmt.Errorf("browser sidecar IP: %w", err)
	}
	bs.IP = ip
	bs.CDPURL = fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
	bs.RunServerWSURL = fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
	if err := waitForCDP(ctx, bs.CDPURL, remainingBudget(ctx, 90*time.Second)); err != nil {
		return fmt.Errorf("browser sidecar CDP: %w", err)
	}
	runServerAddr := fmt.Sprintf("%s:%d", ip, browserRunServerPort)
	if err := waitForRunServerWS(ctx, runServerAddr, remainingBudget(ctx, 30*time.Second)); err != nil {
		return fmt.Errorf("browser sidecar run-server WS: %w", err)
	}
	return nil
}

// Restart-ladder timing (spec §3). The overall budget is generous because a
// recreate hits a fresh apt-get install; a plain stop/start reuses the
// installed filesystem and is fast.
const (
	browserRestartBudget        = 120 * time.Second
	browserSplitBrainPollBudget = 10 * time.Second
	browserRestartPollInterval  = 500 * time.Millisecond
)

// remainingBudget returns the smaller of (a) how long is left on ctx's
// deadline and (b) fallback, so a generous outer budget (e.g. the restart
// ladder's 120s) can't let one probe's slice balloon into the whole thing.
// When ctx carries no deadline, returns fallback. Never negative.
func remainingBudget(ctx context.Context, fallback time.Duration) time.Duration {
	if d, ok := ctx.Deadline(); ok {
		if r := time.Until(d); r > 0 {
			return min(r, fallback)
		}
		return 0
	}
	return fallback
}

// containerStateRunning inspects the named container through browserExecCmd and
// reports whether it is currently running and whether it exists at all. Apple
// Container's `container inspect` exits 0 with body "[]" for a missing
// container, so both an empty/"[]" body and an inspect error map to
// exists=false. A populated record's top-level "status" field (value "running"
// / "stopped") drives the running bool.
func containerStateRunning(ctx context.Context, name string) (running, exists bool) {
	out, err := browserExecCmd(ctx, "container", "inspect", name)
	if err != nil {
		return false, false
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "[]" {
		return false, false
	}
	var records []struct {
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(trimmed), &records) != nil || len(records) == 0 {
		return false, false
	}
	return strings.EqualFold(records[0].Status, "running"), true
}

// resolveBrowserSplitBrain recovers a sidecar whose substrate state has
// diverged from reality — the 2026-07-17 incident, where `container kill`
// reported "not running" while the state check still said running. Recovery
// (observed to be the only thing that reconciled the substrate): SIGKILL the
// host-side `container-runtime-linux` process whose argv carries the container
// name, then poll until the substrate agrees the container is no longer
// running. All process/substrate calls route through browserExecCmd.
func resolveBrowserSplitBrain(ctx context.Context, name string) error {
	// name is embedded in a pgrep -f (regex) pattern: quote it so a literal
	// "." in a project name (e.g. "my.project") can't act as a regex
	// wildcard and cause kill -9 to match a sibling project's
	// container-runtime-linux process.
	pattern := "container-runtime-linux.*" + regexp.QuoteMeta(name)
	if out, err := browserExecCmd(ctx, "pgrep", "-f", pattern); err == nil {
		for _, pid := range strings.Fields(out) {
			_, _ = browserExecCmd(ctx, "kill", "-9", pid)
		}
	}
	deadline := time.Now().Add(browserSplitBrainPollBudget)
	for {
		if running, _ := containerStateRunning(ctx, name); !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("container %s still running after host-process teardown", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(browserRestartPollInterval):
		}
	}
}

// restartBrowserSidecar restarts the project's shared browser sidecar via the
// escalation ladder in spec §3: read the recorded Playwright version, stop the
// container, escalate to SIGKILL then host-process teardown if it won't die,
// start it (or recreate it if it's gone), and verify real liveness — all
// within a bounded budget. Consumed by the daemon endpoint and the `cspace
// browser restart` CLI.
func restartBrowserSidecar(ctx context.Context, project, plVersion string) (*BrowserSidecar, error) {
	name := browserSingletonName(project)

	ctx, cancel := context.WithTimeout(ctx, browserRestartBudget)
	defer cancel()

	// 1. Pin to the running sidecar's recorded version (best-effort) so a
	//    recreate reconstructs the same labels; else the caller's, else default.
	if v := sidecarVersion(ctx, name); v != "" {
		plVersion = v
	} else if plVersion == "" {
		plVersion = defaultPlaywrightVersion
	}

	// 2. Bounded stop. Best-effort: a missing or already-stopped container just
	//    errors here; the state check below decides what to do.
	_, _ = browserExecCmd(ctx, "container", "stop", "-t", "5", name)

	running, exists := containerStateRunning(ctx, name)
	switch {
	case !exists:
		// 3a. Gone entirely (an agent `rm`'d / "shut it down"). Recreate; the
		//     `run -d` argv both creates and starts it, so no separate start.
		args := browserSidecarRunArgs(name, plVersion)
		if out, err := browserExecCmd(ctx, "container", args...); err != nil {
			return nil, fmt.Errorf("recreate browser sidecar %s: %w (%s)", name, err, strings.TrimSpace(out))
		}
	default:
		// 3b. Still present. If the stop didn't take, escalate.
		if running {
			killOut, killErr := browserExecCmd(ctx, "container", "kill", "--signal", "SIGKILL", name)
			running, _ = containerStateRunning(ctx, name)
			if running {
				// Split-brain signature (documented): kill errored with text
				// containing "not running" while the state check still reports
				// running. We escalate to host-process teardown whenever the
				// container survives SIGKILL — a superset that also covers a
				// generic wedged-still-running container — but track the
				// canonical signature for the returned error's context.
				killText := strings.ToLower(killOut)
				if killErr != nil {
					killText += " " + strings.ToLower(killErr.Error())
				}
				splitBrain := killErr != nil && strings.Contains(killText, "not running")
				if err := resolveBrowserSplitBrain(ctx, name); err != nil {
					if splitBrain {
						return nil, fmt.Errorf("split-brain recovery failed for %s: %w", name, err)
					}
					return nil, fmt.Errorf("browser sidecar %s still running after SIGKILL: %w", name, err)
				}
			}
		}
		// 3c. Container is stopped now (either it already was, or we killed it).
		if out, err := browserExecCmd(ctx, "container", "start", name); err != nil {
			return nil, fmt.Errorf("start browser sidecar %s: %w (%s)", name, err, strings.TrimSpace(out))
		}
	}

	// 4. Verify real liveness within the remaining budget.
	bs := &BrowserSidecar{ContainerName: name}
	if err := verifyBrowserFn(ctx, bs); err != nil {
		return nil, fmt.Errorf("verify restarted browser sidecar %s: %w", name, err)
	}
	return bs, nil
}

// browserImage returns the Microsoft Playwright Docker image for a given
// Playwright version. Tag and run-server MUST be the same release because
// Playwright pins to a specific chromium build ID per release; a v1.58.2
// run-server inside a v1.59 image looks for chromium-1208 and finds
// chromium-1213, refusing to launch. Microsoft publishes a major.minor.patch-
// noble tag for every Playwright npm release.
func browserImage(version string) string {
	return fmt.Sprintf("mcr.microsoft.com/playwright:v%s-noble", version)
}

// defaultPlaywrightVersion is the fallback used when the project has no
// @playwright/test in package.json (or there's no package.json at all).
// Bump in lockstep with the agent's @playwright/mcp install — the
// supervisor's claude-runner.ts registers playwright-mcp/chrome-devtools-mcp
// regardless, and a CDP-protocol mismatch between those clients and a
// far-newer chromium would surface here too.
const defaultPlaywrightVersion = "1.59.0"

// browserRunServerPort is where the sidecar's `playwright run-server`
// listens. Project tests connect via PW_TEST_CONNECT_WS_ENDPOINT.
const browserRunServerPort = 3000

// browserCDPPort is where the sidecar's headless Chromium exposes
// DevTools Protocol. The agent's playwright-mcp / chrome-devtools-mcp
// connect via CSPACE_BROWSER_CDP_URL.
const browserCDPPort = 9222

// browserContainerName returns the canonical sidecar name for a sandbox,
// in lockstep with cspace up's containerName template plus a "-browser"
// suffix.
func browserContainerName(project, sandbox string) string {
	return fmt.Sprintf("cspace-%s-%s-browser", project, sandbox)
}

// browserSingletonName is the per-PROJECT shared browser sidecar container
// name (Phase 2). One per project, shared by all that project's sandboxes.
func browserSingletonName(project string) string {
	return fmt.Sprintf("cspace-%s-browser", project)
}

// workspaceFriendlyHost is the per-instance hostname the shared browser uses
// to reach a sandbox's workspace: <sandbox>.<project>.cspace.test, resolved by
// the cspace DNS daemon to the instance's vmnet IP. Both labels are lowercased
// to match the daemon's lowercased, case-sensitive registry comparison.
func workspaceFriendlyHost(name, project string) string {
	return strings.ToLower(name) + "." + strings.ToLower(project) + ".cspace.test"
}

// applyWorkspaceHostEnv sets CSPACE_WORKSPACE_HOST on env. Callers must
// invoke this unconditionally (not gated behind browser-sidecar setup) —
// agents/docs point at this var as THE address to reach the workspace from
// outside it, so it must be present even when the browser sidecar is
// disabled (--no-browser or devcontainer.json opt-out).
func applyWorkspaceHostEnv(env map[string]string, name, project string) {
	env["CSPACE_WORKSPACE_HOST"] = workspaceFriendlyHost(name, project)
}

// startBrowserSidecar runs the Playwright sidecar container, waits for its
// IP and CDP endpoint to come up, and returns the container name and CDP
// URL. Idempotent: if a container of the same name already exists, it is
// stopped and removed first.
//
// Modern Chromium ignores --remote-debugging-address=0.0.0.0 and force-binds
// to 127.0.0.1, so we run Chrome on :9223 internally and use socat to forward
// 0.0.0.0:9222 -> 127.0.0.1:9223. Same workaround as
// scripts/spikes/2026-05-01-browser-sidecar.sh and
// lib/templates/docker-compose.shared.yml (legacy compose).
//
// The sidecar gets --dns 1.1.1.1 --dns 8.8.8.8 because Apple Container's
// default DNS is broken, and the sidecar is NOT a cspace container so it
// doesn't go through our substrate adapter's DNS injection. Without these,
// `apt-get update` inside the sidecar hangs.
// BrowserSidecar describes a started browser sidecar's reachable endpoints.
type BrowserSidecar struct {
	ContainerName  string // substrate container name
	IP             string // vmnet IP (used for /etc/hosts inject in workspace)
	CDPURL         string // http://<ip>:9222 — for $CSPACE_BROWSER_CDP_URL
	RunServerWSURL string // ws://<ip>:3000/ — for $PW_TEST_CONNECT_WS_ENDPOINT
}

// Resource caps for the browser sidecar. The sidecar bypasses the
// substrate adapter (raw `container run` below), so without explicit
// flags it runs on Apple Container's default 1024 MiB — which OOM-wedged
// under shared e2e load: one long-lived CDP chromium plus a fresh browser
// per run-server connection, shared by every sandbox in the project.
// (cs-finding 2026-07-17-browser-sidecar-runs-on-default-1gib-and-ooms-under-e2e-load)
const (
	browserSidecarCPUs      = 4
	browserSidecarMemoryMiB = 4096
)

// browserSidecarRunArgs builds the full `container run` argv for the
// sidecar. Pure so tests can assert on the invocation shape.
func browserSidecarRunArgs(containerName, plVersion string) []string {
	return []string{
		"run", "-d",
		"--name", containerName,
		"--label", "cspace.playwright-version=" + plVersion,
		"--cpus", fmt.Sprintf("%d", browserSidecarCPUs),
		"--memory", fmt.Sprintf("%dMiB", browserSidecarMemoryMiB),
		// Public resolvers needed for the apt-get install step. Once
		// dnsmasq is up we repoint resolv.conf at it; until then,
		// these are how the sidecar fetches packages.
		"--dns", "1.1.1.1",
		"--dns", "8.8.8.8",
		browserImage(plVersion),
		"bash", "-c",
		"set -e; " +
			// 1) apt-get socat (CDP forwarder) AND dnsmasq (so the
			//    headless browser resolves *.cspace.test the same
			//    way the cspace sandbox does — chrome can navigate
			//    to friendly URLs from playwright-mcp).
			"apt-get update -qq && apt-get install -y -qq socat dnsmasq >/dev/null 2>&1; " +
			// 2) Configure dnsmasq forwarder. Forward .cspace.test
			//    to the cspace daemon on the gateway, fall through to
			//    public resolvers for everything else.
			"cat > /etc/dnsmasq.d/cspace.conf <<'CFG'\n" +
			"listen-address=127.0.0.1\n" +
			"port=53\n" +
			"no-resolv\n" +
			"no-hosts\n" +
			"bind-interfaces\n" +
			"server=/cspace.test/192.168.64.1#5354\n" +
			"server=1.1.1.1\n" +
			"server=8.8.8.8\n" +
			"CFG\n" +
			"dnsmasq --conf-file=/etc/dnsmasq.d/cspace.conf; " +
			// 3) Repoint glibc resolver at dnsmasq.
			"echo 'nameserver 127.0.0.1' > /etc/resolv.conf; " +
			// 4) Start chrome on loopback (chrome ignores
			//    --remote-debugging-address=0.0.0.0 in modern builds).
			//
			//    --disable-dev-shm-usage routes Chrome's shared-memory
			//    backing for URLLoader buffers to /tmp instead of /dev/shm.
			//    Apple Container's default tmpfs caps /dev/shm at 64 MiB,
			//    which Chrome's network service exhausts on the very first
			//    paint of a moderately complex page (multiple JS chunks,
			//    CSS chunks, Sentry envelope POSTs). The renderer then
			//    rejects subresource requests with net::ERR_INSUFFICIENT_
			//    RESOURCES even though the server is healthy and curl from
			//    the same network returns 200. Documented Playwright/
			//    Puppeteer recommendation for containerized Chrome — see
			//    cs-finding 2026-05-06-browser-sidecar-chromium-hits-err-
			//    insufficient-resources-on for the diagnostic trail.
			"/ms-playwright/chromium-*/chrome-linux/chrome " +
			"--headless=new --no-sandbox --disable-gpu " +
			"--disable-dev-shm-usage " +
			"--remote-debugging-port=9223 about:blank & " +
			// 5) Wait for chrome's CDP to be ready.
			"until curl -sf http://127.0.0.1:9223/json/version >/dev/null 2>&1; do sleep 0.5; done; " +
			// 6) Forward CDP loopback → external for siblings.
			fmt.Sprintf("socat TCP-LISTEN:%d,fork,reuseaddr TCP:127.0.0.1:9223 & ", browserCDPPort) +
			// 7) Start Playwright run-server in the foreground for
			//    project tests using PW_TEST_CONNECT_WS_ENDPOINT.
			//    `npx playwright` resolves to the version baked into
			//    the image, which we keep in lockstep with the agent's
			//    @playwright/mcp pin (see browserImage).
			fmt.Sprintf("exec npx -y playwright@%s run-server --port %d --host 0.0.0.0",
				plVersion, browserRunServerPort),
	}
}

// runBrowserSidecar starts a sidecar container with the given name. It does
// NOT remove a pre-existing same-named container (callers decide that). The
// cspace.playwright-version label lets the shared path detect a version-mismatched
// singleton on reuse.
func runBrowserSidecar(ctx context.Context, containerName, plVersion string) (*BrowserSidecar, error) {
	if plVersion == "" {
		plVersion = defaultPlaywrightVersion
	}
	args := browserSidecarRunArgs(containerName, plVersion)
	cmd := exec.CommandContext(ctx, "container", args...)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return nil, fmt.Errorf("start browser sidecar: %w (%s)", runErr, strings.TrimSpace(string(out)))
	}

	// Capture sidecar IP.
	ip, err := waitForBrowserIP(ctx, containerName, 30*time.Second)
	if err != nil {
		stopBrowserSidecar(context.Background(), containerName)
		return nil, fmt.Errorf("browser sidecar IP: %w", err)
	}

	cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
	wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)

	// Wait for CDP endpoint to actually respond. apt-get update +
	// install socat against fresh apt indices can run 30–60s in the
	// playwright image; chrome itself starts in a couple of seconds.
	// 90s gives generous headroom without making the failure case
	// (no network / wrong image) wait too long.
	if err := waitForCDP(ctx, cdpURL, 90*time.Second); err != nil {
		stopBrowserSidecar(context.Background(), containerName)
		return nil, fmt.Errorf("browser sidecar CDP: %w", err)
	}

	return &BrowserSidecar{
		ContainerName:  containerName,
		IP:             ip,
		CDPURL:         cdpURL,
		RunServerWSURL: wsURL,
	}, nil
}

// startBrowserSidecar is the per-instance path (opt-out / --no-shared-browser):
// it torches any prior same-named container then runs fresh. Unchanged signature.
func startBrowserSidecar(ctx context.Context, project, sandbox, plVersion string) (*BrowserSidecar, error) {
	containerName := browserContainerName(project, sandbox)
	// Idempotency: torch any prior container with the same name.
	_ = exec.CommandContext(ctx, "container", "stop", containerName).Run()
	_ = exec.CommandContext(ctx, "container", "rm", containerName).Run()
	return runBrowserSidecar(ctx, containerName, plVersion)
}

// InjectWorkspaceHost writes a /etc/hosts entry inside the browser sidecar
// mapping `workspace` → workspaceIP, so headless Chromium can resolve
// http://workspace:<port> URLs back to the sandbox. Convenience wrapper
// around InjectHosts for this single-alias case.
func InjectWorkspaceHost(ctx context.Context, sidecarName, workspaceIP string) error {
	if sidecarName == "" || workspaceIP == "" {
		return nil
	}
	return InjectHosts(ctx, sidecarName, map[string]string{"workspace": workspaceIP})
}

// InjectHosts writes a cspace-managed block to a sidecar microVM's
// /etc/hosts mapping each hostname → IP. Replaces any prior cspace-injected
// block (idempotent). Currently used only to inject the `workspace` alias
// (see InjectWorkspaceHost); other sandbox-internal hostnames are resolved
// via daemon DNS rather than /etc/hosts.
//
// Best-effort: a single bash invocation, errors return up so callers can
// log a warning and continue.
func InjectHosts(ctx context.Context, sidecarName string, hosts map[string]string) error {
	if sidecarName == "" || len(hosts) == 0 {
		return nil
	}
	// Sort by hostname for deterministic output (eases diffing across runs).
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	var block strings.Builder
	block.WriteString("# BEGIN cspace-injected\\n")
	for _, name := range names {
		fmt.Fprintf(&block, "%s %s\\n", hosts[name], name)
	}
	block.WriteString("# END cspace-injected\\n")
	clean := exec.CommandContext(ctx, "container", "exec", "--user", "0", sidecarName,
		"sh", "-c",
		"sed -i '/^# BEGIN cspace-injected$/,/^# END cspace-injected$/d' /etc/hosts")
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("clean hosts in %s: %w (%s)", sidecarName, err, strings.TrimSpace(string(out)))
	}
	add := exec.CommandContext(ctx, "container", "exec", "--user", "0", sidecarName,
		"sh", "-c",
		fmt.Sprintf("printf '%s' >> /etc/hosts", block.String()))
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("inject hosts in %s: %w (%s)", sidecarName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitForBrowserIP polls `container inspect <name>` until it returns a
// non-empty IPv4 address or the deadline passes.
func waitForBrowserIP(ctx context.Context, name string, max time.Duration) (string, error) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		out, err := browserExecCmd(ctx, "container", "inspect", name)
		if err == nil {
			var records []struct {
				Networks []struct {
					IPv4Address string `json:"ipv4Address"`
				} `json:"networks"`
			}
			if json.Unmarshal([]byte(out), &records) == nil && len(records) > 0 {
				for _, n := range records[0].Networks {
					if n.IPv4Address != "" {
						if i := strings.IndexByte(n.IPv4Address, '/'); i >= 0 {
							return n.IPv4Address[:i], nil
						}
						return n.IPv4Address, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("timeout waiting for IP")
}

// waitForCDP polls the sidecar's /json/version until it returns 200.
func waitForCDP(ctx context.Context, cdpURL string, max time.Duration) error {
	deadline := time.Now().Add(max)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", cdpURL+"/json/version", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for CDP at %s", cdpURL)
}

// waitForRunServerWS polls the sidecar's run-server with a real WebSocket
// upgrade handshake until it completes. A TCP-connect (or plain HTTP GET)
// check is not enough: a wedged guest can still ACK new connections while
// its userspace is dead, so only a completed 101 Switching Protocols
// response proves the run-server is actually alive
// (cs-finding 2026-07-17-tcp-connect-probes-pass-wedged-services).
func waitForRunServerWS(ctx context.Context, addr string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if err := wsHandshakeOnce(addr, 3*time.Second); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for run-server WS handshake at %s", addr)
}

// wsHandshakeOnce performs a single WebSocket-upgrade handshake attempt
// against addr, succeeding only if the server replies with HTTP/1.1 101.
func wsHandshakeOnce(addr string, timeout time.Duration) error {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(timeout))
	// RFC 6455 §4.1 requires Sec-WebSocket-Key to be the base64 encoding of
	// exactly 16 raw bytes (22 base64 chars + "=="). "cspace-probe-16b" is
	// exactly 16 ASCII bytes. Node's `ws` library — which Playwright's
	// run-server uses — validates the header against
	// ^[+/0-9A-Za-z]{22}==$ and returns HTTP 400 for anything else,
	// including the 17-byte/24-char value this probe previously sent,
	// which made a healthy run-server look dead to waitForRunServerWS.
	key := base64.StdEncoding.EncodeToString([]byte("cspace-probe-16b"))
	req := "GET / HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		return err
	}
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 101") {
		return fmt.Errorf("unexpected handshake response: %q", string(buf[:n]))
	}
	return nil
}

// stopBrowserSidecar stops + removes the sidecar container. Idempotent: any
// errors from `container stop` / `container rm` are swallowed so callers
// can use this on cleanup paths without secondary failure handling.
func stopBrowserSidecar(ctx context.Context, name string) {
	if name == "" {
		return
	}
	_ = exec.CommandContext(ctx, "container", "stop", name).Run()
	_ = exec.CommandContext(ctx, "container", "rm", name).Run()
}

// ensureSharedBrowserSidecar returns the project's shared browser sidecar,
// starting it if absent and REUSING it if a healthy, version-matched one is
// already running. Never torches a healthy singleton. The bool is startedNew:
// true iff this call created the container (the caller uses it to gate
// error-path teardown so a reused singleton is never stopped).
func ensureSharedBrowserSidecar(ctx context.Context, project, plVersion string) (*BrowserSidecar, bool, error) {
	if plVersion == "" {
		plVersion = defaultPlaywrightVersion
	}
	name := browserSingletonName(project)

	if containerExists(ctx, name) {
		// Healthy + version-matched? Reuse without torching.
		ip, ipErr := waitForBrowserIP(ctx, name, 5*time.Second)
		if ipErr == nil {
			cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
			runServerAddr := fmt.Sprintf("%s:%d", ip, browserRunServerPort)
			if waitForCDP(ctx, cdpURL, 10*time.Second) == nil && sidecarVersion(ctx, name) == plVersion &&
				waitForRunServerWS(ctx, runServerAddr, 10*time.Second) == nil {
				wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
				return &BrowserSidecar{ContainerName: name, IP: ip, CDPURL: cdpURL, RunServerWSURL: wsURL}, false, nil
			}
		}
		// Exists but unhealthy / version-mismatched: torch then restart.
		stopBrowserSidecar(ctx, name)
	}
	bs, err := runBrowserSidecar(ctx, name, plVersion)
	if err != nil {
		// Concurrency: a sibling `up` may have created it between our check and
		// run ("already exists"). Re-probe and reuse if it's now healthy.
		if containerExists(ctx, name) {
			if ip, ipErr := waitForBrowserIP(ctx, name, 5*time.Second); ipErr == nil {
				cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
				runServerAddr := fmt.Sprintf("%s:%d", ip, browserRunServerPort)
				if waitForCDP(ctx, cdpURL, 10*time.Second) == nil &&
					waitForRunServerWS(ctx, runServerAddr, 10*time.Second) == nil {
					wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
					return &BrowserSidecar{ContainerName: name, IP: ip, CDPURL: cdpURL, RunServerWSURL: wsURL}, false, nil
				}
			}
		}
		return nil, false, err
	}
	return bs, true, nil
}

// sidecarVersion reads the cspace.playwright-version recorded on the running
// sidecar (the --label from runBrowserSidecar). Returns "" if it can't be
// read, which forces a conservative restart on reuse.
func sidecarVersion(ctx context.Context, name string) string {
	out, err := browserExecCmd(ctx, "container", "inspect", name)
	if err != nil {
		return ""
	}
	// cspace.playwright-version appears once, as "<key>":"<value>" or
	// "key=value" in the labels/env block. Extract the value after the marker.
	s := out
	const marker = "cspace.playwright-version"
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i+len(marker):]
	// skip non-version chars (": =\") then read until the next quote/space/comma
	rest = strings.TrimLeft(rest, "\":= ")
	end := strings.IndexAny(rest, "\", \n}")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// detectPlaywrightVersion reads the project's @playwright/test pin from
// <projectRoot>/package.json. Returns the literal version string with
// any semver-range marker stripped (^1.58.2 → 1.58.2). Returns empty
// when the file or dependency is absent — caller substitutes
// defaultPlaywrightVersion. Never blocks; never errors loudly.
//
// Background: Playwright's run-server enforces same-version equality
// between the @playwright/test client and the server (returns 428
// Precondition Required across a minor version boundary). Letting the
// project's pin drive the sidecar's version is the only way to support
// projects on different Playwright releases without a manual override.
func detectPlaywrightVersion(projectRoot string) string {
	raw, err := os.ReadFile(filepath.Join(projectRoot, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return ""
	}
	for _, deps := range []map[string]string{pkg.DevDependencies, pkg.Dependencies} {
		v, ok := deps["@playwright/test"]
		if !ok {
			continue
		}
		// Strip semver range markers (^, ~, >=, =, v) and surrounding
		// whitespace. Anything remaining beyond the first space (e.g.
		// "^1.58.2 || 1.59") gets cut at the first valid version.
		v = strings.TrimSpace(v)
		v = strings.TrimLeft(v, "^~>=v ")
		if i := strings.IndexAny(v, " ,|"); i >= 0 {
			v = v[:i]
		}
		return v
	}
	return ""
}
