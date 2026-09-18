# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**cspace** is a CLI for managing isolated Claude Code sandboxes on macOS, built on Apple Container (lightweight microVMs driven by the `container` CLI). `cspace up` provisions a sandbox with its own git clone of the project, seeds Claude Code inside it, wires per-sandbox DNS (`<sandbox>.<project>.cspace.test`), and shares one ref-counted browser sidecar per project (Chromium CDP on :9222 for browser MCP, a Playwright run-server on :3000 for e2e). The CLI is Go (Cobra); the optional in-sandbox agent supervisor is Bun/TypeScript.

**Trust this file and the code over older docs.** This file was rewritten 2026-07-16 to match the code. Design docs under `docs/superpowers/specs/` describe components that were removed or never shipped in their described form — a Node ESM supervisor with a Unix control socket, agent playbooks (`lib/agents/`), advisor agents, coordinator orchestration, and the `cspace-context` MCP server (the root `.mcp.json` that once registered `cspace context-server` was deleted; no such command was ever implemented). Known-but-unfixed problems are tracked as findings in `.cspace/context/findings/` — check there before "discovering" a bug, and log new ones there.

**`docs/` is plain markdown, not a site.** The Astro/Starlight docs site that used to live there (`docs/src/`, `astro.config.mjs`, `netlify.toml`, and 7 npm dependencies) was removed on 2026-08-06 — it was unmaintained and several pages documented components that were never shipped. It is deliberately gone; don't reintroduce a docs-site build. What remains is reference markdown that Go code names in user-facing warnings and comments — `devcontainer-subset.md`, `env-cspace.md`, `image-dependencies.md`, `migration-from-cspace-json.md` (plus `superpowers/` design history and `fixtures/`). Renaming or deleting one of those four means updating its reference in `internal/` (e.g. `internal/compose/v2/subset.go`, `internal/devcontainer/validate.go`, `internal/cli/cmd_up.go`).

## Commands

- `cspace up [name]`, `cspace down`, `cspace ports` — sandbox lifecycle
- `cspace attach <name>` — attach to a persistent tmux session (`cspace-claude`) inside the sandbox, so closing the terminal no longer ends the Claude session and the next `cspace attach` resumes it with its screen intact. Runs `container exec` as a supervised child (not `syscall.Exec`) so cspace stays alive to detach its tmux client when the session ends. `--no-tmux` is a hidden escape hatch for the old direct-exec behavior; a sandbox built from an image that predates tmux falls back to it automatically, with a warning that the session will not survive the window closing
- `cspace send <instance> <text>` — inject a user turn into a sandbox's supervisor via its HTTP control port
- `cspace agent status <sandbox>` — print the sandbox agent's steering status (session, working/idle state, queue depth, last event)
- `cspace agent interrupt <sandbox>` — cancel a sandbox agent's in-flight task
- `cspace keychain init|status` — store/inspect credentials in the macOS Keychain
- `cspace image build` — rebuild the sandbox image (uses the repo's `lib/` when run from a cspace checkout, embedded assets otherwise)
- `cspace daemon …`, `cspace dns …`, `cspace registry …`, `cspace doctor` — host daemon, resolver install, registry inspection, diagnostics
- `cspace browser restart|status` — restart or health-check the project's shared browser sidecar; works both from the host and from inside a sandbox
- `cspace sidecar restart <service>` — restart one compose sidecar (a backend, a database); works from the host (`--sandbox` when several are running) and from inside a sandbox, so an agent can recover its own dependencies
- `cspace tui` — full-screen dashboard of all cspace containers (grouped by project) with attach / down / agent send·interrupt / browser restart
- `cspace self-update`, `cspace version`, `cspace completion`

## Development

```bash
make build        # check-hooks + sync-embedded + go build -> bin/cspace-go
make test         # go tests (runs sync-embedded first)
make vet
make lint
make test-scripts # bash tests (scripts/*.test.sh, lib/runtime/scripts/*.test.sh)
make check        # fmt-check + vet + lint + test + test-scripts
cspace image build  # rebuild the sandbox image after Dockerfile/scripts changes

cd lib/agent-supervisor-bun && bun install   # supervisor deps (Bun, not pnpm)
bun test                                     # supervisor tests
bun run typecheck                            # supervisor typecheck — NOT run by test or build
```

**Typecheck the supervisor after any SDK bump.** `bun test` and `bun build.ts` both use Bun's transpiler, which strips types without checking them — so the supervisor can compile and pass all 45 tests with broken types. Its tests deliberately type the SDK query handle structurally (`routes.ts`) so they run without a real SDK, which means an upstream signature change is invisible to them. `bun run typecheck` (`tsc --noEmit`) is the only thing that catches it; `@anthropic-ai/claude-agent-sdk` 0.3.x widening `Query.interrupt()`'s resolved value was caught exactly this way and nothing else flagged it. It is not wired into `make check`, which covers the Go side only — run it by hand after touching the supervisor or its deps.

**Always build via `make`** (or run `make sync-embedded` first). `internal/assets/embedded/` is gitignored and populated from `lib/` by `make sync-embedded`; a bare `go build`/`go install` on a clean checkout embeds an empty asset tree and fails only at runtime.

## Releases

**Releases are cut locally from a Mac, not by CI.** `.github/workflows/release.yml` was deleted on 2026-08-27; `ci.yml` stays and still runs fmt/vet/lint/test on pushes and PRs, but it no longer publishes anything.

```bash
make release TAG=v1.0.0-rc.48 ARGS=--dry-run   # every guard + a throwaway build
make release TAG=v1.0.0-rc.48                  # tag, publish
```

`scripts/release.sh` refuses without an explicit tag, then gates on: tag shape (`vX.Y.Z[-rc.N]`), a clean tree (untracked files included — goreleaser counts them as dirty), branch `main` (`CSPACE_RELEASE_ALLOW_BRANCH=1` overrides), the tag being unused locally and on origin, HEAD matching `origin/main`, and `make check`. Only then does it tag, push, and run `goreleaser release --clean`.

It lives here rather than in Actions because **one token covers everything**: `gh auth token` authenticates both the release and the Homebrew tap push (the tap is the same account's repo), so the separate `HOMEBREW_TAP_GITHUB_TOKEN` secret — invalid and silently stranding the tap from rc.36 to rc.41 while every release still reported success — is gone.

**A published GitHub release is immutable.** A failure after the tag is pushed cannot be fixed by re-running the same tag — cut the next rc. The script says so on failure, and refuses a tag that already exists.

**The sandbox image is not published.** Publishing to ghcr was built and then dropped at rc.47: Apple Container's `image push` fails against ghcr.io with `BLOB_UPLOAD_UNKNOWN` (see the finding). Every host builds its own image, and `cspace up` now does that automatically when `cspace:latest` is missing rather than failing the boot; a stale image still prompts. For reference if this is ever revisited: the image is 362 MB as an OCI tar, of which only ~42 MB (the cspace binary and supervisor) changes per release.

## Architecture

### Go CLI (`cmd/cspace/`, `internal/`)

Entry point is `cmd/cspace/main.go` → `cli.Execute()`. Commands are `newXxxCmd()` functions in `internal/cli/`, registered via `AddCommand()` in `root.go`. `cmd_up.go` holds the (large, ~875-line) boot flow: daemon spawn, credential reconciliation, devcontainer merge, clone provisioning, sidecars, registry writes, DNS gate, attach. Internal packages:

- **config** — three-layer JSON merge: embedded `defaults.json` → `.cspace.json` → `.cspace.local.json` via `config.Load()`. `DeepMerge` replaces arrays wholesale (setting `plugins.install` in `.cspace.json` discards the whole default list, it does not append).
- **control** — the control API the TUI and CLI commands share, so "how do you attach," "how do you send a turn," "how do you read status" each have exactly one implementation. The attach argv and TERM/COLORTERM mapping (`argv.go`), the tmux driver — presence probe, client list, detach (`tmux.go`) — and the attach lock plus per-client records under `~/.cspace/controlplane/<project>/<sandbox>/` (`attach.go`) are plain functions that need no `Client`; `cmd_attach.go` and the TUI's `Attach` action both call into them directly. Every query (`Snapshot`, `AgentStatus`, `Events`, `InteractiveState`, `Ports`) and action (`Down`, `Send`, `Interrupt`, `RestartBrowser`, `Up`, `ListClients`, `DetachClient`) hangs off one `Client`/`Options`, seamed on `ContainerCLI` (the substrate), `EntryStore` (the registry) and `Host` (the teardown/browser-restart operations whose implementations still live in `internal/cli`, since `internal/control` must not import it). `Up` is the one action that reaches outside these seams: with no callable `cspace up` function to invoke, it shells out to the same `cspace` binary it is already running inside. `internal/tui` consumes the package through type aliases (`internal/tui/types.go`) rather than keeping its own copies, so a `Row` built there is the same type as one built here.
- **secrets** — credential resolution, macOS Keychain access, host auto-discovery (see Credentials below)
- **registry** — the sandbox registry persisted/served by the daemon
- **substrate/applecontainer** — wrapper around the Apple Container `container` CLI (run/stop/inspect/build/stats). Two `container run` flags are load-bearing and easy to drop by accident: `--kernel-arg sysctl.net.ipv4.conf.all.route_localnet=1` (the entrypoint's inbound DNAT is illegal without it, and Apple Container mounts `/proc/sys` read-only so it cannot be set from inside), and `--init` (PID 1 that forwards signals and reaps orphans — the sandbox image no longer carries tini).
- **sidecars** — multi-service lifecycle: compose-plan execution, healthchecks, `/etc/hosts` injection
- **compose/v2**, **devcontainer** — parse a project's `dockerComposeFile`/`devcontainer.json` and merge them into the sandbox plan
- **overlay** — the `cspace up` TUI overlay
- **planets** — instance naming/port data (`planets.json`); **sandboxmode** — in-sandbox detection via `CSPACE_*` env; **features** — optional runtime feature installers; **assets** — the `go:embed` FS

### Host daemon (`cspace daemon serve`)

One background process per host, auto-spawned by `cspace up` (detached with `Setsid`, logging to `~/.cspace/daemon.log` with 1MiB rotation). It serves DNS for `*.cspace.test` on `127.0.0.1:5354` (host side; `sudo cspace dns install` writes `/etc/resolver/cspace.test`) and the vmnet gateway on :5354 (sandbox/sidecar side — the gateway is *discovered* per install via `container network inspect default`, not hardcoded: 1.2 allocated `192.168.64.1`, 1.3 allocates `192.168.65.1`), plus an HTTP registry API on `127.0.0.1:6280`. `cspace up` does a version handshake against `/health` and stops/respawns a version-mismatched daemon. DNS answers prefer a live `container inspect` IP (TTL-memoized, negative-cached) over the registry-recorded IP.

### Agent supervisor (`lib/agent-supervisor-bun/`)

Bun/TypeScript, compiled to a single binary by `build.ts` during image build. This is cspace's general-purpose in-sandbox agent: it wraps `@anthropic-ai/claude-agent-sdk`'s `query()` with an async prompt queue so user turns can be injected mid-session, always with `settingSources: ["project"]` so the headless agent picks up the workspace's own `CLAUDE.md`/`.claude/settings.json` like an interactive session would.

- **Config surface**: a role — text appended to (never replacing) the system prompt — resolves from `/sessions/agent-role.md` (staged by `cspace up --role <host-path>`, cleared on a subsequent `up` with no `--role`) or, failing that, the committed `/workspace/.cspace/agent.md` convention. Model comes from `CSPACE_AGENT_MODEL`, set from `cspace up --model` or `.cspace.json`'s `agent.model` (`--model` wins); empty leaves the SDK/CLI default model in effect.
- **Control surface**: HTTP on `CSPACE_CONTROL_PORT` (default 6201), bearer-token auth (`CSPACE_CONTROL_TOKEN`) enforced on every route. `POST /send` injects a turn (`cspace send <sandbox> <text>`); `POST /interrupt` cancels the in-flight SDK query (`cspace agent interrupt <sandbox>`, 409 when there's no active task); `GET /status` reports session, `working`/`idle` state, last event, and queue depth (`cspace agent status <sandbox>`); `GET /health` reports liveness (polled by `cspace up`'s boot wait). The supervisor refuses to start at all if `CSPACE_CONTROL_TOKEN` is empty rather than serve unauthenticated on `0.0.0.0`.
- **Liveness**: any terminal outcome of the SDK stream — it throwing, or its async iterator simply ending — makes the supervisor `exit(1)` so `lib/runtime/scripts/cspace-supervisor-loop.sh` respawns it; the process never lingers serving `/health` with a dead agent behind it. If a resumed session fails to start, it retries once with a fresh session (never re-resolving the poisoned resume id) before exiting either way. The loop treats only exit codes `0` and `143` (clean shutdown/SIGTERM) as intentional; `137` (SIGKILL, e.g. the OOM killer) now respawns instead of being mistaken for clean shutdown. Events append to `/sessions/primary/events.ndjson`, single-generation-rotating to `events.ndjson.1` at 10MiB; on (re)start the supervisor resumes the last session id found in the current (unrotated) generation of that log, falling back to a fresh session if it's gone.

### Project sidecars and addressing

Compose services declared by a project (`convex-backend`, a database, …) run as
per-sandbox microVMs named `cspace-<project>-<sandbox>-<service>`, spawned by
`sidecars.Orchestration.Up` during `cspace up`.

**Sandboxes address them by DNS, not by IP.** The daemon resolves
`<service>.<sandbox>.<project>.cspace.test` with a live `container inspect`, so
a sidecar restarting onto a new vmnet IP is transparent — and the same name
resolves on the host through `/etc/resolver/cspace.test`. Compose-style bare
names (`http://convex-backend:3210`) keep working because the sandbox's
`/etc/resolv.conf` carries `search <sandbox>.<project>.cspace.test`; only
single-label names consult the search list (ndots:1), so external lookups are
untouched.

The sandbox's `/etc/hosts` therefore holds **only its own aliases** (its compose
service name and `workspace`). Do not add sidecar entries back: files are
consulted before DNS, so a boot-time IP would win over the correct answer for
the life of the sandbox — that is exactly what stranded an agent in the
2026-08-28 convex-backend incident. Sidecars themselves still get the full
`/etc/hosts` map, because they run their own images with no dnsmasq and glibc
cannot express the daemon's non-standard port in `resolv.conf`.

`cspace sidecar restart <service>` recovers a dead sidecar from either side via
the daemon's `POST /sidecar/restart/{project}/{sandbox}/{service}`, reusing the
browser's escalation ladder (stop → SIGKILL → host-process teardown for Apple
Container's split-brain state → start → wait for an address). It restarts but
does not recreate: the run spec lives in the project's compose file, which only
`cspace up` reads.

### Sandbox runtime (`lib/`)

`lib/` is the source of truth; `make sync-embedded` copies an explicit allowlist into `internal/assets/embedded/` for `go:embed`.

- **templates/Dockerfile** — the sandbox image (Debian-based: Node, Bun, Claude Code, MCP servers, dev tooling). Apple Container's builder does **not** recurse directory COPYs, so files are COPY'd individually — when you add a file under `lib/runtime/scripts/` or `lib/plugins/`, you must also add a COPY line or it silently won't ship (see the per-file-COPY finding).
- **runtime/scripts/** — `cspace-entrypoint.sh` (settings seed, git identity, in-sandbox DNS forwarder, inbound DNAT), `cspace-install-plugins.sh`, `cspace-supervisor-loop.sh`, `statusline.sh`, `cspace-agent-state.sh` (the target of the settings-seeded hooks; writes `/sessions/agent-state.json`, bind-mounted to the host at `~/.cspace/sessions/<project>/<sandbox>/agent-state.json`, so the control plane can show an interactive session's state without asking Claude anything). `lib/runtime/tmux.conf` (cspace's tmux config, passed with `tmux -f` on every session create) lives alongside these but isn't a `.sh` — it needed its own `sync-embedded` rule and Dockerfile COPY, since the existing rule only globs `*.sh`. `set -g extended-keys always` in it is load-bearing: `extended-keys on` silently swallows Shift+Enter in CSI-u form, and only `always` passes it through byte-for-byte.
- **runtime/features/** — optional installers: node, python, git, github-cli, docker-in-docker, common-utils
- **plugins/** — the `cspace-browser` Claude plugin (marketplace + `.mcp.json` wiring for the shared browser sidecar)
- **defaults.json** — embedded config defaults. cspace ships primitives — `up`/`send`/`down`/`browser`, the supervisor; orchestration patterns live in project-side skills such as resume-redux's `delegate-to-containers`.

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

`internal/credentials` owns resolution, policy, and reporting for the five cspace-owned keys — `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, `GITHUB_PERSONAL_ACCESS_TOKEN`. `internal/secrets` holds only primitives (Keychain I/O, host credential discovery) and makes no policy decisions.

Precedence, highest first:

1. `cspace up --env KEY=VALUE`
2. **Project Keychain** — `cspace-<project>-<KEY>`, written by `cspace keychain init --project`. Use it to give one project's sandboxes a narrower token than your personal one; a host `gh` login carries `repo` scope over every repository you can reach.
3. **Global Keychain** — `cspace-<KEY>`, written by `cspace keychain init`. The canonical store on macOS.
4. Ambient host shell
5. Auto-discovery — `gh auth token`, and the host's `claude /login` Keychain entry

There is **no credential file**. `.cspace/secrets.env` was removed, not deprecated — the Keychain is the only durable store, so "where did this value come from" has five possible answers and the boot summary names which one won.

**Compose `env_file` and devcontainer `containerEnv` are ignored for these five keys**, unconditionally — including when cspace resolves nothing. A project's `.env` cannot shadow a cspace credential, and every other key in it flows through untouched. See `docs/env-cspace.md` for the three cases this knowingly breaks.

`cspace up` prints a one-line credential summary before the overlay starts (naming carrier, source, and durability), escalating to a warning when a short-lived credential has under `credentials.runwayWarningHours` (default 4) remaining. Verification runs on the value that actually ships, after every merge.

The two families have opposite policies, declared once in `internal/credentials/groups.go`:

- **Anthropic — Exclusive.** Exactly one carrier ships, routed by the token's own prefix (`sk-ant-oat…` → `CLAUDE_CODE_OAUTH_TOKEN`, `sk-ant-api…` → `ANTHROPIC_API_KEY`). The wrong carrier causes "Invalid API key" and a spurious custom-API-key prompt.
- **GitHub — Mirror.** One value under all three names, because `gh` reads `GH_TOKEN`, the GitHub MCP server reads `GITHUB_PERSONAL_ACCESS_TOKEN`, and Actions-style tooling reads `GITHUB_TOKEN`. The winner is chosen by **source rank, never name order** — name order is what used to overwrite a project's distinct `GITHUB_PERSONAL_ACCESS_TOKEN` with its `GH_TOKEN`.

A GitHub token is verified against `GET /user` at boot; a 401 advances down the candidate stack to the next source. A network failure never advances the ladder, because an unreachable provider is not evidence of a bad token.

**Credentials are baked into the container at create time and never refreshed.** Host-side changes do not reach a running sandbox — `cspace down <name> && cspace up <name>` is the only re-bake path. `cspace doctor`'s "Sandbox credentials" section reads each running sandbox's baked env and reports divergence from current host resolution, which host-side probing alone cannot detect.

## Env plumbing

`docs/env-cspace.md` documents the `.env.cspace` convention (project-declared container overrides), the full env merge order, and the `$CSPACE_WORKSPACE_HOST` / e2e `baseURL` guidance. For **non-credential** keys the order is `--env` > devcontainer `containerEnv` > compose `env_file`. The five cspace-owned credential keys do not participate in that order at all — see Credentials above.

**Terminal color.** `cspace up` bakes `TERM`/`COLORTERM` from the host terminal into the container, and `cspace attach` passes them again per-exec (`TerminalEnv` in `internal/control/argv.go`). Without this, Apple Container's TTY default of a bare `TERM=xterm` with no `COLORTERM` makes Claude paint with 16 colors inside a sandbox while the same terminal gives it 16.7M outside — the PTY strips nothing, the program just picks a smaller palette. `TERM` is mapped to `xterm-256color` unless it already ends in `-256color`, because the sandbox carries Debian's terminfo and an entry it lacks (`xterm-ghostty`) breaks every ncurses program in there. A `dumb` or unset `TERM` is left alone.

Security caveat: secrets currently transit `-e` flags into the substrate, and Apple Container's `vminitd` logs the full process env — anyone with `container logs` access on the host can read them.

## Browser sidecar

The shared per-project sidecar (`cspace-<project>-browser`) has a stable DNS name, `browser.<project>.cspace.test`, served by the host daemon's DNS handler — it survives sidecar restarts, unlike the raw vmnet IP a sandbox used to have baked into its env. `PW_TEST_CONNECT_WS_ENDPOINT` carries this name. The CDP env vars (`CSPACE_BROWSER_CDP_URL`, `PLAYWRIGHT_MCP_CDP_ENDPOINT`) instead carry `http://127.0.0.1:9222` — Chrome's DevTools HTTP endpoint rejects name-based Host headers, so the entrypoint runs a loopback relay that dials the DNS name per connection (same restart-safety, Chrome-acceptable Host). If the sidecar wedges or an agent tears it down, `cspace browser restart` (host-side or in-sandbox, via the daemon's `POST /browser/restart/{project}`) restarts it through an escalation ladder and reverifies liveness with protocol-level probes; `cspace browser status` reports current health without restarting.

**The browser MCP tool prefix differs by session type**, because the servers are registered twice by two different mechanisms. Interactive sessions (`cspace attach`) get them from the `cspace-browser` plugin, so the tools are `mcp__plugin_cspace-browser_cspace-playwright__*` and `…_cspace-chrome-devtools__*`. Headless supervisor sessions (`cspace send`) get them from `claude-runner.ts`, which registers the same two servers directly because the Agent SDK does not auto-load CLI plugin MCP servers — there the tools are `mcp__cspace-playwright__*` and `mcp__cspace-chrome-devtools__*`. Neither is ever `mcp__playwright__*`; a project's instructions naming that are describing the host's MCP config, not a sandbox.

**Navigating to the workspace from the browser sidecar** uses `$CSPACE_WORKSPACE_HOST` (`<sandbox>.<project>.cspace.test`), never `$(hostname)`. The raw hostname is the container name and resolves only inside the sandbox itself — the sidecar is a separate microVM with its own network namespace. See `docs/env-cspace.md`.

## Key patterns

- **Instance naming**: planet names (`mercury`, `venus`, …) with deterministic ports are reserved for the human-facing TUI. Agents spawning sandboxes should use descriptive names (`issue-<n>`, a short task label). `cspace up` refuses a name a container already holds — auto-naming skips taken names via `pickPlanetName`, and explicit names are checked by `ensureSandboxAvailable` right after the substrate health check, before anything else runs.
- **Sessions**: per-sandbox at `~/.cspace/sessions/<project>/<sandbox>/` on the host, bind-mounted into the sandbox; wiped by `cspace down`. Attach bookkeeping is separate and lives at `~/.cspace/controlplane/<project>/<sandbox>/` — the attach lock and one client record per live tmux client (see the **control** package above) — deliberately not under `sessions/`, since it has to outlive nothing but its own client. `cspace down` removes it unconditionally (even with `--keep-state`): once the container is stopped there is no tmux server left for a record to describe.
- **Adding a CLI command**: create `newXxxCmd()` in a new file under `internal/cli/`, register it in `root.go`.
- **Template resolution**: `cspace image build` uses the repo's `lib/templates/Dockerfile` when run from a cspace checkout, otherwise the embedded copy.
- **Stale-image gate**: `cspace up` checks `cspace:latest`'s `cspace.version` label against the running CLI *before* `overlay.Start` (`preflightImageGate`, cmd_up.go). It has to be there — bubbletea holds stdin in raw mode once the overlay is up, so a prompt issued later can never be answered, which is why this used to warn and boot a stale image anyway. Projects that pin their own image (compose `image:`, devcontainer `image:`, a Dockerfile) skip the gate entirely.
- **Reclaiming disk**: every image rebuild leaves the previous one dangling, and Apple Container keeps an unpacked snapshot per image. `container image prune` removes both — measured 2026-08-28 on a dev machine: 65 GB → 41 GB, with snapshots falling 40 GB → 16 GB. It only touches unreferenced images, so it is safe to run with containers up; `--all` additionally drops images no container currently uses (a re-pull next time).

## Security posture (read before relying on it)

- **There is no firewall.** The `firewall.*` config is parsed and merged but no egress filtering is implemented — deliberately tabled for now (agents benefit from web access); see the firewall finding. Never describe sandboxes as network-restricted.
- The entrypoint's inbound DNAT forwards all vmnet TCP to loopback, so "loopback-only" services in a sandbox are reachable by its vmnet peers.
- The supervisor control port binds 0.0.0.0 with bearer-token auth enforced on every route; the supervisor now fails closed (refuses to start) rather than serve unauthenticated if `CSPACE_CONTROL_TOKEN` is empty (production always sets one).
- The `cspace-claude` tmux server's socket inside the sandbox lets anything running as `dev` there — including the supervisor — drive the interactive Claude session (`tmux send-keys -t cspace-claude …`). This crosses no new trust boundary (`dev` already owns everything else in the sandbox), but it is a new capability worth knowing about.

## Commit Style

Short imperative sentences describing what changed and why. Examples from history:
- "Fix EPIPE crash in supervisor and $DC reference in cmd_up"
- "Add incremental commit+push after implement and verify phases"
- "Surface stderr from failed container exec commands"
