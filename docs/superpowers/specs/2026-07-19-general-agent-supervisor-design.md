# The Supervisor Becomes the General Agent — Design

**Date:** 2026-07-19
**Decisions locked with Elliott:** config = convention file + flag overrides; steering included; specialized-agent residue removed; supervisor is officially KEPT (reverses the earlier removal-candidate status).

## Problem

cspace's headless agent (the Bun supervisor) is already general in shape — a persistent, resumable Claude session bound to `/workspace` with a mailbox (`POST /send`) — but it has no configuration surface (no role, no model choice, hardcoded everything), dishonest liveness (a dead SDK stream leaves a healthy-looking process), and no steering. Meanwhile the codebase still carries residue of the abandoned specialized-agents design (dead config blocks, a dead MCP registration, never-shipped specs) that confuses every agent that reads it.

## Design

### 1. Removal cut (specialized-agent residue)

- Delete unconsumed config: `Advisors`/`AdvisorConfig`/`CoordinatorModel`, and the `advisors`, `agent` (old `issue_label` form), `verify`, `post_setup`, `claude` blocks from structs + `defaults.json`. Unknown keys in existing `.cspace.json` files are ignored by the JSON loader, so downstream projects keep working unmodified.
- Delete the root `.mcp.json` registration of `cspace context-server` (command does not exist).
- Move never-shipped/superseded specs (advisor agents, coordinator orchestration, context-MCP-server contract) to `docs/superpowers/specs/archive/` with a one-line header note each.
- Rename `internal/orchestrator` → `internal/sidecars` (it is compose-sidecar lifecycle, not agent orchestration; the name collision has caused real confusion). Mechanical rename, no behavior change.
- CLAUDE.md: replace the vestigial-keys caveat with the model statement — *cspace ships primitives (`up`/`send`/`down`/`browser`, the supervisor); orchestration patterns live in project-side skills* (e.g. resume-redux's `delegate-to-containers`).

### 2. Configuration surface (convention over configuration)

Nothing configured → exactly today's behavior.

- **Role**: if `.cspace/agent.md` exists in the workspace clone, its content is **appended** to the system prompt (SDK append-style option — never replaces Claude Code's own system prompt, so tool behavior and safety framing stay intact; exact SDK option name pinned at implementation against the pinned `@anthropic-ai/claude-agent-sdk` version). One-off override: `cspace up --role <host-path>`; cspace resolves the file at up-time and writes its content to `/sessions/agent-role.md` (the session dir is host-bind-mounted, so no env transport, no size limits, restart-safe). Supervisor resolution order: `/sessions/agent-role.md` (explicit override) → `/workspace/.cspace/agent.md` (committed convention) → none.
- **Model**: `.cspace.json` `agent.model` (a NEW, consumed block — distinct from the deleted dead one) with `cspace up --model <m>` override; delivered as `CSPACE_AGENT_MODEL` env, passed to the SDK's `model` option when non-empty.
- **Project settings**: the supervisor asks the SDK to load project-level settings from `cwd` (CLAUDE.md / `.claude/settings`) so the headless agent sees the same project instructions an interactive session does. Exact SDK mechanism (`settingSources` or equivalent) verified at implementation; the contract is: *the workspace's CLAUDE.md reaches the agent*.
- Explicitly not added: per-sandbox MCP server config (the browser pair stays hardwired; projects already add MCP servers via their own `.mcp.json` once settings loading lands), permission-mode knobs (unattended stays `bypassPermissions`).

### 3. Liveness (resolves the deferred supervisor findings)

1. **Stream death exits**: `runClaude(...)` rejection no longer just logs — the process logs `sdk-error` and exits non-zero, so the restart loop actually restarts the agent.
2. **Resume poisoning fixed**: if a resumed `query()` fails at startup, retry ONCE without `resume` (fresh session), logging a `resume-failed` event. A stale session id can no longer wedge every future restart.
3. **OOM respawns**: `cspace-supervisor-loop.sh` stops treating exit 137 as intentional shutdown — 0 and 143 remain the clean exits; 137 (OOM SIGKILL) respawns like any crash.
4. **Auth fails closed**: empty `CSPACE_CONTROL_TOKEN` → the supervisor refuses to start serving (fatal, logged) instead of serving unauthenticated on `0.0.0.0`.
5. **Event-log rotation**: `events.ndjson` rotates at 10 MiB to `events.ndjson.1` (single generation, mirroring the daemon-log pattern). Resume scans only the current file; a resume id lost to rotation degrades to a fresh session — acceptable and logged.

### 4. Steering

- **`POST /interrupt`** (token-authed like `/send`): calls the SDK query handle's `interrupt()` — stops the current task, conversation persists, next `/send` continues it. 200 `{ok:true}`; 409 if no task is running (best-effort — the SDK call is idempotent).
- **`GET /status`** (token-authed): `{ok, session, state, lastEventTs, lastEventType, queueDepth}` where `state` is derived: `working` if the last SDK event is newer than the last result/idle marker, else `idle`; `queueDepth` = prompts queued but not yet consumed.
- **CLI**: new `cspace agent status|interrupt <sandbox>` group (mirrors `cspace browser`'s host + in-sandbox dual-context pattern, resolving control URL + token via the registry exactly like `cspace send`).

### 5. Security posture

- Token becomes mandatory (fail-closed) — closes the fail-open finding item. The `/lookup` token-exposure finding is unchanged and stays a separate follow-up; this spec neither widens nor narrows it.
- Role files are not secrets; they transit the sessions bind-mount, not env/argv.

## Testing

- Supervisor: `bun test` unit coverage for the pure parts — PromptStream (exists behaviorally today, now pinned), resume-id scanning incl. rotation edge, role-file resolution order, status-state derivation. No live SDK calls; `runClaude` is faked at its seam.
- Go: flag/env plumbing (`--role`/`--model` → env + `/sessions/agent-role.md` write), `cspace agent` CLI routing (httptest fake control server, both contexts), rename fallout compile-clean.
- Standard constraints: every `go test ./internal/cli` uses `-skip 'TestCspaceLifecycle'`; no live containers in the default suite; live verification happens post-release in a disposable sandbox (role file honored, interrupt stops a running task, kill -9 of the supervisor respawns and resumes).

## Non-goals

- Multi-session per sandbox (one sandbox = one agent = one conversation).
- Outbound messaging machinery — in-sandbox agents already reply via `cspace send`.
- `/lookup` auth tightening; firewall; boot-stall fix (`2026-07-19-plugins-marketplace-add…`) — separate items, though the boot-stall fix is a natural same-release passenger.
- Renaming "supervisor" in code/paths beyond docs framing.

## Rollout

rc.39: binary + image rebuild required (supervisor and loop script ship in the image). Existing sandboxes keep old behavior until recreated. Findings resolved by this work: `2026-07-16-supervisor-silent-death-modes-and-fail-open-auth` (all four numbered defects); memory note updated: supervisor is kept as the general-agent primitive.
