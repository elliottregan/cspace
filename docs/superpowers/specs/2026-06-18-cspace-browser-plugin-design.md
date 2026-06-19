# cspace-browser plugin — design

**Date:** 2026-06-18
**Status:** Draft (awaiting review)

## Context

cspace registers browser MCP servers (`playwright`, `chrome-devtools`) for every
sandbox session so agents have browser tools out of the box. Today this is
**imperative**:

- `cspace-entrypoint.sh` `jq`-writes the two servers into `~/.claude.json`
  (`mcpServers`) with `${CSPACE_BROWSER_CDP_URL}` as a literal placeholder, and
  deletes them when no sidecar is present.
- `lib/agent-supervisor-bun/src/claude-runner.ts` also registers them via
  `--mcp-config` for the headless supervisor session.

Both point at a **per-instance** Playwright/Chromium sidecar over CDP
(`$CSPACE_BROWSER_CDP_URL`). Without a sidecar, the stock servers try to launch
a local Chromium, which fails on ARM64 (the cspace image is linux/arm64 on
Apple Container).

Problems with the imperative approach: fragile `jq` munging, duplicated
registration (entrypoint + supervisor), no conflict-scoping (a project's own
`playwright` MCP can clash), and no versioning of the registration.

**Validated facts (2026-06-18, host experiments):**
- `playwright-mcp --cdp-endpoint <shared> --isolated` gives each connection its
  **own isolated browser context on the shared browser** (no cross-instance
  cookie/localStorage leakage).
- `chrome-devtools-mcp --browserUrl <shared>` has **no attach-mode isolation**;
  it shares the default context. Its only isolation is the per-call
  `new_page({isolatedContext:true})` tool, which can't be enforced.
- The current registration (no `--isolated`) shares state across instances when
  they share a browser.

## Goals

1. Package cspace's browser MCP registration as a **versioned, conflict-scoped
   Claude Code plugin** (`cspace-browser`), shipped in-repo and installed at
   container boot.
2. Register `playwright-mcp` with `--isolated` (per-instance isolated context,
   which also makes a future shared browser safe for Playwright).
3. Register `chrome-devtools-mcp` pointed at the same browser. Its context is
   **shared** (accepted): the priority is that every instance has the right
   tools wired up; `playwright-mcp` is the primary, isolated tool and is used
   far more. Session pollution via chrome-devtools is an accepted small
   tradeoff.
4. Replace the imperative entrypoint `jq`-registration with the plugin as the
   single source of truth.
5. Target `${CSPACE_BROWSER_CDP_URL}` so **Phase 2 (single shared browser)
   requires no plugin change** — only what that URL points at changes.

## Non-goals (Phase 1)

- The shared singleton browser, sidecar lifecycle, networking, or per-instance
  hostnames — all Phase 2.
- Solving chrome-devtools-mcp isolation (accepted as shared).

## Design

### Plugin structure (in-repo)

`lib/plugins/cspace-browser/`:

- `.claude-plugin/plugin.json` — manifest (`name: cspace-browser`, `version`,
  `description`).
- `.mcp.json` — the two server declarations, referencing the image-baked
  binaries by bare command, env expanded at spawn:

  ```json
  {
    "mcpServers": {
      "cspace-playwright": {
        "command": "playwright-mcp",
        "args": ["--cdp-endpoint", "${CSPACE_BROWSER_CDP_URL}", "--isolated"]
      },
      "cspace-chrome-devtools": {
        "command": "chrome-devtools-mcp",
        "args": ["--browserUrl", "${CSPACE_BROWSER_CDP_URL}"]
      }
    }
  }
  ```

  Server names are deliberately `cspace-`-prefixed (not bare `playwright` /
  `chrome-devtools`) — see **Server naming + collision avoidance** below.

- A **local-path marketplace** manifest (`.claude-plugin/marketplace.json` at a
  marketplace root in the repo/image) so the plugin is installable via
  `claude plugin marketplace add <path>` + `enabledPlugins`.

The MCP **binaries stay baked in the image** (`Dockerfile`:
`npm install -g @playwright/mcp@<pin> chrome-devtools-mcp@<pin>`). The plugin
installs nothing at runtime; it references the binaries by bare command. Version
control lives in the Dockerfile; the plugin is version-agnostic. We deliberately
do **not** use `npx @pkg@latest` (re-download per spawn, needs network — bad in
a firewalled sandbox).

### Shipping + install at boot

- Embed `lib/plugins/cspace-browser/` into the image via the existing embedded
  assets path (synced to `internal/assets/embedded/`), written to a known
  in-container path (e.g. under the runtime assets dir).
- `cspace-install-plugins.sh` registers the local marketplace and enables
  `cspace-browser` (it already handles marketplaces + `enabledPlugins`). Gate on
  the browser being enabled — skip when `--no-browser` / no sidecar.
- Remove the browser-MCP `jq` block from `cspace-entrypoint.sh`; the plugin
  supersedes it. Equivalent "absent when disabled" behavior = don't enable the
  plugin when there's no sidecar.

### Headless SDK consideration (verification spike)

The agent runs via the supervisor's Agent SDK (`claude-runner.ts`). Confirm
whether **enabled-plugin MCP servers load in SDK/headless sessions**:

- If yes → `claude-runner.ts` drops its `--mcp-config` browser registration; the
  plugin is the single source.
- If no → `claude-runner.ts` keeps passing the two servers (same definitions,
  with `--isolated`); the plugin covers interactive `ssh`/`attach`.

Either way the agent gets the tools. Decision recorded after the spike.

### Server naming + collision avoidance

Two independent naming layers:

- **Tools** from a plugin server are namespaced
  `mcp__plugin_<plugin>_<server>__<tool>`, so they never clash with a
  user/project server's `mcp__<server>__<tool>` tools.
- **Servers**, however, are de-duplicated by **bare name** across scopes, and
  plugin-provided servers are the **lowest** precedence
  (local > project > user > plugin). So a user/project server named `playwright`
  would **silently shadow** a plugin server also named `playwright` — the
  plugin's version never connects, and toggling the plugin doesn't help.

For cspace, a bare `playwright` server name would be a footgun: any project with
its own `playwright` MCP would shadow the plugin's, and the agent would fall back
to that plain server — which launches a local Chromium and **fails on ARM64** —
instead of cspace's `--cdp-endpoint --isolated` variant.

**Decision:** name the plugin's servers **`cspace-playwright`** and
**`cspace-chrome-devtools`** (not bare). This guarantees no bare-name collision,
so cspace's configured servers always connect regardless of the project's own
MCP config. Tools surface as `mcp__plugin_cspace-browser_cspace-playwright__*`.
(Reference: Claude Code MCP scope-precedence + plugin tool-naming docs.)

## Testing / acceptance

- **Unit:** `cspace-install-plugins.sh` installs+enables `cspace-browser` when
  the browser is enabled; skips it when disabled.
- **Integration:** `cspace up` an instance; assert the plugin is enabled and the
  `playwright` + `chrome-devtools` MCP servers register and connect to the
  sidecar; assert `playwright-mcp` runs with `--isolated`.
- **Manual:** two instances; each gets the tools; Playwright contexts are
  isolated (cookie/localStorage cross-read returns only own state);
  chrome-devtools shares (accepted).

## Phase 2 (future — separate spec): single shared browser

- Project-scoped **singleton** sidecar `cspace-<project>-browser`; ref-counted
  lifecycle (tear down when the last instance for the project goes down), not
  per-instance teardown.
- `${CSPACE_BROWSER_CDP_URL}` points all instances at the shared sidecar — **no
  plugin change** needed.
- Per-instance **workspace hostnames** in the shared sidecar's `/etc/hosts`
  (`workspace-<instance> → instance IP`); additive injection + cleanup; the
  single `workspace` entry no longer works under sharing. Same for compose
  sidecars (convex-backend, etc.).
- Verify cross-instance vmnet routing (shared sidecar IP reachable from every
  instance microVM).

## Open questions / risks

- **SDK plugin-MCP loading** (above) — resolve with a spike before deleting the
  `claude-runner.ts` registration.
- **Local-marketplace non-interactive install** at boot — extend
  `cspace-install-plugins.sh`'s existing install path to a local marketplace.
- **chrome-devtools shared state** — accepted; revisit only if it causes real
  agent interference.

## Rollout

- **Phase 1** lands the plugin on the **current per-instance sidecar** — no
  behavior regression (same servers, now `--isolated` + plugin packaging),
  removes the entrypoint `jq` munging, and gives conflict-scoping.
- **Phase 2** swaps the sidecar to a project-scoped singleton; the plugin is
  unchanged.
