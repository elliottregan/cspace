---
title: Delegating Container Agents
description: When and how to dispatch autonomous work to isolated cspace devcontainer agents.
sidebar:
  order: 1
---

import { Aside } from '@astrojs/starlight/components';

Container agents let you hand off work to an isolated cspace devcontainer — its own database, its own browser sidecar, its own filesystem. Each container is a full clone of the repo with its own volumes, and `git push` is the only way changes escape.

Docker-outside-Docker (DooD) is enabled, so this works from the host **or** from inside a container — a running coordinator can delegate further work to another coordinator.

## When to use a container agent

Use a container when **at least one** of these is true:

- The task **writes to a stateful backend** (database, queue, cache) and must not collide with the caller's data.
- The task **needs its own browser session** (E2E tests, visual verification, web scraping with state).
- The task is a **long-running autonomous flow** that would block the caller for more than a few minutes.
- The **side effects must be invisible** to the caller until they're ready (risky experiments, speculative migrations).

## When not to use a container agent

Use something lighter when:

- The task **only touches front-end code** (components, CSS, copy) — launch a local subagent or use a git worktree.
- The task is a **file review, audit, or read-only exploration** — use a local subagent.
- The task is a **pure refactor with no stateful side effects** — a git worktree + local subagent is faster.

<Aside type="tip">
State isolation is the load-bearing question. No database writes, no browser? You probably don't need a container.
</Aside>

## Choosing the right primitive

cspace provides two primitives for delegating work to containers:

### `cspace up` — One agent, one scope

Use when the work is small or self-contained, even if it touches several files. Rule of thumb: *"copy changes in a few files, plus some CSS fixes, plus a one-line bugfix"* is still a single `cspace up`.

Ships the prompt directly to one supervisor-backed agent. No coordinator overhead.

```bash
cat > /tmp/work-prompt.txt <<'EOF'
Implement the change described above. Commit and push when done.
EOF
cspace up mars --prompt-file /tmp/work-prompt.txt
```

If you omit the name, the next free planet name is auto-assigned (`mercury`, `venus`, `earth`, `mars`, ...).

The host-side `cspace send` / `cspace respond` / `cspace interrupt` commands all work directly against the named instance.

### `cspace coordinate` — Multi-chunk coordinated work

Use when there's more than one unit of work **and** they need coordination (sequencing, dependency resolution, cross-task aggregation, or unified final review). Examples:

- **A batch of GitHub issues** spanning two or more features — the coordinator resolves dependencies, picks base branches, and merges in order.
- **A large change spec** — one document with multiple implementation chunks. The coordinator breaks it apart and dispatches the pieces.
- **Parallel verification** — several containers running checks where the coordinator compiles results into a single report.
- **Any multi-agent run** that needs a watchdog and final review.

```bash
cat > /tmp/coord-prompt.txt <<'PROMPT'
Work on these GitHub issues, each independently targeting main:
#538, #537, #536, #519
PROMPT
cspace coordinate --prompt-file /tmp/coord-prompt.txt
```

The coordinator reads its playbook from `/opt/cspace/lib/agents/coordinator.md` (or the project's `.cspace/agents/coordinator.md` override). It handles dependency graph resolution, container warming, launching, watchdog monitoring, and final review.

<Aside type="note">
The coordinator is **resumable** — if a run fails, re-invoke with the same `--name` and it picks up where it left off.
</Aside>

## Gotchas

### Always write prompts to a file

`cspace up --prompt "..."` and `cspace coordinate "..."` accept inline strings, but any `$`, backticks, double quotes, or backslashes get shell-expanded before reaching the supervisor. This corrupts the prompt.

**Always use a quoted heredoc and `--prompt-file`**:

```bash
cat > /tmp/delegate-prompt.txt <<'PROMPT'
Implement session token validation using `crypto.timingSafeEqual`.
The existing code is in $CONVEX_DIR — leave it alone and create a new
helper. Add tests for the "empty token" and "wrong length" cases.
PROMPT

cspace up mars --prompt-file /tmp/delegate-prompt.txt
```

The `<<'PROMPT'` (single-quoted heredoc tag) prevents the shell from expanding `$CONVEX_DIR` or interpreting backticks while you write the file.

<Aside type="caution">
Inline strings look convenient but break silently — `$VARIABLE` references get expanded, backtick commands get executed, and the prompt your agent receives is garbled. Always use `--prompt-file`.
</Aside>

### Run dispatched work in the background

Any work you hand off with this skill is long-running. Use `run_in_background: true` on the Bash call with a long timeout (60 minutes is a reasonable default for `cspace coordinate`):

```bash
cspace coordinate --prompt-file /tmp/coord-prompt.txt
```

Do not poll — you will be notified when the command completes. Use the host-side monitoring commands below if you need to interact with the running agents.

## Monitoring running agents

All commands route through the supervisor sockets:

| Command | Purpose |
|---------|---------|
| `cspace list --all` | Show all running instances across every project |
| `cspace ports <name>` | Show port mappings for an instance |
| `cspace ask` | Show pending questions from agents (all instances) |
| `cspace ask <name>` | Show pending questions from one instance |
| `cspace watch [name]` | Stream notifications and questions live |
| `cspace respond <name> <id> "<msg>"` | Answer a pending question |
| `cspace send <name> "<msg>"` | Inject a proactive directive mid-run |
| `cspace interrupt <name>` | Interrupt the tool loop via the supervisor socket |
| `cspace agent-status <name>` | Show supervisor status JSON |
| `cspace ssh <name>` | Shell into an instance (debugging) |

## Cleanup

Containers persist by design — you can reattach to the same instance later with `cspace up <name>` (without `--prompt-file`) or `cspace ssh <name>`. When a batch is genuinely done:

```bash
cspace down <name>           # one instance
cspace down --all            # this project's instances + shared sidecars
cspace down --everywhere     # nuclear: every cspace instance everywhere
```

## Out of scope

Two things that look similar but use different entry points:

- **Container lifecycle only** (no work dispatch) — call `cspace up` / `cspace ssh` / `cspace down` directly without a prompt.
- **Slash command flows** like `/run-issues` and `/run-ready` — they have their own confirmation UX and call `cspace coordinate` internally. See [Running Issues](/skills-and-workflows/running-issues/).
