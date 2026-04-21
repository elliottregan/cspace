# Advisor Agents — Design

**Date:** 2026-04-18
**Status:** Approved for implementation planning

## Problem

The coordinator handles orchestration and dispatch; implementers handle the actual work. Both roles occasionally face architecturally significant choices — picking a base branch when dependencies are ambiguous, interpreting an underspecified issue, deciding whether to introduce a new abstraction — that would benefit from deeper reasoning than either role's day-to-day work warrants.

Running the coordinator itself on a max-thinking Opus is expensive and misdirected: most of what the coordinator does (rendering prompts, merging PRs, tracking a dependency graph) is mechanical. And each time an implementer hits a design question, its session has no prior architectural context; it either over-commits to the first plausible answer or burns tokens re-deriving conclusions that were already made.

We want a dedicated reasoning agent that is consulted on hard calls, retains context across consultations, and grounds its answers in the project's stated principles. Beyond the one agent, we want a general mechanism so additional long-running specialist agents (critic, historian, security-reviewer, etc.) can plug in without rearchitecting.

## Solution

A new first-class agent class, **advisors**: long-running `role=agent --persistent` supervisors, each in its own cspace container, auto-launched by `cspace coordinate`, consulted by the coordinator and implementers via the existing socket bus. Advisors are declared in `.cspace.json`; adding a new advisor is a config entry plus an optional system prompt.

The first shipped advisor is the **decision-maker**: Opus + max effort, read-only consulting (no code edits), grounded in `.cspace/context/principles.md`. The coordinator defaults to Sonnet so deep reasoning is concentrated in the advisor, not spent on orchestration.

Inter-agent messaging moves from bash `cspace send` to typed MCP tools on the existing `agent-messenger` in-process server. Tools are role-scoped (coordinators see `ask_advisor`, advisors see `reply_to_coordinator`, etc.) and the recipient name is a schema-level enum populated from `.cspace.json` — no more free-form strings on the hot path.

## Architecture

```
┌─────────────────────┐        ┌─────────────────────────┐
│  coordinator        │        │  decision-maker advisor │
│  (Sonnet, effort    │◄──────►│  (Opus, effort=max,     │
│   high)             │ socket │   --persistent)         │
│                     │   bus  │                         │
│  role=coordinator   │        │  role=agent             │
└──────────┬──────────┘        └────────────┬────────────┘
           │                                ▲
           │ ask_advisor                    │ handshake_advisor
           │ send_to_worker                 │ ask_advisor
           ▼                                │
┌─────────────────────┐                     │
│  implementer(s)     │─────────────────────┘
│  (--persistent)     │
│  role=agent         │
└─────────────────────┘

Arrows above show the new links introduced by this design. The pre-existing
coordinator <-> worker bus (notify_coordinator / send_to_worker / ask_coordinator)
continues unchanged. Advisor <-> worker arrows are bidirectional: advisor
replies via reply_to_worker when a worker used ask_advisor.

All three tiers share /logs/messages/<name>/supervisor.sock via bind mount.
All three tiers share .cspace/context/ via bind mount (cspace-context MCP).
```

### Properties

- **Session continuity.** Advisor supervisors stay alive across turns and across `cspace coordinate` invocations. SDK sessions accumulate project understanding; consultations reuse that context instead of cold-starting.
- **Dedicated containers.** One advisor, one container, one supervisor. Mirrors the existing "one session, one container" convention. Advisor can check out its own branch without disturbing any worker.
- **Typed messaging.** The existing `agent-messenger` MCP server gains role-scoped write tools. Recipient names are an enum from config. Return envelopes include recipient git branch, idle time, queue depth, and an `expected_reply_window` hint.
- **Pluggable via config.** `advisors` block in `.cspace.json`. No Go changes to add a new advisor — just config plus a system prompt file.
- **Principles from context server.** Advisor opinions come from `.cspace/context/principles.md` (human-owned per the context-server spec), read on each consultation. An empty `principles.md` yields generic behavior; a populated one yields opinionated advice.

## Configuration

New `advisors` block in `.cspace.json`, merged through the existing three-layer config loader (`defaults.json` → `.cspace.json` → `.cspace.local.json`).

```json
{
  "advisors": {
    "decision-maker": {
      "model": "claude-opus-4-7",
      "effort": "max",
      "baseBranch": "main"
    }
  }
}
```

Fields:

| Field | Required | Default | Meaning |
|---|---|---|---|
| `model` | no | account default | Any Claude model id. |
| `effort` | no | `max` | `low\|medium\|high\|xhigh\|max\|auto`. |
| `systemPromptFile` | no | (see below) | Explicit project-relative path to the advisor's system prompt. |
| `baseBranch` | no | `main` | Literal branch name. Advisor checks out this branch at provision. |

**System prompt resolution** (mirrors the existing `cfg.ResolveAgent` pattern):

1. If `systemPromptFile` is explicitly set in config, use that path.
2. Else look for a project override at `.cspace/advisors/<name>.md`.
3. Else fall back to the cspace-embedded default at `lib/advisors/<name>.md`.

No config entry needed to pick up the shipped decision-maker prompt.

The `advisors` key is also the enum source for the MCP tools' `name` parameter — the supervisor reads the merged config at startup and encodes the known advisor names into the tool schemas it serves.

`defaults.json` ships with the decision-maker entry above. A project can:
- Delete the advisor entirely by setting `"advisors": {}` in `.cspace.json`.
- Override model/effort while keeping the shipped system prompt.
- Customize behavior by placing `.cspace/advisors/decision-maker.md`.
- Add new advisors by adding config entries and writing `.cspace/advisors/<name>.md`.

## Lifecycle

### Launch

`cspace coordinate` iterates `cfg.Advisors`. For each advisor:

1. Probe the advisor's supervisor socket (`/logs/messages/<name>/supervisor.sock`). If alive, reuse — session continuity preserved.
2. Otherwise: provision the container if absent (`provision.Run`), then `LaunchSupervisor` with:
   - `Role = RoleAgent`
   - `Persistent = true`
   - `Name = <advisor>`
   - Model, effort, system-prompt-file from config
   - `PromptFile = <rendered bootstrap prompt>` (see below)
   - Detached (the coordinator does not block on advisor stdout)
   - Stderr to the existing per-instance log path used for agent roles (`supervisor.ContainerAgentStderrLog`) — no new log conventions.

The coordinator then launches normally. Its rendered prompt includes the advisor roster — names, one-line descriptions, model/effort — so it knows what `ask_advisor(name: ...)` names are valid.

### Persistence across sessions

Ending a `cspace coordinate` session tears down the **coordinator supervisor only**. Advisor supervisors keep running; their containers keep running. The next `cspace coordinate` finds them alive and reuses them, so the advisor's SDK session continues to accumulate context over weeks.

### Teardown

New subcommand group:
- `cspace advisor list` — list configured advisors and their supervisor status.
- `cspace advisor down <name>` — kill the named advisor's supervisor and stop its container. Session state lost.
- `cspace advisor down --all` — tear down every advisor.
- `cspace advisor restart <name>` — kill the supervisor and relaunch with a fresh bootstrap prompt. For when accumulated context has gone stale.

### Branch handling

Advisors sit on their configured `baseBranch` (default `main`). No auto-follow of the coordinator's feature branch, no pre-supervisor checkout hook. If a consultation needs a different branch, the advisor runs `git` itself — it has write access to its own workspace.

### Bootstrap prompt

Rendered into a file and passed via `--prompt-file` on first launch only. On resumed sessions (socket was alive), no bootstrap — the SDK session continues from where it left off.

```
You are the <name> advisor. Your role is defined in your system prompt
(already applied to this session).

Project principles, direction, and decisions live in the cspace-context
server — call read_context at the start of each consultation for current
values.

You will receive messages via the agent-messenger MCP tools. Reply via
reply_to_coordinator / reply_to_worker. See your system prompt for
response format and quality bar.

Do a light read of read_context(["direction","principles","roadmap"])
now so you have baseline context. Then wait for messages.
```

## Inter-agent messaging MCP tools

Extending `lib/agent-supervisor/sdk-mcp-tools.mjs`'s in-process `agent-messenger` server. Role-scoped tool surface: each role sees only the tools that make sense.

| Role | Write tools added |
|---|---|
| Coordinator | `send_to_worker`, `ask_advisor`, `send_to_advisor` |
| Worker (implementer) | `notify_coordinator`, `ask_coordinator`, `handshake_advisor`, `ask_advisor`, `shutdown_self` |
| Advisor | `reply_to_coordinator`, `reply_to_worker`, `note_to_coordinator` |

Read tools (`agent_health`, `read_agent_stream`, etc.) remain role-appropriate as today.

### Recipient enum

For `ask_advisor`, `send_to_advisor`, `handshake_advisor`: the `name` parameter is a schema enum populated from `cfg.Advisors` at supervisor startup. Typos fail schema validation at the model's tool-call layer, before any socket dial.

For `send_to_worker`: `instance` is a free-form string (worker names are dynamic, issue-N style). The tool handles an unreachable socket by returning a structured error (see below).

### Return envelope

Every send tool returns:

```json
{
  "delivered": true,
  "recipient": "decision-maker",
  "recipient_status": {
    "git_branch": "main",
    "turns_completed": 17,
    "idle_for_seconds": 42,
    "queue_depth": 1,
    "session_id": "abc-123"
  },
  "expected_reply_window": "~2-10 min (complex question)",
  "guidance": "Continue your current task. If the reply changes scope, you'll see it as a new user message."
}
```

`expected_reply_window` and `guidance` are hardcoded per tool and `kind` parameter. E.g. `ask_advisor(kind: "question")` returns `"~2-10 min"`; `kind: "handshake"` returns `"no reply expected; advisor will warm its context"`.

### Transport

The tool dials `/logs/messages/<recipient>/supervisor.sock` directly (bind-mounted into every container) and writes two NDJSON commands:

1. `{"cmd": "send_user_message", "text": "<message>"}` — enqueues on the recipient's `PromptQueue`. Same command `cspace send` uses.
2. `{"cmd": "status"}` — pulls recipient metadata for the return envelope.

Both are handled by the existing socket server in `supervisor.mjs`; only `status` needs extending (see below).

### Error surfacing

If the recipient's socket is stale (supervisor crashed, container stopped, wrong name), the tool returns:

```json
{
  "delivered": false,
  "recipient": "decision-maker",
  "error": "recipient's supervisor not reachable at /logs/messages/decision-maker/supervisor.sock",
  "suggestion": "coordinator may need to `cspace advisor restart decision-maker`"
}
```

Structured output — the model can act on it (try a different advisor, retry after a restart) rather than parsing an opaque bash failure.

### Supervisor status extension

The existing `status` socket command in `supervisor.mjs` returns `{role, instance, sessionId, turns, lastActivityMs}`. Extend to also include:

- `git_branch` — `git -C <cwd> rev-parse --abbrev-ref HEAD`, cached with a 2s TTL so rapid back-to-back sends don't fork repeatedly.
- `queue_depth` — `this._queue.length` on `PromptQueue`.

Small change; existing callers continue to work since they ignore unknown fields.

### `shutdown_self`

New socket command. Called by the implementer at Ship phase after `notify_coordinator`. Closes the `PromptQueue` and cleans up, same path as `SIGTERM`. The container keeps running; only the supervisor exits. The coordinator can later `cspace interrupt` or `cspace down` to reclaim resources.

### `cspace send` CLI stays

Still the underlying transport, still useful for humans, scripts, tests, and as an escape hatch if the MCP tools fail. Agent playbooks move to the MCP tools. Deprecation is a later conversation.

## Worker persistence

Workers launched by the coordinator in Phase 2 now pass `--persistent` so their sessions are alive when advisor replies (or coordinator directives) land on their queue as new user turns.

This is a one-line change in the coordinator playbook's `cspace up` invocation. The worker completion flow (coordinator watches for `notify_coordinator` messages) is unchanged.

The Ship phase of `implementer.md` gains a final `shutdown_self` call so the coordinator isn't left with orphaned persistent workers after they've reported done.

## Coordinator playbook changes

### Model default

`runCoordinateWithArgs` in `internal/cli/coordinate.go` sets coordinator model defaults to `claude-sonnet-4-6` and effort `high` unless `cfg.Claude.Model` / `cfg.Claude.Effort` are explicitly set. Existing users who've configured a model keep that model.

### New Phase 0.5 — Advisors

Inserted between the existing Phase 0 (feature branch & dependency graph) and Phase 1 (setup). Content:

> You have a bench of advisors available — long-running specialists you can consult. Use `ask_advisor(name, question, kind)`. The reply arrives later as a new user turn, not as a tool result.
>
> **Roster (rendered at launch):** [name, model/effort, one-line role per configured advisor]
>
> **Consult the decision-maker when:**
> - Picking a base branch or merge ordering when dependencies are ambiguous.
> - Dispatching an implementer for an underspecified issue (multiple valid interpretations).
> - A worker reports a design-level blocker (e.g. "need to introduce abstraction X — proceed?").
> - A PR's diff doesn't cleanly match its acceptance criteria.
> - A prior decision or finding seems to conflict with the current work.
> - Any choice you judge architecturally significant.
>
> **Do NOT consult for:**
> - Routine orchestration (which port, which file, which commit message).
> - Choices the existing playbook already prescribes.
> - Questions that don't touch principles.md or direction.md.
>
> If you're unsure whether a choice qualifies, ask. A tight question is cheaper than the wrong call.

### Worker launch

Phase 2 `cspace up` invocation gains `--persistent`.

## Implementer playbook changes

### Setup phase

After reading the task prompt and `read_context`:

> Call `handshake_advisor("decision-maker", summary, hints)` with a one-line summary of what you're working on and 3-5 file/module hints. This warms the advisor's context for later consultations. Do not wait for a reply — continue to Explore phase.

### Implement / Design phases

> If you hit an architectural choice you can't confidently resolve against principles.md and prior decisions, call `ask_advisor("decision-maker", question, kind: "question")`. The reply arrives later as a new user message — continue working in the meantime on parts of the task that don't depend on the answer. When the reply lands, integrate it and proceed.

### Ship phase

After successful verification and `notify_coordinator("issue-N complete, PR: ...")`:

> Call `shutdown_self()` to release your supervisor. The coordinator will reclaim the container.

## Shipped decision-maker

### `lib/advisors/decision-maker.md` (system prompt)

```
You are a decision-making consultant to the cspace coordinator and
implementer agents. You do not write code. You read, reason, and reply.

## Your job
Weigh architectural trade-offs against the project's stated principles
and direction. When consulted, produce a recommendation with explicit
reasoning.

## On each consultation
1. Call read_context(["direction","principles","roadmap"]) for fresh
   values (humans edit these; your session cache may be stale).
2. Call list_findings(status=["open","acknowledged"]) and read any that
   bear on the question.
3. Call list_entries(kind="decisions") and read any prior decisions that
   touch the same area.
4. Read code as needed — grep, read, follow references.

## Response shape
- Recommendation (one sentence).
- Key reasoning (3-8 bullets, each tied to a principle, constraint, or
  prior decision).
- Alternatives considered and why they lose.
- Follow-ups for the caller if any.

## Record your conclusions
For non-trivial calls, call log_decision(...) so the reasoning survives
beyond your session. The coordinator/implementer reading it later should
be able to act without re-consulting you.

## On handshakes
If the message is a handshake_advisor (an implementer saying "starting
work on X"), do a shallow research pass: read the issue, grep the hinted
files, skim related decisions/findings. Do not reply to the implementer.
Your SDK session now has that context and will be warm for later questions.

The note_to_coordinator tool is available if during research you discover
something the coordinator needs to know right away (a conflict with a
prior decision, a finding that invalidates the issue's premise). Use it
sparingly — the default on handshakes is silence.

## Anti-patterns
- Do not edit code, open PRs, run verify commands, or take side effects
  beyond context-server writes.
- Do not answer questions that aren't architectural — redirect to the
  coordinator.
- Do not speculate past what principles.md and direction.md actually say.
  If they're silent on a question, say so explicitly.
```

### Principles

Live in `.cspace/context/principles.md` (human-owned per the context-server spec). The decision-maker reads them on each consultation. Empty file → generic decision-maker behavior; populated → opinionated advice.

This is the correct place because the user's architectural preferences are **data the project commits and evolves**, not code that cspace ships. A project that inherits the decision-maker gets project-specific advice the moment someone writes their principles down.

## Files

### New

- `lib/advisors/decision-maker.md` — shipped system prompt.
- `internal/cli/advisor.go` — `cspace advisor list|down|restart` subcommand group.
- `internal/advisor/` — Go package for advisor launch/teardown (`Launch`, `IsAlive`, `Teardown`). Mirrors `internal/supervisor` surface but config-driven per-advisor.

### Modified

- `internal/config/` — `Advisors map[string]AdvisorConfig` on `Config`; defaults entry in `defaults.json`.
- `internal/cli/coordinate.go` — iterate `cfg.Advisors`, skip-if-alive, provision-if-missing, launch persistent supervisor detached; Sonnet default for coordinator; render advisor roster into the coordinator's prompt.
- `internal/supervisor/launch.go` — thread advisor config (model/effort/system-prompt) through `LaunchParams`; `Persistent` flag wiring already exists.
- `lib/agent-supervisor/sdk-mcp-tools.mjs` — new role-scoped send/ask tools; socket-direct transport; recipient-enum validation; structured error returns.
- `lib/agent-supervisor/supervisor.mjs` — `status` command adds `git_branch` (2s-cached) and `queue_depth`; new `shutdown_self` handling (graceful prompt-queue close).
- `lib/agents/coordinator.md` — Phase 0.5 (advisors), consult triggers, `--persistent` on worker launch.
- `lib/agents/implementer.md` — handshake on boot, `ask_advisor` mid-task, `shutdown_self` at Ship.
- `CLAUDE.md` — short "Advisors" section pointing at `.cspace/advisors/` and the role of `principles.md`.

## Testing

- Go unit tests for config parsing of `Advisors` block (empty, partial, override, full cases).
- Go unit tests for `internal/advisor` launch-decision logic (alive-reuse vs fresh-provision).
- Node tests for the new MCP tools: recipient-enum validation, socket-dial error surfacing, return-envelope shape. Mock socket server; no live Claude.
- Node test for supervisor `status` response shape (includes `git_branch`, `queue_depth`).
- Node test for `shutdown_self` (prompt queue closes, process exits cleanly).
- One integration test: start a coordinator + decision-maker locally with stub prompts, coordinator calls `ask_advisor`, advisor replies via `reply_to_coordinator`, coordinator receives it as a user turn. Asserts the round-trip envelope and session persistence across two `cspace coordinate` calls.
- No tests for `decision-maker.md` content (it's a prompt).

## Out of scope

- **Additional advisors** beyond decision-maker (critic, historian, security-reviewer). The mechanism supports them; shipping them is separate.
- **Removing `cspace send` CLI.** Stays as the transport and human escape hatch.
- **Queue prioritization** on advisor inboxes (consultations vs. handshakes). FIFO for v1.
- **Cross-project advisor sharing** (one advisor container serving multiple repos). Each project owns its own.
- **UI for advisor visibility** (TUI panel, dashboard). Stream goes to the event log; `cspace ssh <advisor>` or `read_agent_stream` suffice for now.
- **Auto-expiry of stale advisor sessions.** Humans restart them when they want.
- **Editing `principles.md` via MCP.** It's human-owned per the context-server spec; that contract doesn't change here.
