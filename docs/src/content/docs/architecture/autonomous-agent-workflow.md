---
title: Autonomous Agent Workflow
description: The 7-phase workflow that cspace autonomous agents follow to resolve GitHub issues end-to-end.
sidebar:
  order: 2
---

When you run `cspace issue 42`, cspace provisions a devcontainer instance and launches an autonomous Claude agent inside it. The agent follows a 7-phase workflow defined in the implementer playbook (`lib/agents/implementer.md`) to resolve the issue end-to-end — from reading the issue through shipping a tested PR.

The agent is fully autonomous: there is no human in the loop. It makes all decisions itself, does not wait for approvals, and is expected to ship a complete, tested pull request.

## The 7 phases

### Phase 1 — Setup

The agent reads the GitHub issue and creates the development branch:

1. `gh issue view <number>` — read the issue description and acceptance criteria
2. Create a branch from the base: `git checkout -b issue-<number> origin/<base-branch>`
3. Push the branch with an empty initial commit
4. Open a draft PR linking to the issue: `gh pr create --draft --base <base-branch>`

The base branch is typically `main`, but the coordinator can set it to a feature branch or another issue branch when managing dependencies.

### Phase 2 — Codebase exploration

**Goal**: Build deep understanding of the relevant code before designing a solution.

The agent launches 2–3 parallel code-explorer sub-agents, each targeting a different aspect of the codebase:

- **Similar features** — find existing code that does something comparable
- **Architecture** — understand the high-level structure, abstractions, and control flow
- **User experience** — trace the feature from the user's perspective

Each explorer identifies 5–10 key files. Once all explorers return, the agent reads every identified file to build comprehensive understanding before moving to design.

### Phase 3 — Architecture design

**Goal**: Design multiple implementation approaches with explicit trade-offs.

The agent launches 2–3 parallel code-architect sub-agents, each with a different focus:

| Approach | Focus |
|----------|-------|
| **Minimal changes** | Smallest diff, maximum reuse of existing patterns |
| **Clean architecture** | Maintainability, elegant abstractions, future extensibility |
| **Pragmatic balance** | Speed and quality — good enough architecture shipped fast |

The agent reviews all approaches and selects the one that best fits the specific task. This avoids the common failure mode of jumping to the first idea without considering alternatives.

### Phase 4 — Implement

The agent creates an implementation plan and writes the code. If it encounters work that is important but out of scope for the current issue, it creates a new GitHub issue for that work and continues with the original task.

### Phase 5 — Verify

The agent runs the project's configured verification commands:

1. **Lint, typecheck, and tests**: The command from `verify.all` in `.cspace.json`
2. **E2E tests**: The command from `verify.e2e` in `.cspace.json`

If any checks fail, the agent fixes the issues and re-runs verification until everything passes.

> **Shell rule**: Long-running commands like E2E tests must never be piped through `tail` or `head`. These commands buffer their output, so piping them blocks until the entire command finishes. Instead, the agent redirects output to a file and reads it afterward: `cmd > /tmp/output.log 2>&1 && tail -40 /tmp/output.log`.

### Phase 6 — Ship

1. Commit all changes with a message that includes `Closes #<number>`
2. Push the branch: `git push`
3. Mark the PR as ready: `gh pr ready`

### Phase 7 — Review

The agent performs its own review before considering the task done:

1. **Screenshots** — uses Playwright MCP browser tools to capture screenshots of new or changed features from the running preview server
2. **Code review** — runs a self-review pass on the PR diff, fixing any issues found
3. **AC verification** — re-reads the issue and compares every acceptance criterion against the actual changes, going back to implement anything missing

## How the supervisor manages sessions

The agent doesn't run Claude Code directly. Instead, it runs inside a **supervisor** process (`lib/agent-supervisor/supervisor.mjs`) — a long-lived wrapper around the Claude Agent SDK that enables mid-session communication.

### Streaming input model

The supervisor uses the SDK's streaming-input mode with a queue-backed async iterable:

1. The initial prompt (the rendered implementer playbook) is pushed to a `PromptQueue`
2. The SDK's `query()` function consumes the queue as an async iterator
3. External commands (from the coordinator or host) can push additional user turns into the queue while the agent is running

This allows the coordinator to send directives mid-task — for example, "rebase onto the latest feature branch" or "the requirements changed, also add X" — without restarting the session.

### Control socket

Each supervisor listens on a Unix domain socket for host-side commands:

| Command | Behavior |
|---------|----------|
| `send_user_message` | Injects a new user turn into the running session |
| `respond_to_question` | Answers a question the agent asked via `ask_orchestrator` |
| `interrupt` | Gracefully cancels the current query |
| `status` | Returns session ID, turn count, idle time |
| `shutdown` | Closes the prompt queue and exits cleanly |

Socket path: `/logs/messages/{instance}/supervisor.sock`

### MCP tools for communication

The supervisor provides in-process MCP tools for agent-coordinator communication:

- **`ask_orchestrator`** — blocks until the coordinator responds (up to 10 minutes). Used for genuinely ambiguous decisions with significant trade-offs.
- **`notify_orchestrator`** — fire-and-forget status update. Used for milestones like "branch created", "PR opened", "tests passing".

Directives from the coordinator arrive as new user turns in the conversation — the agent does not need to poll for them.

### Event logging

The supervisor writes structured NDJSON event logs to `/logs/events/{instance}/`:

```
session-2026-04-10T12-00-00-000Z-<session-id>.ndjson
```

Each line is a self-describing envelope:

```json
{
  "ts": "2026-04-10T12:00:00.000Z",
  "instance": "mercury",
  "role": "agent",
  "sdk": { ... }
}
```

### Completion notifications

When an agent finishes (success or failure), the supervisor writes a completion notification to the coordinator's inbox at `/logs/messages/_coordinator/inbox/`:

```json
{
  "type": "completion",
  "instance": "mercury",
  "status": "success",
  "exitCode": 0,
  "sessionId": "abc-123",
  "turns": 47,
  "durationMs": 180000,
  "costUsd": 12.50,
  "summary": "Implemented the login page..."
}
```

The coordinator watches this inbox directory and receives completion notifications as automatic user turns — no polling required.

### Idle watchdog

If the SDK emits no events for 10 minutes (configurable via `--idle-timeout-ms`), the supervisor assumes the agent is stuck — typically on a hung MCP tool call (e.g., a crashed browser sidecar). It calls `interrupt()` on the query to unwind gracefully, and the agent exits with an `idle_timeout` status so the coordinator can decide whether to retry.

### Error recovery

The agent system prompt instructs agents to handle errors pragmatically:

- **Persistent tool errors** — investigate briefly (2–3 attempts), then escalate via `ask_orchestrator` or exit cleanly with a diagnostic summary
- **Environmental failures** (MCP unreachable, browser hung, repeated identical errors) — do not retry indefinitely
- **Final message** — always include enough diagnostic context for the coordinator to decide next steps

The coordinator sees the agent's final message in the completion notification and can restart the agent in a fresh session if needed.
