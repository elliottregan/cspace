package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// browserImage pins the Playwright base used as the sidecar image. Microsoft
// publishes Linux/arm64 builds; the apt-get-install-socat hack inside the
// run command means we don't need a custom image yet. P2/P3 may build a
// leaner image; for now this is fine.
//
// Pinning matches the agent's @playwright/mcp install in the cspace base
// image; bump both together when upgrading.
const browserImage = "mcr.microsoft.com/playwright:v1.58.2-noble"

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

func startBrowserSidecar(ctx context.Context, project, sandbox string) (*BrowserSidecar, error) {
	containerName := browserContainerName(project, sandbox)

	// Idempotency: torch any prior container with the same name.
	_ = exec.CommandContext(ctx, "container", "stop", containerName).Run()
	_ = exec.CommandContext(ctx, "container", "rm", containerName).Run()

	args := []string{
		"run", "-d",
		"--name", containerName,
		// Public resolvers needed for the apt-get install step. Once
		// dnsmasq is up we repoint resolv.conf at it; until then,
		// these are how the sidecar fetches packages.
		"--dns", "1.1.1.1",
		"--dns", "8.8.8.8",
		browserImage,
		"bash", "-c",
		"set -e; " +
			// 1) apt-get socat (CDP forwarder) AND dnsmasq (so the
			//    headless browser resolves *.cspace2.local the same
			//    way the cspace sandbox does — chrome can navigate
			//    to friendly URLs from playwright-mcp).
			"apt-get update -qq && apt-get install -y -qq socat dnsmasq >/dev/null 2>&1; " +
			// 2) Configure dnsmasq forwarder. Forward .cspace2.local
			//    to the cspace daemon on the gateway, fall through to
			//    public resolvers for everything else.
			"cat > /etc/dnsmasq.d/cspace.conf <<'CFG'\n" +
			"listen-address=127.0.0.1\n" +
			"port=53\n" +
			"no-resolv\n" +
			"no-hosts\n" +
			"bind-interfaces\n" +
			"server=/cspace2.local/192.168.64.1#5354\n" +
			"server=1.1.1.1\n" +
			"server=8.8.8.8\n" +
			"CFG\n" +
			"dnsmasq --conf-file=/etc/dnsmasq.d/cspace.conf; " +
			// 3) Repoint glibc resolver at dnsmasq.
			"echo 'nameserver 127.0.0.1' > /etc/resolv.conf; " +
			// 4) Start chrome on loopback (chrome ignores
			//    --remote-debugging-address=0.0.0.0 in modern builds).
			"/ms-playwright/chromium-*/chrome-linux/chrome " +
			"--headless=new --no-sandbox --disable-gpu " +
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
			fmt.Sprintf("exec npx -y playwright run-server --port %d --host 0.0.0.0", browserRunServerPort),
	}
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

// InjectWorkspaceHost writes a /etc/hosts entry inside the browser sidecar
// mapping `workspace` → workspaceIP. Project test code can then use
// http://workspace:<port> as BASE_URL when the dev/preview server is in
// the workspace sandbox itself. Best-effort; failures log and continue.
func InjectWorkspaceHost(ctx context.Context, sidecarName, workspaceIP string) error {
	if sidecarName == "" || workspaceIP == "" {
		return nil
	}
	// Strip any prior cspace-injected block, then append. Idempotent.
	clean := exec.CommandContext(ctx, "container", "exec", "--user", "0", sidecarName,
		"sh", "-c",
		"sed -i '/^# BEGIN cspace-injected$/,/^# END cspace-injected$/d' /etc/hosts")
	if out, err := clean.CombinedOutput(); err != nil {
		return fmt.Errorf("clean hosts in %s: %w (%s)", sidecarName, err, strings.TrimSpace(string(out)))
	}
	add := exec.CommandContext(ctx, "container", "exec", "--user", "0", sidecarName,
		"sh", "-c",
		fmt.Sprintf("printf '# BEGIN cspace-injected\\n%s workspace\\n# END cspace-injected\\n' >> /etc/hosts", workspaceIP))
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
		out, err := exec.CommandContext(ctx, "container", "inspect", name).Output()
		if err == nil {
			var records []struct {
				Networks []struct {
					IPv4Address string `json:"ipv4Address"`
				} `json:"networks"`
			}
			if json.Unmarshal(out, &records) == nil && len(records) > 0 {
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
