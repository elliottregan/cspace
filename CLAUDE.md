# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**cspace** is a CLI for managing isolated Claude Code sandboxes on macOS, built on Apple Container (lightweight microVMs driven by the `container` CLI). `cspace up` provisions a sandbox with its own git clone of the project, seeds Claude Code inside it, wires per-sandbox DNS (`<sandbox>.<project>.cspace.test`), and shares one ref-counted browser sidecar per project (Chromium CDP on :9222 for browser MCP, a Playwright run-server on :3000 for e2e). The CLI is Go (Cobra); the optional in-sandbox agent supervisor is Bun/TypeScript.

**Trust this file and the code over older docs.** This file was rewritten 2026-07-16 to match the code. Design docs under `docs/superpowers/specs/` describe components that were removed or never shipped in their described form — a Node ESM supervisor with a Unix control socket, agent playbooks (`lib/agents/`), advisor agents, coordinator orchestration, and the `cspace-context` MCP server (the root `.mcp.json` still registers `cspace context-server`, but no such command exists). Known-but-unfixed problems are tracked as findings in `.cspace/context/findings/` — check there before "discovering" a bug, and log new ones there.

## Commands

- `cspace up [name]`, `cspace down`, `cspace attach`, `cspace ports` — sandbox lifecycle
- `cspace send <instance> <text>` — inject a user turn into a sandbox's supervisor via its HTTP control port
- `cspace keychain init|status` — store/inspect credentials in the macOS Keychain
- `cspace image build` — rebuild the sandbox image (uses the repo's `lib/` when run from a cspace checkout, embedded assets otherwise)
- `cspace daemon …`, `cspace dns …`, `cspace registry …`, `cspace doctor` — host daemon, resolver install, registry inspection, diagnostics
- `cspace browser restart|status` — restart or health-check the project's shared browser sidecar; works both from the host and from inside a sandbox
- `cspace self-update`, `cspace version`, `cspace completion`

## Development

```bash
make build        # check-hooks + sync-embedded + go build -> bin/cspace-go
make test         # go tests (runs sync-embedded first)
make vet
make lint
make check        # fmt-check + vet + lint + test
cspace image build  # rebuild the sandbox image after Dockerfile/scripts changes

cd lib/agent-supervisor-bun && bun install   # supervisor deps (Bun, not pnpm)
```

**Always build via `make`** (or run `make sync-embedded` first). `internal/assets/embedded/` is gitignored and populated from `lib/` by `make sync-embedded`; a bare `go build`/`go install` on a clean checkout embeds an empty asset tree and fails only at runtime.

## Architecture

### Go CLI (`cmd/cspace/`, `internal/`)

Entry point is `cmd/cspace/main.go` → `cli.Execute()`. Commands are `newXxxCmd()` functions in `internal/cli/`, registered via `AddCommand()` in `root.go`. `cmd_up.go` holds the (large, ~875-line) boot flow: daemon spawn, credential reconciliation, devcontainer merge, clone provisioning, sidecars, registry writes, DNS gate, attach. Internal packages:

- **config** — three-layer JSON merge: embedded `defaults.json` → `.cspace.json` → `.cspace.local.json` via `config.Load()`. `DeepMerge` replaces arrays wholesale (setting `plugins.install` in `.cspace.json` discards the whole default list, it does not append).
- **secrets** — credential resolution, macOS Keychain access, host auto-discovery (see Credentials below)
- **registry** — the sandbox registry persisted/served by the daemon
- **substrate/applecontainer** — wrapper around the Apple Container `container` CLI (run/stop/inspect/build)
- **orchestrator** — multi-service lifecycle for sidecars: compose-plan execution, healthchecks, `/etc/hosts` injection
- **compose/v2**, **devcontainer** — parse a project's `dockerComposeFile`/`devcontainer.json` and merge them into the sandbox plan
- **overlay** — the `cspace up` TUI overlay
- **planets** — instance naming/port data (`planets.json`); **sandboxmode** — in-sandbox detection via `CSPACE_*` env; **features** — optional runtime feature installers; **assets** — the `go:embed` FS

### Host daemon (`cspace daemon serve`)

One background process per host, auto-spawned by `cspace up` (detached with `Setsid`, logging to `~/.cspace/daemon.log` with 1MiB rotation). It serves DNS for `*.cspace.test` on `127.0.0.1:5354` (host side; `sudo cspace dns install` writes `/etc/resolver/cspace.test`) and `192.168.64.1:5354` (vmnet gateway side, for sandboxes and sidecars), plus an HTTP registry API on `127.0.0.1:6280`. `cspace up` does a version handshake against `/health` and stops/respawns a version-mismatched daemon. DNS answers prefer a live `container inspect` IP (TTL-memoized, negative-cached) over the registry-recorded IP.

### Agent supervisor (`lib/agent-supervisor-bun/`)

Bun/TypeScript, compiled to a single binary by `build.ts` during image build. Wraps `@anthropic-ai/claude-agent-sdk`'s `query()` with an async prompt queue so user turns can be injected mid-session. Control is HTTP on `CSPACE_CONTROL_PORT` (default 6201) with bearer-token auth (`CSPACE_CONTROL_TOKEN`): `POST /send` injects a turn, `GET /health` reports the session. Events append to `/sessions/primary/events.ndjson`; on restart the supervisor resumes the last session id found in that log. `lib/runtime/scripts/cspace-supervisor-loop.sh` restarts it, treating exit codes 0/143/137 as clean shutdown. **Status note:** this layer is lightly used and is a candidate for removal; see the supervisor finding before investing work here.

### Sandbox runtime (`lib/`)

`lib/` is the source of truth; `make sync-embedded` copies an explicit allowlist into `internal/assets/embedded/` for `go:embed`.

- **templates/Dockerfile** — the sandbox image (Debian-based: Node, Bun, Claude Code, MCP servers, dev tooling). Apple Container's builder does **not** recurse directory COPYs, so files are COPY'd individually — when you add a file under `lib/runtime/scripts/` or `lib/plugins/`, you must also add a COPY line or it silently won't ship (see the per-file-COPY finding).
- **runtime/scripts/** — `cspace-entrypoint.sh` (settings seed, git identity, in-sandbox DNS forwarder, inbound DNAT), `cspace-install-plugins.sh`, `cspace-supervisor-loop.sh`, `statusline.sh`
- **runtime/features/** — optional installers: node, python, git, github-cli, docker-in-docker, common-utils
- **plugins/** — the `cspace-browser` Claude plugin (marketplace + `.mcp.json` wiring for the shared browser sidecar)
- **defaults.json** — embedded config defaults. Vestigial keys from the earlier orchestration design — `advisors`, `agent`, `verify`, `claude`, `post_setup` — are parsed into `config.Config` but have no consumers.

## Project context (`.cspace/context/`)

Layered planning context, bind-mounted into every sandbox for the project so writes are visible to sibling agents without git push/pull.

- `direction.md`, `principles.md`, `roadmap.md` — human-owned; edit directly.
- `decisions/`, `discoveries/` — agent-owned terminal records; immutable once written.
- `findings/` — lifecycle records (bugs, observations, refactor proposals). Plain markdown, edited directly (the MCP server from the original spec is not currently shipped). Frontmatter: `title`, `date`, `kind: finding`, `status: open|acknowledged|resolved|wontfix`, `category: bug|observation|refactor`, `tags`. Body: `## Summary`, `## Details`, `## Updates` (append timestamped status entries; never rewrite history). When a commit resolves a finding, append `(cs-finding:<slug>)` to the commit message and add a resolved entry to its Updates section.

Read the relevant findings at the start of non-trivial work.

## Anthropic credentials

cspace sandboxes need an Anthropic credential to drive Claude Code. Two token formats are supported, but they **must ride different env vars** — the wrong carrier causes "Invalid API key" errors and a spurious "custom API key" prompt in interactive Claude:

- **Long-lived API key** (`sk-ant-api-…`) → `ANTHROPIC_API_KEY`. Stable, no expiry. **Recommended for daily use** — paste once via `cspace keychain init`.
- **Long-lived OAuth token** (`sk-ant-oat-…`, from `claude setup-token`) → `CLAUDE_CODE_OAUTH_TOKEN`. `cspace keychain init` routes by prefix automatically.
- **Short-lived OAuth token** auto-discovered from the host's `claude /login` Keychain entry. Convenient for first-run, but expires within hours — don't rely on it for sessions over a day.

`cspace keychain status` shows where each credential is sourced from. Resolution order (first reachable wins): `<project>/.cspace/secrets.env` → `~/.cspace/secrets.env` → macOS Keychain (`cspace-ANTHROPIC_API_KEY` / `cspace-CLAUDE_CODE_OAUTH_TOKEN`) → auto-discovery from the host's Claude Code login.

GitHub credentials follow the same precedence; auto-discovery uses `gh auth token`, and a token GitHub rejects with 401 at `up`-time preflight is replaced by the `gh auth token` fallback. The three GitHub env vars are deliberate: `gh` CLI reads `GH_TOKEN`, the GitHub MCP server reads `GITHUB_PERSONAL_ACCESS_TOKEN`, Actions-style tooling reads `GITHUB_TOKEN` — cspace propagates one value across all three (`propagateFamily`).

## Env plumbing

`docs/env-cspace.md` documents the `.env.cspace` convention (project-declared container overrides), the full env merge order, and the `$CSPACE_WORKSPACE_HOST` / e2e `baseURL` guidance. Two sharp edges, both tracked as findings: compose `env_file` entries out-rank `.cspace/secrets.env` (a project redeclaring a secret key silently overrides the delivered credential — `cspace up` warns for the known secret keys), and `--env` does not currently win over ambient host-shell credentials despite the documented contract.

Security caveat: secrets currently transit `-e` flags into the substrate, and Apple Container's `vminitd` logs the full process env — anyone with `container logs` access on the host can read them.

## Browser sidecar

The shared per-project sidecar (`cspace-<project>-browser`) has a stable DNS name, `browser.<project>.cspace.test`, served by the host daemon's DNS handler — it survives sidecar restarts, unlike the raw vmnet IP a sandbox used to have baked into its env. `PW_TEST_CONNECT_WS_ENDPOINT` carries this name. The CDP env vars (`CSPACE_BROWSER_CDP_URL`, `PLAYWRIGHT_MCP_CDP_ENDPOINT`) instead carry `http://127.0.0.1:9222` — Chrome's DevTools HTTP endpoint rejects name-based Host headers, so the entrypoint runs a loopback relay that dials the DNS name per connection (same restart-safety, Chrome-acceptable Host). If the sidecar wedges or an agent tears it down, `cspace browser restart` (host-side or in-sandbox, via the daemon's `POST /browser/restart/{project}`) restarts it through an escalation ladder and reverifies liveness with protocol-level probes; `cspace browser status` reports current health without restarting.

## Key patterns

- **Instance naming**: planet names (`mercury`, `venus`, …) with deterministic ports are reserved for the human-facing TUI. Agents spawning sandboxes should use descriptive names (`issue-<n>`, a short task label). Note: explicit names currently bypass collision checks (see finding).
- **Sessions**: per-sandbox at `~/.cspace/sessions/<project>/<sandbox>/` on the host, bind-mounted into the sandbox; wiped by `cspace down`.
- **Adding a CLI command**: create `newXxxCmd()` in a new file under `internal/cli/`, register it in `root.go`.
- **Template resolution**: `cspace image build` uses the repo's `lib/templates/Dockerfile` when run from a cspace checkout, otherwise the embedded copy.

## Security posture (read before relying on it)

- **There is no firewall.** The `firewall.*` config is parsed and merged but no egress filtering is implemented — deliberately tabled for now (agents benefit from web access); see the firewall finding. Never describe sandboxes as network-restricted.
- The entrypoint's inbound DNAT forwards all vmnet TCP to loopback, so "loopback-only" services in a sandbox are reachable by its vmnet peers.
- The supervisor control port binds 0.0.0.0 with bearer-token auth; the check is skipped if the token is empty (production always sets one).

## Commit Style

Short imperative sentences describing what changed and why. Examples from history:
- "Fix EPIPE crash in supervisor and $DC reference in cmd_up"
- "Add incremental commit+push after implement and verify phases"
- "Surface stderr from failed container exec commands"
