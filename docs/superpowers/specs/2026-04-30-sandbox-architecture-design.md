# Sandbox Architecture — Design

**Date:** 2026-04-30
**Status:** Approved for prototype (Phase 0); full implementation planning gated on prototype results.
**Supersedes:** Issue #64 (architectural direction); SmolVM evaluation 2026-04-23 (substrate calculus updated by this design).

## Problem

cspace's current model is **one container per agent session**. Every `cspace up` provisions three containers (devcontainer + browser sidecar + search-mcp), inter-agent orchestration is mediated by a shared external Docker volume (`cspace-<project>-logs`), and the project's `.cspace/context/` directory is bind-mounted into every container so agents can read each other's findings in real time.

This works for the coordinator/advisor/implementer fan-out it was built for, but accumulates accidental complexity:

- Four patch releases (v0.10.0 → v0.10.3) in a single afternoon chasing Compose ergonomic failures (`PROJECT_ROOT` collision, `include:` resolution, OrbStack runc race, standalone-devcontainer auto-layering). The pattern: most cspace pain is Compose-the-tool, not Docker-the-engine.
- Per-instance overhead: 4 workers = 12 containers.
- Host-enforced firewall (`init-firewall.sh`) that interactive users disable because it blocks `npm install` postinstalls and ad-hoc web lookups; the autonomous-agent-protection story it was built for has evaporated.
- `.cspace/context/` bind mount creates cross-container coupling that breaks any non-Docker substrate (issue #45) and any cspace-in-cspace invocation.
- Three-way Compose layering (`core` + `project` + `runtime-override`) is the source of every recent ergonomic bug.

Most of cspace's accidental complexity exists to make per-container agents coordinate as if they were peers. The new model collapses the unit of isolation from "one agent" to "one top-level sandbox," with multi-session support inside each sandbox where isolation is not required, and a host-mediated control plane that preserves direct user-turn messaging across sandboxes.

The new model also drops Docker entirely. With the architectural simplifications, the three macOS dealbreakers that anchored the SmolVM evaluation (bidirectional context mounts, VM-to-VM networking, host egress filtering) all dissolve, and the substrate calculus changes. macOS 26 ships Apple Container — a vendor-maintained, OCI-compatible, microVM-per-container runtime — which is a strictly better fit than continuing to fight Docker Compose on a substrate that was never designed for AI-agent sandboxing.

## Solution

A ground-up reorganization of cspace's substrate, isolation model, messaging, and tooling:

- **Substrate:** Apple Container (macOS) + containerd (Linux), behind a `Substrate` interface. Drops Docker engine, Docker Desktop, and OrbStack. Same OCI image works on both backends. Each container = its own Linux microVM on macOS (hardware isolation as a free upgrade); native containerd on Linux.
- **Sandboxes are the unit of isolation.** Top-level sandboxes are the only thing cspace publishes ports for. Each runs one supervisor managing N sessions. Per-implementer isolation (own DB, own Playwright, own env) is preserved by making each implementer a top-level sandbox.
- **Coordinator + advisors collocate.** One sandbox holds coordinator + advisors; they share filesystem and consult each other locally. Implementers stay isolated as their own sandboxes.
- **Messaging is direct user-turn injection.** Inbound control-plane messages land as user turns in the recipient session's live SDK prompt stream. No polling, no queues, no message-check tools. Cross-sandbox traffic routes through the host registry; same `cspace send` UX the agent playbooks already use.
- **Supervisor rewrite.** Bun + TypeScript. `bun build --compile` produces a single binary in the sandbox image; eliminates the in-container `pnpm install` step and adds multi-session management.
- **Push-based activity hub.** Coordinator-sandbox runs an HTTP/WS hub (wraps existing `internal/diagnostics/Hub`); other sandboxes POST event envelopes; host UI subscribes via WS. Filesystem tailer kept as fallback for offline-coordinator replay.
- **Host services for cross-sandbox coordination.**
  - `cspace context-daemon` owns `.cspace/context/` and serves the existing MCP tool surface over HTTP. Resolves issue #45 and removes the bind mount.
  - `cspace registry` (a thin host endpoint) resolves sandbox names → control ports for in-sandbox `cspace` calls.
- **Firewall dropped by default.** Trust model is "agents we wrote, code review, human in the loop." `init-firewall.sh` machinery available but unconfigured; per-project opt-in via `.cspace.json`.

## Topology

Three sandbox roles, each a top-level OCI container running in its own Linux microVM (macOS) or namespace (Linux):

| Role | Examples | Dev port? | Activity port? | Auto-spawned by |
|---|---|---|---|---|
| `interactive` | `mercury`, `venus` | yes | no | explicit `cspace up` |
| `coordinator` | `coordinator-<project>` | no | yes | `cspace coordinate` |
| `autonomous` | `impl-issue-42` | no | no | coordinator's `spawn` |

A project has at most one `coordinator` sandbox at a time. Interactive and autonomous sandboxes are unbounded.

```
Host
├── coordinator-sandbox (microVM)
│   ├── coordinator session (primary)
│   ├── advisor sessions (decision-maker, architect, …)
│   ├── activity HTTP/WS server (published)
│   └── control HTTP server (published)
│
├── impl-issue-42 (microVM)
│   ├── implementer session
│   ├── postgres + chromium + Playwright MCP (per-implementer)
│   └── control HTTP server (published)
│
├── impl-issue-43 (microVM)              ← same shape, fully isolated DB/browser
│   └── ...
│
└── mercury-sandbox (microVM, interactive)
    ├── primary session
    ├── postgres + chromium (mercury-shared)
    ├── N optional nested sessions (no separate DB/Playwright)
    ├── dev.mercury.<project>.cspace.local → published
    └── control HTTP server (published)
```

**Topology rules:**

1. Only top-level sandboxes get published ports (control, activity, dev, preview).
2. `cspace send <sandbox>` is the only cross-sandbox messaging primitive.
3. Implementers always get their own top-level sandbox (per-DB / per-browser isolation requirement).
4. Coordinator + advisors share one sandbox (collocated for fast local consultation; no cross-VM hops on the hot path).
5. Interactive sandboxes can spawn nested sessions for short-lived non-isolated work; nested sessions share the parent sandbox's resources (DB, browser, filesystem).
6. Nested sessions cannot publish to the host. If a nested session produces something worth demoing, the human promotes the work into an interactive top-level sandbox.

## Substrate

### macOS — Apple Container

[github.com/apple/container](https://github.com/apple/container), Apache-2.0, Apple-maintained, GA in macOS 26.

- Each container runs in its own minimal Linux microVM via Virtualization.framework + a custom static Linux kernel.
- OCI-compatible: standard images, including the existing `lib/templates/Dockerfile`, run unchanged.
- No daemon, no Docker Desktop, no OrbStack. The `container` CLI launches/manages VMs directly.
- Boot ~1s warm.
- Networking: each container gets a routable IPv4 on a shared host bridge by default. **(Verify in P0.)** If verified, this potentially simplifies cross-sandbox addressing — sandboxes may be able to reach each other by IP without hostfwd. If networking is more restrictive than this, fall back to per-sandbox hostfwd published on `127.0.0.1:<random>`.

### Linux — containerd

Native containerd via its gRPC API. Same OCI image. No VM needed; Linux processes get namespace-level isolation, which is acceptable for the trust model (autonomous-agent-protection-via-VM is no longer a goal).

### Substrate interface

```go
// internal/substrate/substrate.go
type Substrate interface {
    BuildImage(ctx context.Context, dockerfile, tag string) error
    CreateSandbox(ctx context.Context, spec SandboxSpec) (SandboxHandle, error)
    StartSandbox(ctx context.Context, h SandboxHandle) error
    StopSandbox(ctx context.Context, h SandboxHandle) error
    DestroySandbox(ctx context.Context, h SandboxHandle) error
    Exec(ctx context.Context, h SandboxHandle, cmd []string, opts ExecOpts) (ExecResult, error)
    PortForward(ctx context.Context, h SandboxHandle, hostPort, containerPort int) error
    SandboxIP(ctx context.Context, h SandboxHandle) (net.IP, error)
}
```

Two implementations: `internal/substrate/applecontainer` and `internal/substrate/containerd`. CLI is substrate-agnostic; selection is automatic by GOOS with `--substrate` override for testing.

## Control plane — `cspace send` and friends

Each top-level sandbox publishes a **control port** (HTTP). The control port hosts:

| Endpoint | Purpose |
|---|---|
| `POST /send` | Inject a user turn into a session's prompt stream |
| `POST /exec` | Run a command, stream stdout/stderr |
| `GET  /logs` | Tail or fetch session NDJSON event log |
| `GET  /file?path=` | Read a file from the sandbox |
| `POST /alloc-port` | Allocate a port from the sandbox's reserved range |
| `GET  /sessions` | List sessions and their states |

### Routing

Host CLI maintains a registry at `~/.cspace/sandbox-registry.json` keyed by `<project>:<sandbox-name>`:

```json
{
  "myproject:coordinator-myproject": {
    "control_url": "http://127.0.0.1:6201",
    "control_token": "...",
    "ip": "192.168.64.42",
    "started_at": "2026-04-30T15:00:00Z"
  }
}
```

`cspace send coordinator "..."` resolves from the registry, POSTs `{ session: "primary", text: "..." }` to `<control_url>/send` with the bearer token. Inside the sandbox, the supervisor dispatches to the named session via local Unix socket (`/sessions/<id>/supervisor.sock`).

`cspace send coordinator:decision-maker "..."` targets a specific nested session.

### In-sandbox `cspace` CLI

The `cspace` binary is part of the sandbox image. From inside any sandbox, `cspace exec impl-42 git status` works identically to running it from the host shell:

- The in-sandbox `cspace` binary reads `CSPACE_REGISTRY_URL` (injected at sandbox-create) and `CSPACE_REGISTRY_TOKEN`.
- The host runs a small `cspace registry-daemon` that exposes registry queries over HTTP. Sandboxes resolve names → control URLs through this daemon.
- All `cspace exec/logs/ssh/cp/send` commands work from inside any sandbox.

This preserves today's behavior where coordinator agents drive `cspace` from their bash tool — the UX is unchanged, the transport moves from Docker socket to HTTP control ports.

### Direct user-turn injection (hard requirement)

Inbound control-plane messages are injected as user turns into the live SDK prompt stream. The supervisor maintains an async-queue prompt stream wrapping `query()` from `@anthropic-ai/claude-agent-sdk`; new turns enter the queue and are consumed on the next iteration. Agents never poll a "check messages" tool. The current behavior over Unix socket is preserved over HTTP.

## Activity plane — push to coordinator-sandbox hub

Coordinator-sandbox runs `cspace-activity-server` (new ~200 LoC, wraps existing `internal/diagnostics/Hub`):

| Endpoint | Purpose |
|---|---|
| `POST /events/:sandbox/:session` | Ingest envelopes from sibling sandboxes |
| `GET  /subscribe` (WS) | Fan-out to host UI |
| `GET  /replay/:sandbox/:session?from=<seq>` | Replay from durable log |

Every sandbox's supervisor:

- Writes NDJSON locally to `/sessions/<id>/events.ndjson` (replay + `cspace logs --follow` tailing).
- POSTs each envelope to `$CSPACE_ACTIVITY_URL` (injected from registry at sandbox-create).

If no coordinator-sandbox is running: supervisors buffer to a bounded ring on disk per sandbox (50MB rotating, oldest dropped — loss is acceptable since each sandbox's local NDJSON is authoritative for its own events). Replay on coordinator start.

`internal/diagnostics/tailer.go` is kept through the transition for local-replay fallback; removed in cleanup once push is the only path.

## Cross-sandbox context — host daemon

`cspace context-daemon` runs on the host, **one per project**:

- Owns `.cspace/context/` on the host as the on-disk format (unchanged from today's filesystem layout).
- Listens on `127.0.0.1:<port>`; port + token registered in `~/.cspace/context-daemon-registry.json` keyed by project root.
- Exposes the existing MCP tool surface (`read_context`, `log_decision`, `log_discovery`, `log_finding`, `append_to_finding`, `read_finding`, `list_findings`, `list_entries`, `remove_entry`) over HTTP.
- Sandbox MCP server is rewritten as a thin HTTP client; tool surface unchanged so agent playbooks need no edits.
- Atomic-write semantics: daemon serializes ops via the same O_EXCL primitives today's MCP server uses against the filesystem. No new authority model.
- `.cspace/context/` bind mount **removed** from sandbox provisioning. Resolves issue #45.

**Lifecycle:** hybrid (auto-spawned by `cspace up`, manageable via `cspace context-daemon {start|stop|restart|status}`). Idle threshold: daemon exits after 30 min with no connections **and** no project sandbox running (checked via the sandbox registry).

## Per-sandbox internals

### Supervisor

Bun TS, single binary (`bun build --compile`) shipped at `/usr/local/bin/cspace-supervisor` in the sandbox image:

- Manages N sessions; spawns a Claude Code child per session with its own worktree + env.
- HTTP server on the control port (`Bun.serve`), backing `POST /send`, `POST /exec`, `GET /logs`, etc.
- Local IPC at `/sessions/<id>/supervisor.sock` for in-sandbox dispatch.
- Per-session NDJSON event log at `/sessions/<id>/events.ndjson`.
- POSTs envelopes to `$CSPACE_ACTIVITY_URL` for hub fan-out.
- Re-exposes MCP tools (`agent_health`, `agent_recent_activity`, `read_agent_stream`) scoped per session.
- Single-binary install eliminates `pnpm install` from `entrypoint.sh`. Faster boots.

### Worktrees

Each session lives in `/workspace/.worktrees/<session-id>`, created via `git worktree add` against the sandbox's clone. Primary session uses `/workspace` directly.

### Browser

One chromium-cdp process per sandbox, started lazily on first browser MCP call. Each session that uses the browser gets `--user-data-dir=/sessions/<id>/chrome-profile`. Implementers (one session per implementer-sandbox) effectively get a dedicated browser; mercury's nested sessions share the mercury browser process via separate profile dirs.

### Postgres / DB

Each implementer-sandbox runs its own postgres process, isolated by sandbox boundary. Mercury runs one postgres shared across its primary + nested sessions. Project-side configuration (e.g., Convex) follows the same pattern: per-implementer-sandbox isolated, per-interactive-sandbox shared.

### Per-sandbox port allocation

Each sandbox reserves an internal range (default `30000-30099`, configurable as `sandbox.portRange` in `.cspace.json`). Supervisor hands out ports on `POST /control/alloc-port`, records in `/sessions/<id>/ports.json`, frees on session teardown. Range size 100 covers ~30 concurrent sessions assuming ~3 ports each.

### `cspace ssh` semantics

- `cspace ssh mercury` → fresh shell in `/workspace` (primary session's worktree).
- `cspace ssh mercury --session test-runner` → shell in `/workspace/.worktrees/test-runner`, with `tmux` attach to the supervisor-managed session if one exists.
- `cspace ssh mercury --session test-runner --tail` → tail that session's NDJSON event log.

## Failure recovery

Sandbox lifetime > session lifetime. A session crashing leaves the sandbox running with worktree, postgres state, chromium profile, NDJSON log, and `/sessions/<id>/` workspace intact.

Recovery flows:

- `cspace exec impl-42 git status` to inspect.
- `cspace session resume impl-42:primary` to re-spawn against the existing worktree (Claude Code session JSONL is host-side at `~/.cspace/sessions/<project>/`, survives anyway).
- `cspace exec impl-42 git push` to push manually.
- `cspace ssh impl-42` for an interactive shell.

The supervisor never auto-tears-down a sandbox on session crash — that's an explicit `cspace down` operation. Coordinator and human have the same recovery surface.

## Firewall + trust model

**Drop firewall by default.**

- `lib/scripts/init-firewall.sh` runs but its allowlist is empty; sandbox boots with full egress.
- `.cspace.json` gains `sandbox.firewall: "off" | "allowlist" | "strict"` (default `off`). `allowlist` reads `firewallAllowlist` from config. `strict` is today's hardcoded GitHub/npm/Anthropic allowlist.
- Trust model documented in `docs/trust-model.md`: cspace is a productivity sandbox, not a containment boundary against malicious agent code. Code review and human-in-the-loop are the actual safeguards.
- Per-sandbox override available; coordinator/autonomous sandboxes can opt back into a stricter mode if a project wants tighter rules.

## Naming and CLI surface

| Old | New | Notes |
|---|---|---|
| instance | sandbox | renamed in docs/help; old word still works in CLI args |
| `cspace up <name>` | unchanged | now: start sandbox + primary session |
| `cspace down <name>` | unchanged | stops entire sandbox |
| `cspace ssh <name>` | unchanged + `--session` flag | |
| `cspace send <name> "..."` | unchanged + `<name>:<session>` syntax | |
| `cspace exec <name> ...` | unchanged | now over HTTP control port |
| `cspace logs <name>` | unchanged | now over HTTP control port |
| `cspace coordinate` | unchanged | spawns coordinator + advisors in one sandbox |
| — | `cspace session {new,list,resume,attach}` | new |
| — | `cspace sandbox {list,info}` | new (`stop` is alias for `down`) |
| — | `cspace context-daemon {start,stop,restart,status}` | new |
| — | `cspace registry-daemon {start,stop,status}` | new (host registry endpoint) |

Backward compatibility is **not** preserved. Old running instances are not auto-migrated; users `cspace down`-then-`cspace up` to adopt the new shape per sandbox.

## Out of scope

- Multi-project orchestration (single coordinator-sandbox spanning multiple projects).
- Hardware-isolated agents-running-third-party-code threat model. cspace remains an "agents we wrote on our own repos" tool; if the threat model expands, Apple Container's microVMs are already in place.
- Distributed cspace (sandboxes on remote hosts). Teleport (one session moved to another machine) is the current concession; full multi-host orchestration is out.
- Search stack changes. Code/commits search per #62 stays project-scoped; runs as a sibling process or sibling sandbox depending on resource model — orthogonal to this design.

## Implementation phases

### Phase 0 — Prototype (1–2 weeks)

**Goal:** Prove the dealbreakers before committing to the full architecture. Single-session minimum viable sandbox on Apple Container; no hub, no advisors, no DB, no Playwright, no dev-server publishing.

**Verifies:**

1. **Boot a sandbox on Apple Container.** Build the existing OCI image, `container run` it, exec into it. Time the boot, measure RAM.
2. **Install + run Claude Code inside.** Cold install or pre-baked image. Verify it can authenticate, run a trivial prompt, exit cleanly.
3. **Direct user-turn messaging from host to sandbox.** Minimal Bun supervisor exposes `POST /send`; host `cspace send <sandbox> "..."` injects a user turn that the running Claude session picks up. No polling.
4. **Cross-sandbox messaging.** Two sandboxes boot. From sandbox A, run `cspace send <B> "..."`; verify it lands as a user turn in B's session via the host registry.
5. **Apple Container networking model.** Confirm or refute the "host-routable IP per container" claim. Pick simpler messaging path if confirmed; fall back to hostfwd registry if not.
6. **Linux backend feasibility check.** Spike `containerd` invocation to the same image; do not implement fully — just confirm shape.

**Out of P0:** activity hub, context daemon, multi-session, Playwright, postgres, dev server, friendly URLs, advisors, coordinator, ssh, exec/logs/cp endpoints beyond minimum, firewall, teleport.

**P0 deliverables:**

- `internal/substrate/applecontainer/` minimum viable adapter (just enough for Build/Run/Exec/Stop).
- `lib/agent-supervisor/` rewritten in Bun TS (minimum viable: `POST /send` + spawn Claude Code child + write events.ndjson).
- `cspace prototype-up` and `cspace prototype-send` scratch commands (do not need to integrate into main CLI yet).
- Linux build of the `cspace` binary, embedded into the sandbox image so in-sandbox `cspace send` can reach the host registry-daemon. (cspace itself is Go on macOS host; cross-compile to `linux/arm64` for the sandbox.)
- Written prototype report: what worked, what didn't, P1 risks.

**Gate:** P0 report reviewed before P1 begins. If any of the five dealbreakers fail, this design is revisited.

### Phase 1 — Sandbox + single primary session (1–2 weeks)

After P0 validates the basics:

- Wire the substrate adapter into the main CLI; `cspace up <name>` uses it.
- Sandbox image rebuilt with Bun supervisor as single binary.
- Single-session per sandbox at first (parity with today).
- Sandbox registry (`~/.cspace/sandbox-registry.json`) and host `cspace registry-daemon`.
- Compose, Docker SDK calls, and `lib/templates/docker-compose*.yml` deleted.

### Phase 2 — Multi-session inside sandbox (1–2 weeks)

- Supervisor extended to manage N sessions per sandbox.
- `cspace session {new,list,resume,attach}` commands.
- Per-session worktrees, browser profiles, port allocations, NDJSON logs.
- `cspace coordinate` spawns coordinator + advisors in one sandbox; implementers stay as their own top-level sandboxes (one session each).

### Phase 3 — Push-based activity hub (3–5 days)

- `cmd/cspace-activity-server/` binary in coordinator-sandbox.
- In-sandbox supervisor POSTs envelopes; activity URL injected from registry.
- Bounded ring buffer for offline-coordinator case.
- Host UI subscription via WS.
- Filesystem tailer kept as fallback.

### Phase 4 — Context daemon, drop firewall (1 week)

- `cspace context-daemon` binary; auto-spawn from `cspace up`; `~/.cspace/context-daemon-registry.json`.
- Sandbox MCP rewritten as HTTP client.
- Bind mount removed.
- Firewall drops to `off` default; `sandbox.firewall` config knob added.

### Phase 5 — Linux backend + cleanup (1 week)

- `internal/substrate/containerd/` adapter.
- Remove `internal/diagnostics/tailer.go` once push is the only path.
- Remove `cspace-<project>-logs` volume creation; document one-time `cspace logs migrate` for users with legacy NDJSON.
- Remove single-session supervisor code paths.

**Total estimate:** 6–8 weeks after P0 succeeds.

## Open questions to verify in P0

1. **Apple Container networking — host-routable IPs?** If true, drop hostfwd per sandbox and use direct IP routing. If false, keep hostfwd registry.
2. **Apple Container exec stream semantics.** Does `container exec` give us interleaved stdout/stderr with exit codes cleanly? If not, supervisor wraps it.
3. **macOS 26 Apple Container compatibility with Bun-built single binary.** Bun's compiled output is glibc/musl-Linux ELF; Apple Container runs Linux microVMs, so should be fine — verify in P0.
4. **Boot time for cold image.** Goal: <3s warm, <10s cold. If significantly worse, may need image-size optimization.
5. **RAM overhead per sandbox.** 4 implementers + coordinator + 1 mercury = 6 microVMs. Goal: total system overhead from cspace under 2GB on a 16GB Mac. If higher, reconsider implementer sandbox count or DB/browser strategy.
6. **Linux containerd parity.** Spike-level only in P0. Full implementation in P5.

## Key decisions log

- **Substrate is Apple Container + containerd, not SmolVM.** Apple Container is vendor-maintained, OCI-compatible (existing Dockerfile reusable, no custom rootfs pipeline), and ships GA on the user's macOS 26. SmolVM's blockers from the 2026-04-23 evaluation were dissolved by the new architecture, but maturity gap remains the deciding factor.
- **Implementers are top-level sandboxes, not nested sessions.** The "coordinator + workers in one sandbox" framing from issue #64 was over-promised. Per-implementer DB / browser / env isolation requires sandbox-level boundaries. Coordinator + advisors collocate (they share project state and consult each other; no isolation between them needed).
- **Messaging is direct user-turn injection over HTTP.** No polling, no message-check tools. Same UX as today's Unix-socket `cspace send`; just over HTTP.
- **`cspace` CLI works the same inside any sandbox.** In-sandbox cspace dials the host registry-daemon over HTTP; same `cspace exec/logs/ssh/cp/send` surface for coordinator agents and humans. Today's `docker exec` workflows port directly.
- **Bun TS supervisor with single-binary distribution.** Eliminates `pnpm install` from boot path. Bun's HTTP/WS built-ins drop several npm deps.
- **Backward compatibility is not preserved.** Old running instances do not auto-migrate; users `cspace down` then `cspace up` to adopt the new shape. No legacy compose layering, no DSC volume, no bind mounts.
- **Drop firewall by default.** Trust model is "agents we wrote on our own repos." Code review and human-in-the-loop are the actual safeguards. Machinery available for projects that opt back in.
