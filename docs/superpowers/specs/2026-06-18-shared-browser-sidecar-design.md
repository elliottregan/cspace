# Shared Browser Sidecar (Phase 2) — design

**Date:** 2026-06-18
**Status:** Draft (awaiting review)
**Follows:** `2026-06-18-cspace-browser-plugin-design.md` (Phase 1). The plugin
already points MCP servers at `${CSPACE_BROWSER_CDP_URL}` and runs `playwright-mcp
--isolated`, so this phase changes only **what that URL points at** and the
sidecar's lifecycle — no plugin change.

## Context

Today cspace runs **one browser sidecar per instance**:
`startBrowserSidecar(ctx, project, sandbox, plVersion)` (`internal/cli/browser.go:77`)
starts a container `cspace-<project>-<sandbox>-browser` (`browserContainerName`,
`browser.go:46`) from the Playwright image, running a headless Chromium (CDP on
`:9222` via socat) + a `playwright run-server` (`:3000`). It torches any
same-named container first (`browser.go:84`). `cmd_up.go` injects its CDP URL as
`CSPACE_BROWSER_CDP_URL` (`cmd_up.go:542`) and tears it down on per-instance
`cspace down` via the registry's `BrowserContainer` field (`cmd_down.go`).

Each sidecar is a separate microVM running a full Playwright/Chromium image. With
N instances of a project, that is N heavyweight browser containers. **Goal: one
shared browser per project**, all instances connecting to it over CDP. Validated
(2026-06-18, host experiment): `playwright-mcp --cdp-endpoint <shared> --isolated`
gives each connection its **own isolated context on the shared browser** — no
cross-instance cookie/localStorage leakage — so per-instance isolation survives
sharing.

Two things make this tractable that weren't obvious at first:

1. **The DNS daemon already does per-instance resolution.** The cspace daemon
   answers `*.cspace.test` (`cmd_daemon.go`): `<sandbox>.<project>.cspace.test` →
   that instance's IP, and `<service>.<sandbox>.<project>.cspace.test` → that
   instance's compose-sidecar IP (via live `container inspect`,
   `cmd_daemon.go:270-300`). And the browser sidecar **already runs dnsmasq
   forwarding `*.cspace.test` to that daemon** (`browser.go`: "so the headless
   browser resolves `*.cspace.test` the same way the cspace sandbox does"). So
   the shared browser can resolve per-instance names with no new infrastructure.

2. **The hostname collision is an artifact of the current design, not
   fundamental.** Today the sidecar's `/etc/hosts` is injected with **bare**
   names (`workspace`, `convex-backend`) mapped to one instance's IPs
   (`cmd_up.go:662-703`). Bare names collide when shared. The friendly
   `.cspace.test` names are already per-instance-unique and already resolved —
   so the shared browser stops using bare-name `/etc/hosts` injection and uses
   the friendly DNS instead.

## Goals

1. **One shared browser sidecar per project**, `cspace-<project>-browser`,
   reused across all instances of that project.
2. **Ref-counted lifecycle** keyed on the registry: the last `cspace down` for a
   project stops the shared sidecar; `cspace registry prune` is the leak
   backstop.
3. **Per-instance isolation preserved** via `playwright-mcp --isolated` (Phase 1).
4. **Networking via friendly DNS**: drop bare-name `/etc/hosts` injection into
   the shared browser; set `CSPACE_WORKSPACE_HOST` to the per-instance friendly
   name `<sandbox>.<project>.cspace.test`; rely on the existing daemon to resolve
   it (and compose-sidecar friendly names).
5. **resume-redux runs unchanged** — its e2e suite and the MCP agent both reach
   the per-instance site through the shared browser. This is the acceptance bar.
6. **Opt-out** to per-instance browser for projects that need the browser to hit
   bare compose-sidecar URLs directly.

## Non-goals (Phase 2)

- The per-context **proxy** that would make even bare compose-sidecar URLs
  resolve per-context automatically (Phase 3; needs a per-instance proxy + MCP
  `--proxy-server` support).
- `chrome-devtools-mcp` per-instance isolation (it shares context on a shared
  browser — accepted in Phase 1; `playwright` carries the isolation guarantee).
- The `options.plugins` single-source supervisor unification (separate Phase-1.5
  cleanup).

## Design

### 1. Singleton sidecar

- **Name:** `cspace-<project>-browser` (drop the `<sandbox>` segment). Add
  `browserSingletonName(project)` alongside the existing `browserContainerName`.
- **Start-if-absent / reuse-if-healthy:** replace the torch-on-collision at
  `browser.go:84` with: if the singleton container is running and its CDP
  endpoint answers, **reuse** it (inspect its IP, reconstruct the CDP/run-server
  URLs); otherwise start it. Factor this into
  `ensureSharedBrowserSidecar(ctx, project, plVersion) (*BrowserSidecar, error)`.
- **Version pin:** the singleton is pinned to the project's `@playwright/test`
  via `detectPlaywrightVersion` (`browser.go:316`). One project ⇒ one pin, so
  the run-server's strict same-version handshake is satisfied for all instances.
  See **Edge cases** for pin mismatch across clones.

### 2. Ref-counted lifecycle

- `cspace up` no longer registers a per-instance `BrowserContainer` for teardown;
  instead the shared sidecar's name is derivable from the project. The registry
  already tracks every instance (`registry.List()`, keyed by project+name).
- On `cspace down <sandbox>`: after removing the instance, count remaining
  registry entries for the **project**. If zero, stop the shared sidecar
  (`stopBrowserSidecar`); otherwise leave it running.
- **Backstop:** `cspace registry prune` (and the daemon's idle path) reaps a
  shared sidecar whose project has no live instances — covers abnormal instance
  deaths where `cspace down` never ran.
- The deferred error-teardown in `cmd_up.go:562` must NOT stop a *reused* shared
  sidecar (only one it just started, and only if no other instance is using it).

### 3. Networking — friendly DNS, no bare-name injection

- **Workspace host (automatic):** set `CSPACE_WORKSPACE_HOST =
  <sandbox>.<project>.cspace.test` (was the bare `"workspace"`, `cmd_up.go:556`).
  The daemon resolves it to the instance's workspace IP; the shared browser's
  dnsmasq forwards the lookup. Drop the bare-`workspace` injection into the
  **browser sidecar** (`cmd_up.go:663` `InjectWorkspaceHost(browserSidecar, ip)`).
  The **workspace container's own** `workspace → 127.0.0.1` entry
  (`cmd_up.go:667`) stays — it's that container resolving itself, not the shared
  browser.
- **Compose sidecars (opt-in for direct browser access):** stop injecting bare
  compose hostnames into the shared browser (`cmd_up.go:700-703`,
  `hosts["workspace"]=ip` + `orch.ServiceIPs()`). A project whose **browser**
  must reach a compose sidecar directly points its app's browser-facing URL at
  the friendly `<service>.<sandbox>.<project>.cspace.test` (the daemon already
  resolves it). cspace exposes those friendly names to the workspace as env vars
  so the project can wire them. The app's **internal** calls keep using bare
  compose-DNS names (per-instance, unaffected). No daemon change — the 3-label
  resolution already exists.
- **No new daemon/proxy code.** The only resolution change is which names we
  hand out (friendly, not bare) and dropping the browser-sidecar `/etc/hosts`
  injection.

### 4. Per-instance isolation

Unchanged from Phase 1: each instance's `playwright-mcp` runs `--cdp-endpoint
<shared> --isolated`, yielding its own browser context on the shared Chromium.
`chrome-devtools-mcp` shares the default context (accepted).

### 5. Opt-out

- Default is **shared**. Opt out via the `cspace up --no-shared-browser` flag OR
  `.cspace.json` `browser.shared: false`; **both are supported and the flag
  overrides the config**. Opting out starts a **per-instance** sidecar (today's
  behavior, the existing `startBrowserSidecar(project, sandbox, …)` path) and
  injects bare `/etc/hosts` as today — for projects whose browser hits bare
  compose-sidecar URLs and don't route through a workspace proxy.

## resume-redux compatibility (the acceptance proof)

resume-redux is already architected for "the browser can only reach the
workspace," which is exactly the shared-browser constraint:

- **e2e reads the env var:** `scripts/e2e.sh:63-64` sets
  `BASE_URL="http://${CSPACE_WORKSPACE_HOST}:4173"`. When cspace sets
  `CSPACE_WORKSPACE_HOST` to the friendly name, e2e follows automatically — no
  project change.
- **browser→Convex rides the workspace proxy:** post-create forces the `__SELF__`
  proxy ("browser-side fetches don't hit a non-resolvable hostname [in] cspace's
  browser sidecar"); `scripts/preview-server.mjs` proxies `/__convex/*` to
  `convex-backend` resolved **inside the workspace**. The browser never resolves
  `convex-backend`.

So under the shared browser: e2e (run-server) and the MCP agent (CDP) reach the
per-instance workspace via the friendly name; Convex rides the workspace proxy;
`--isolated` keeps contexts separate. resume-redux needs **no change**, and it
does not need the compose-sidecar opt-in.

## Edge cases

- **Playwright pin mismatch across clones:** if a later instance's clone pins a
  different `@playwright/test` than the running singleton, the run-server
  handshake would fail. Resolution: `ensureSharedBrowserSidecar` compares the
  needed pin against the running singleton; on mismatch, fall back to a
  per-instance sidecar for that instance with a warning (don't restart the
  shared one out from under live instances). Rare (same project).
- **Concurrent first-`up` race:** two instances starting simultaneously could
  both try to create the singleton. `container run --name` errors on the second
  ("already exists"); treat that as "reuse" and inspect the existing one.
- **Stale singleton after crash:** a singleton left running with zero live
  instances is reaped by `cspace registry prune` — which, because the singleton
  name is derived (not stored in a registry entry), must explicitly stop
  `browserSingletonName(project)` for any project with `CountForProject == 0`
  (NOT covered by the daemon idle path, which only exits the daemon process and
  never stops containers).
- **`down` during `up` (narrow TOCTOU):** the singleton is created early in
  `up` but the instance isn't registered until later. A `down` of a project's
  only still-booting instance, racing its `up` before registration, would read
  `CountForProject == 0` and stop the singleton out from under the live `up`.
  Pre-existing registration-ordering window; requires unusual concurrent
  operator action; accepted for this phase.
- **`CSPACE_WORKSPACE_HOST` consumers:** anything still hardcoding bare
  `workspace` (instead of reading the env var) breaks under sharing. Document the
  contract; resume-redux already complies.
- **In-container friendly DNS is now load-bearing (daemon dependency):** because
  the shared browser resolves the workspace via `.cspace.test` (no bare-name
  `/etc/hosts` injection), the cspace daemon's **gateway** DNS listener
  (`192.168.64.1:5354`) must be up. It binds the vmnet bridge IP, which doesn't
  exist until the first container boots — and `cspace up` spawns the daemon
  before that — so the one-shot bind lost a startup race and NXDOMAINed
  in-container lookups. This phase therefore makes the gateway listener **retry**
  its bind (Phase 2 amends the spec's earlier "no daemon change" assumption; the
  host-loopback listener is unaffected).

## Testing / acceptance

- **Unit:** the ref-count decision (stop shared sidecar iff zero remaining
  project instances) as a pure function over a registry-entry list; the friendly
  `CSPACE_WORKSPACE_HOST` value construction.
- **Integration:** boot two instances of a project (no compose) on one shared
  browser; assert (a) one `cspace-<project>-browser` container exists and both
  instances' MCP servers connect to it; (b) Playwright contexts are isolated
  (cookie cross-read returns only own); (c) `cspace down` of the first leaves the
  shared sidecar up, `cspace down` of the second stops it (ref-count); (d)
  `--no-shared-browser` yields a per-instance sidecar.
- **resume-redux acceptance (the bar):** two resume-redux instances on one shared
  browser — the e2e suite passes per-instance, and the MCP agent loads the site
  per-instance (Convex via the workspace proxy). No leak after both go down.

## Rollout

- Default shared; `--no-shared-browser` opt-out from day one.
- No daemon change, no plugin change. Changes are concentrated in
  `internal/cli/browser.go` (singleton name + ensure/reuse), `internal/cli/cmd_up.go`
  (friendly host env, drop browser `/etc/hosts` injection, reuse the singleton),
  `internal/cli/cmd_down.go` (ref-count teardown), and the opt-out flag/config.
- Compose-sidecar friendly names are **always** exposed to the workspace as env
  vars (harmless when unused); projects opt into browser→sidecar direct access
  by wiring their app's browser-facing URL to them.
