---
name: delegating-container-agents
description: >
  Dispatch autonomous work to isolated cspace devcontainer agents when the task
  genuinely requires environment isolation — its own database, browser session,
  filesystem, or a long-running autonomous flow that shouldn't share state with
  the caller. Use for: a batch of GitHub issues spanning multiple features, a
  large refactor split into chunks, parallel browser-based evaluations, or any
  single autonomous task that needs its own backend. Do NOT use for front-end-only
  work, file reviews, refactors, or anything where a local subagent or git
  worktree would work — those are faster and cheaper. Works from anywhere via
  docker-in-docker, so coordinators running inside containers can delegate
  further work to other coordinators.
---

# Delegating Container Agents

Hand work off to an isolated cspace devcontainer when you need real isolation:
its own database, its own browser sidecar, its own filesystem. Each container
is a full clone of the repo with its own volumes; `git push` is the only way
changes escape. Docker-in-docker is enabled, so this skill works from the host
or from inside a container — a running coordinator can delegate further work
to another coordinator.

## Gate: does this task actually need a container?

**Use a container only when at least one of these is true:**

- The task writes to a stateful backend (database, queue, cache) and must not
  collide with the caller's data.
- The task needs its own browser session (E2E tests, visual verification, web
  scraping with state).
- The task is a long-running autonomous flow that would block the caller for
  more than a few minutes.
- The side effects must be invisible to the caller until they're ready (risky
  experiments, speculative migrations).

**Use something lighter when:**

- The task only touches front-end code (components, CSS, copy) → launch a
  local subagent or use a git worktree.
- The task is a file review, audit, or read-only exploration → use a local
  subagent (Explore for search, code-reviewer for review).
- The task is a pure refactor with no stateful side effects → git worktree +
  local subagent is faster.

State isolation is the load-bearing question. No database writes, no browser?
You probably don't need a container.

## Pick the right primitive

### `cspace up <name> --prompt-file <path>` — one agent, one scope

Use when the work is small or self-contained, even if it touches several
things. Rule of thumb: *"copy changes in a few files, plus some CSS fixes,
plus a one-line bugfix"* is still a single `cspace up`.

Ships the prompt directly to one supervisor-backed agent. No coordinator
overhead. The host-side `cspace send` / `cspace respond` / `cspace interrupt`
commands all work directly against the named instance.

```bash
cat > /tmp/work-prompt.txt <<'EOF'
Implement the change described above. Commit and push when done.
EOF
cspace up mars --prompt-file /tmp/work-prompt.txt
```

If you omit the name, the next free planet name is auto-assigned (`mercury`,
`venus`, `earth`, `mars`, …).

### `cspace coordinate "<instructions>"` — multi-chunk coordinated work

Use when there's more than one unit of work **and** they need coordination
(sequencing, dependency resolution, cross-task aggregation, or unified final
review). Examples:

- **A batch of GitHub issues spanning two or more features.** A coordinator
  resolves dependencies, picks base branches, and merges in order.
- **A BRD or large change spec** — one document with multiple implementation
  chunks. The coordinator breaks it apart and dispatches the pieces.
- **Parallel verification** — several containers running checks where a
  coordinator compiles the results into a single report.
- **Any multi-agent run that needs a watchdog and final review.**

The coordinator reads `/opt/cspace/lib/agents/coordinator.md` (or the
project's `.cspace/agents/coordinator.md` override) as its playbook: dependency
graph → warming → launching → watchdog → final review. It is **resumable**
— if a run fails, re-invoke with the same `--name` and it picks up where it
left off.

```bash
cat > /tmp/coord-prompt.txt <<'PROMPT'
Work on these GitHub issues, each independently targeting main:
#538, #537, #536, #519
PROMPT
cspace coordinate --prompt-file /tmp/coord-prompt.txt
```

For non-trivial prompts, **always use `--prompt-file`** to avoid bash quoting
issues.

### Out of scope for this skill

- **Container lifecycle only** (no work dispatch) → call `cspace up` /
  `cspace ssh` / `cspace down` directly without a prompt.
- **Slash command flows** like `/run-issues` and `/run-ready` — they have
  their own confirmation UX and call `cspace coordinate` internally.

## Two gotchas you will hit

### Always write prompts to a file

`cspace up --prompt "..."` and `cspace coordinate "..."` accept inline
strings, but any `$`, backticks, double quotes, or backslashes get
shell-expanded before reaching the supervisor. This corrupts the prompt.

**Always use a quoted heredoc and `--prompt-file`**:

```bash
cat > /tmp/delegate-prompt.txt <<'PROMPT'
Implement session token validation using `crypto.timingSafeEqual`.
The existing code is in $CONVEX_DIR — leave it alone and create a new
helper. Add tests for the "empty token" and "wrong length" cases.
PROMPT

cspace up mars --prompt-file /tmp/delegate-prompt.txt
```

The `<<'PROMPT'` (single-quoted heredoc tag) prevents the shell from
expanding `$CONVEX_DIR` or interpreting backticks while you write the file.

### Run dispatched work in the background

Any work you hand off with this skill is long-running. Use
`run_in_background: true` on the Bash call with a long timeout (60 minutes
is a reasonable default for `cspace coordinate`):

```bash
cspace coordinate --prompt-file /tmp/coord-prompt.txt
```

Do not poll — you will be notified when the command completes. Use the
host-side commands below if you need to interact with the running agents
in the meantime.

## Monitor and interact with running agents

All of these route through the supervisor sockets. Same quoting rules apply
— prefer files for anything non-trivial.

| Command | Purpose |
|---|---|
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

Containers persist by design — you can reattach to the same `mars` later
with `cspace up mars` (without `--prompt-file`) or `cspace ssh mars`. When
a batch is genuinely done:

```bash
cspace down <name>           # one instance
cspace down --all            # this project's instances + shared sidecars
cspace down --everywhere     # nuclear: every cspace instance everywhere
```
