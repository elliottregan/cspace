# Browser Sidecar: Stable Addressing + Agent-Invocable Restart — Design

**Date:** 2026-07-17
**Findings driving this:** `2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path`, `2026-07-17-tcp-connect-probes-pass-wedged-services` (context: `2026-07-17-browser-sidecar-runs-on-default-1gib-and-ooms-under-e2e-load`, resolved)

## Problem

Sandboxes reach the shared browser sidecar via raw IPs baked into env at boot (`CSPACE_BROWSER_CDP_URL`, `PLAYWRIGHT_MCP_CDP_ENDPOINT`, `PW_TEST_CONNECT_WS_ENDPOINT`). A sidecar restart moves the IP, stranding every running sandbox with no repair path short of host-side surgery (observed 2026-07-17: NAT stopgap after an OOM wedge). Agents inside sandboxes have no way to restart a failed sidecar at all. Health gating uses probes that a listening-but-wedged service passes.

## Design

### 1. Stable name: `browser.<project>.cspace.test`

- `daemonDNSHandler` (internal/cli/cmd_daemon.go): in the 2-label case (`<sandbox>.<project>`), when the leftmost label is the reserved word `browser`, resolve the container `cspace-<project>-browser` via the `lookupSidecarIP` path and skip the registry-entry match. No registry entry, no lifecycle bookkeeping — if the container exists, the name resolves; after a restart the new IP propagates within one memo TTL. Note: at initial implementation `lookupSidecarIP` ran unbounded and uncached (a bare `lookupSidecarIPCtx(context.Background(), ...)`), so this sentence was aspirational — a hung Apple Container apiserver could spawn one never-exiting `container inspect` subprocess per query. A same-branch follow-up fix (`Bound and memoize sidecar DNS lookups`) added the 2s bound (matching `inspectContainerIP`) and TTL memo + negative cache (via the shared `ipMemo`/`sandboxIPMemo` machinery) to `lookupSidecarIP` itself, making the TTL-memo + negative-cache claim actually true for both this path and the pre-existing 3-label `<service>.<sandbox>.<project>` sidecar path, which shares the same call site.
- Qualified form only. Bare `browser.cspace.test` (1-label) stays NXDOMAIN.
- `browser` becomes a reserved sandbox name: `cspace up browser` is rejected with a clear error (it would collide with `browserSingletonName`'s `cspace-<project>-browser`).
- Sandbox env switches from IPs to the name (cmd_up.go, the block at ~650-659): `CSPACE_BROWSER_CDP_URL=http://browser.<project>.cspace.test:9222`, `PLAYWRIGHT_MCP_CDP_ENDPOINT` same, `PW_TEST_CONNECT_WS_ENDPOINT=ws://browser.<project>.cspace.test:3000/`. Extracted into a small pure helper so it's unit-testable. `BrowserSidecar.IP` remains IP-based internally (hosts injection into the sidecar still needs it). In-sandbox resolution rides the existing dnsmasq → gateway-DNS path that the boot resolution gate already verifies.

### 2. Protocol-level probes

- `waitForCDP` already asserts HTTP 200 on `/json/version` — kept.
- New `waitForRunServerWS(ctx, host, port, max)`: raw TCP dial + real WebSocket upgrade request, success only on an `HTTP/1.1 101` response line. The 2026-07-17 wedge accepted TCP and then hung; this catches that.
- Both probes gate: sidecar reuse in `ensureSharedBrowserSidecar`, restart verification (below), and `cspace browser status` output.

### 3. Restart ladder (host-side logic, one implementation)

`restartBrowserSidecar(ctx, project, plVersion)` in browser.go, all substrate/process invocations routed through a package-level exec seam (function var, same pattern as `validateGitHubToken`) so tests fake every outcome:

1. Read the current sidecar's `cspace.playwright-version` label (best-effort; fall back to caller's version, then `defaultPlaywrightVersion`).
2. `container stop` (bounded). If state still `running`: `container kill --signal SIGKILL`.
3. **Split-brain handling** (observed in the incident: kill reports "not running" while state says running): find the host-side `container-runtime-linux` process whose argv contains the container name (`pgrep -f`), SIGKILL it, poll until the substrate reports the container not running.
4. `container start`. If the container no longer exists (agent `rm`'d it / "shut it down"): recreate via the existing `runBrowserSidecar` (labels and version are reconstructed by that path).
5. Verify: `waitForBrowserIP` → `waitForCDP` → `waitForRunServerWS`, total budget ~120s (first-boot `apt-get` is the slow case; stop/start reuses the installed filesystem and is fast).
6. Return the refreshed `BrowserSidecar` (new IP included for visibility; consumers use the DNS name).

### 4. Daemon endpoint + CLI surface

- **Daemon:** `POST /browser/restart/{project}` on the existing mux (Go 1.22 pattern routes, like `GET /lookup/`). Per-project `sync.Mutex` serializes concurrent restarts. Auth: loopback requests allowed as-is (same trust as the rest of the daemon API); non-loopback requires `Authorization: Bearer <token>` matching a registry entry **of that project**. Responds with the refreshed endpoints JSON or a structured error.
- **CLI:** new `cspace browser` command group (`newBrowserCmd()` in a new `internal/cli/cmd_browser.go`, registered in root.go):
  - `cspace browser restart` — host: calls `restartBrowserSidecar` directly (no daemon dependency); in-sandbox (`sandboxmode.IsInSandbox()`): POSTs the daemon endpoint via `CSPACE_REGISTRY_URL`, fetching its own token from `GET /lookup/<project>/<own-sandbox>` (the same self-lookup `cspace send` relies on).
  - `cspace browser status` — resolves the sidecar (host: inspect; sandbox: the DNS name) and runs both protocol probes, printing per-endpoint health. Works identically in both contexts; no daemon dependency.

## Security notes

- Pre-existing exposure, documented not expanded: `GET /lookup/` and `GET /list` already serve registry entries **including control tokens** to any vmnet peer (that is how in-sandbox `cspace send` self-authenticates today). The restart endpoint's token check is consistent with that model and becomes meaningful if/when lookup is tightened — tracked as its own follow-up finding, out of scope here.
- Restarting a shared sidecar mid-flight kills other agents' browser sessions. Not prevented — per-project serialization only. Documented behavior; agents are expected to use it when the sidecar is broken.

## Testing

- All new logic unit-tested without touching containers: DNS handler case via the existing handler-test patterns with a faked sidecar-IP lookup; WS probe against local `net.Listener` fixtures (101 / 400 / accept-then-hang); restart ladder via the exec seam with scripted outcomes (clean stop, split-brain, missing container); endpoint via `httptest` with a faked ladder; CLI routing via sandboxmode env.
- Suite invocations use `-skip 'TestCspaceLifecycle'` (that test mutates the host daemon — see finding `2026-07-16-integration-test-mutates-host-daemon-and-may-rebuild-image`). No new default-suite integration tests. Never touch live `cspace-resume-redux-*` containers.

## Non-goals

- No change to sidecar ref-count semantics or `cspace down` teardown.
- No `/lookup` auth tightening (follow-up).
- No change to compose-service sidecar resources or addressing.
- Existing running sandboxes keep their baked IPs until recreated — this fixes the next generation.

## Rollout

Ships in the next release; takes effect for sandboxes created by the new binary (env names) and for the sidecar on its next recreation. The daemon must be version-matched (the existing handshake/respawn on `cspace up` handles that).
