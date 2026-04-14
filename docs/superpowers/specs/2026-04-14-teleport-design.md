# Teleport — Design

**Date:** 2026-04-14
**Status:** Approved for implementation planning

## Problem

A Claude Code conversation running inside a cspace container is bound to that container for its entire life. If the container's environment turns out to be wrong (firewall too tight, wrong base image, degraded state, out of resources) or the container crashes, the only recovery today is starting a fresh instance and losing the in-flight conversation.

Two motivating cases:

- **A. Environment change.** Source container is alive but unsuitable; user wants to continue the same conversation in a new container with different config.
- **D. Recovery.** Source container crashed or was killed; user wants to revive the conversation in a new container.

A fork/branch use case (keep original running, spin up a parallel clone at a decision point) is a possible side-effect but not a driver.

## Solution

A new slash command, `/cspace:teleport <target-instance>`, invocable from inside an in-progress Claude Code conversation. It bundles the current workspace's git state and the session transcript onto a host-shared directory, then invokes `cspace up <target> --teleport-from <dir>` (leveraging existing docker-in-docker) to provision a new container with the workspace seeded and the supervisor configured to resume the same session id.

The source container stops (volumes intact, inspectable). The user reconnects on the host with `cspace resume <target>`.

**Scope (v1):**

- User-initiated only, via the in-container slash command.
- Workspace transferred as a git bundle: all branches, tags, and HEAD travel; **uncommitted working-tree changes and untracked files do not**.
- No host CLI in v1. A host-side `cspace teleport` wrapper can be added later if demand appears.
- No agent-initiated teleport. No periodic background snapshots.

## User-facing surface

One slash command in every cspace container:

```
/cspace:teleport <target-instance>
```

Behavior:

- Runs inside an in-progress Claude Code conversation on the source container.
- On success: source session ends cleanly; `<target-instance>` is running with the workspace and transcript loaded, Claude paused in a resume-ready idle state. Final stdout message: *"Teleport complete. Reconnect with: `cspace resume <target-instance>`"*.
- On any failure before target boot: source container keeps running, nothing on the target has been created, error tells the user what went wrong.
- Source container is left **stopped but intact** (`docker compose stop`, not `down`) after a successful teleport. User runs `cspace rm <source>` when ready.

## What moves

Data that travels from source to target:

1. **Workspace git state** — `git bundle create --all` from `/workspace`. Every branch, tag, and HEAD.
2. **Session transcript** — the JSONL file at `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`, copied verbatim.
3. **Session id** — resolved the same way existing hooks resolve it. Two known mechanisms in-tree today: hooks receive it via stdin JSON (`jq -r '.session_id'` — see `lib/hooks/claude-transcript-copy.sh`), and `/tmp/claude-session-id.txt` is written by an existing hook and read by `lib/hooks/copy-transcript-on-exit.sh`. The teleport script reuses the `/tmp/claude-session-id.txt` file as its primary source, with a fallback of picking the most recently modified `.jsonl` file under `~/.claude/projects/<encoded-cwd>/`. If neither is available, the script aborts.

Data that does **not** travel (by design in v1):

- Uncommitted working-tree changes, untracked files.
- `.env` files, secrets, shell history, Claude Code settings, MCP state, browser session cookies, `node_modules`, `.venv`, build artifacts, etc.
- Supervisor state (inbox, outbox, prior directives).

The target provisions these fresh the same way `cspace up` does today. The agent will effectively wake up with a clean environment minus the git state and conversation memory — exactly like closing a laptop and opening a new one with the same repo checked out.

## Transport

A new host bind mount, `~/.cspace/teleport/`, mounted into every container at `/teleport`. Source writes to `/teleport/<session-id>/`, target reads from `/teleport/<session-id>/` on first boot.

Chosen over a named Docker volume because it's debuggable from the host (user can `ls ~/.cspace/teleport/` when something goes wrong) and symmetric with existing host-path conventions in cspace. Chosen over ad-hoc `docker cp` because it avoids orchestrating multi-step copies and works uniformly whether the source is healthy or degraded.

The directory is cleaned up by `teleport-prepare.sh` on successful target boot; left in place on failure so the user can retry or inspect.

## Mechanism

When a user types `/cspace:teleport mars` inside mercury's conversation:

1. **Slash command body** (`lib/commands/teleport.md`) expands to a short prompt instructing Claude to run `/opt/cspace/bin/teleport-prepare.sh mars`. The slash command contains zero logic — it's a trampoline, so all real work is testable standalone.

2. **`teleport-prepare.sh`** (new, `lib/scripts/`) runs inside the source container:
   - Resolves the session id (primary: `/tmp/claude-session-id.txt`; fallback: newest `.jsonl` under `~/.claude/projects/<encoded-cwd>/`) and the transcript path at `~/.claude/projects/<encoded-cwd>/<session>.jsonl`. Aborts with a clear error if either is missing.
   - `git bundle create /teleport/<session>/workspace.bundle --all` from `/workspace`.
   - `cp <transcript> /teleport/<session>/session.jsonl`.
   - Writes `/teleport/<session>/manifest.json` with `{source, target, session_id, created_at, source_head, source_branch}`.
   - Invokes `cspace up mars --teleport-from /teleport/<session>` as a background job (same pattern the coordinator uses for launching issue agents).
   - Polls `cspace status mars` (or equivalent) until the target reports healthy, with a bounded timeout and an actionable error on timeout.
   - On success: removes `/teleport/<session>/`, issues `cspace stop mercury` (docker-in-docker), prints the reconnect message.

3. **`cspace up --teleport-from <dir>`** (new flag in `internal/cli/up.go`):
   - Skips the normal host-repo bundle step; uses `<dir>/workspace.bundle` as the workspace seed instead.
   - After normal provisioning completes, copies `<dir>/session.jsonl` into the target's `~/.claude/projects/<encoded-cwd>/` path so Claude Code's resume logic can find it.
   - Launches the supervisor with `ResumeSessionID` set to the session id from the manifest.

4. **Supervisor resume** (`lib/agent-supervisor/supervisor.mjs`):
   - New `--resume-session <id>` CLI flag.
   - When set, passes `{ resume: <id> }` into the SDK `query()` options and skips initial prompt injection.
   - The SDK replays the transcript and continues. NDJSON stream, inbox/directive machinery, and existing event logging behave normally from that point.
   - Session comes up *idle* — no auto-prompt, no auto-turn — so the user can reconnect and continue consciously.

## Failure modes

Each failure has a single concrete rule:

| Failure | Behavior |
| --- | --- |
| Bundle step fails | Abort before touching target. Source untouched. Error includes the `git bundle` stderr. |
| `cspace up` fails | Leave `/teleport/<session>/` on disk for manual retry. Source untouched. Error includes the target instance name and transfer dir path. |
| Target boots but resume fails | Leave target up in whatever state it reached (do not auto-delete — user may want to inspect). Source untouched. Error points at the target's supervisor log path. |
| Session id cannot be resolved | Abort with "teleport requires a live Claude session" message. |
| Transcript file missing | Abort; same message shape as above. |
| Target name already in use | `cspace up` already handles this today; surface its error unchanged. |

The invariant is: **if anything fails, the source container is still fully functional.** The user can always retry or diagnose.

## Architecture

### File layout

**Inside-container (agent-facing):**

- `lib/commands/teleport.md` — slash command trampoline, 5–10 lines.
- `lib/scripts/teleport-prepare.sh` — container-side teleport logic. Depends only on `git`, `cp`, `cspace` CLI, and a handful of env vars. Independently testable.

**Host CLI (Go):**

- `internal/cli/up.go` — add `--teleport-from <dir>` flag; route to teleport-aware provisioning path.
- `internal/provision/teleport.go` — new file. Reads the manifest, seeds workspace from the bundle (reusing existing helpers), copies transcript into the target's projects dir, launches supervisor in resume mode.
- `internal/supervisor/launch.go` — add `ResumeSessionID` field to `LaunchParams`; thread it through to the Node supervisor as `--resume-session <id>`. Reuses `RoleAgent`; no new role needed.

**Supervisor (Node):**

- `lib/agent-supervisor/supervisor.mjs` — add `--resume-session <id>` CLI flag. When set, pass `{ resume: <id> }` to the SDK `query()` and skip initial prompt injection. ~20 lines confined to arg-parse and query-construction.

**Embedded assets:**

- `internal/assets/embedded/commands/teleport.md` and `.../scripts/teleport-prepare.sh` are populated by `make sync-embedded`. The existing install step in `init-claude-plugins.sh` already walks `commands/` and `scripts/`, so no new install logic is needed if we follow the naming convention.

**Templates:**

- `lib/templates/docker-compose.core.yml` — add one new bind-mount: `~/.cspace/teleport:/teleport`. No other structural changes.

### Unchanged

- `init-firewall.sh`: teleport traffic is local docker socket, already allowed.
- Hook system: teleport is synchronous on the current turn; no new hook events.
- Inbox/outbox directive machinery: resumed sessions use the target's fresh supervisor; no state migration.

### Component boundaries

Each unit has a clear single purpose:

- `teleport-prepare.sh` — knows source-container filesystem conventions and how to invoke `cspace up`. Nothing else.
- `internal/provision/teleport.go` — knows how to seed a target from a manifest directory. Does not know how the manifest was produced.
- `supervisor.mjs --resume-session` — knows how to pass `resume` to the SDK. Does not know anything about bundles or teleport.

Neither layer reaches into the other's internals. Each can be tested with fixtures that stand in for its neighbors.

## Testing

- **`teleport-prepare.sh`:** shell test driven from Go (`os/exec`) against a fake `$CLAUDE_SESSION_ID`, a fake transcript, and a stubbed `cspace` on PATH. Asserts bundle validity (`git bundle verify`), manifest schema, and correct args to the stubbed `cspace up`.
- **`internal/provision/teleport.go`:** Go unit test with table-driven manifest fixtures and a temp-dir source layout. Asserts workspace seeding and transcript placement. No Docker needed.
- **`internal/cli/up.go` flag plumbing:** existing cobra flag-test pattern.
- **Supervisor resume flag:** Node test asserting `{ resume: <id> }` is set on the SDK options object. SDK itself is mocked.
- **End-to-end:** manual smoke test — `cspace up mercury`, run a real conversation, `/cspace:teleport venus`, confirm `cspace resume venus` continues the conversation. Not part of `make test`.

## One-line recap

Slash command triggers an in-container script that bundles workspace + transcript to a shared host dir, calls `cspace up --teleport-from`, which provisions a new container with the workspace seeded and the supervisor resumed on the same session id. Source stops, user reconnects with `cspace resume <target>`.

## Out of scope for v1

Reserved for follow-up work once v1 is in the wild:

- Host-side `cspace teleport` CLI wrapper.
- Agent-initiated teleport (MCP tool).
- Fork mode (keep source running; teleport as "branch this conversation").
- Periodic bundle snapshots for deeper recovery guarantees.
- Transfer of uncommitted/untracked files.
- Replace-in-place mode (destroy source, reuse name).
