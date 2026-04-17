---
title: Autonomous Agents
description: Commands for running autonomous Claude agents and multi-task coordinators.
sidebar:
  order: 3
---

Commands for running autonomous Claude agents against free-text prompts and coordinating multi-agent workflows.

## `cspace coordinate`

Launch a multi-task coordinator agent.

### Syntax

```bash
cspace coordinate "<instructions>" [flags]
cspace coordinate --prompt-file <path> [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `--name <name>` | Use a specific instance name (makes it resumable). Default: `coord-{timestamp}` |
| `--prompt-file <path>` | Load the prompt from a file instead of inline |
| `--system-prompt-file <path>` | Override the coordinator system prompt AND skip the `coordinator.md` playbook. Use this to run ad-hoc tasks (chat, code review, bespoke automation) in the coordinator's persistent session without inheriting orchestration framing. |

Inline prompt and `--prompt-file` are mutually exclusive.

### Description

Creates a dedicated coordinator instance and launches the coordinator agent. The coordinator can manage parallel agents across multiple tasks with dependency tracking and merge ordering.

**How it works:**
1. Provisions a new instance (or reuses one if `--name` matches an existing instance)
2. Loads the `coordinator.md` playbook from `.cspace/agents/coordinator.md` (project override) or the default bundled playbook
3. Builds the full prompt by concatenating the playbook + `USER INSTRUCTIONS:` + the user's prompt
4. Re-copies the host `.env` into the container so the coordinator inherits `GH_TOKEN` and other environment variables
5. Launches the agent supervisor with `--role coordinator`
6. Streams real-time status output to the terminal

The coordinator's Unix control socket is reachable for mid-run interaction via `cspace send _coordinator` and `cspace interrupt _coordinator`. Only one coordinator can run per project — attempting to start a second will fail with an error.

### Examples

```bash
# Inline instructions
cspace coordinate "Implement issues #10, #11, and #12 in parallel"

# Load prompt from file
cspace coordinate --prompt-file ./tasks/sprint-plan.md

# Use a specific name for resumability
cspace coordinate "Fix all lint errors" --name lint-cleanup

# Named coordinator with prompt file
cspace coordinate --prompt-file ./tasks/roadmap.md --name q2-roadmap

# Ad-hoc coordinator with a custom identity — skip the orchestration playbook
cat > /tmp/review-agent.txt <<'EOF'
You are a code-review assistant. When the user sends a PR URL, fetch the
diff with `gh pr diff` and reply with a concise review. Stay alive
between messages.
EOF
cspace coordinate --system-prompt-file /tmp/review-agent.txt --name reviewer \
  "Ready to review PRs — paste a URL."
```

---

## One-Shot Agents with `cspace up`

Run a one-shot autonomous agent using `cspace up` with `--prompt` or `--prompt-file`. This routes through the supervisor (identical to the coordinator path) with messenger MCP tools, structured logging, and socket-based control.

### Syntax

```bash
cspace up [name] --prompt "text"
cspace up [name] --prompt-file <path>
```

### Description

Unlike the interactive mode (plain `cspace up`), the autonomous path:
- Routes through the agent supervisor with `--role agent`
- Creates a Unix control socket for `cspace send`/`cspace interrupt`
- Streams NDJSON status events to the Go CLI for rendering
- Writes structured event logs to the shared logs volume
- On completion, reports to the coordinator (if running) via `cspace send _coordinator`

### Examples

```bash
# One-shot agent with inline prompt
cspace up mercury --prompt "Refactor the auth module to use JWT tokens"

# One-shot agent with prompt from file
cspace up mercury --prompt-file ./tasks/refactor-auth.md

# Auto-named one-shot agent
cspace up --prompt-file ./tasks/fix-tests.md
```

---

## Persistent Agents with `cspace up --persistent`

One-shot agents exit after their first result. Adding `--persistent` keeps the supervisor's prompt queue open so `cspace send <instance> "..."` can drive multi-turn conversations on the agent's instance-scoped socket.

Unlike the coordinator — which is a singleton (one per project, fixed `_coordinator` socket name, orchestration playbook) — persistent agents are:

- **Named** — each gets its own socket at `/logs/messages/<instance>/supervisor.sock`
- **Concurrent** — N agents can run side-by-side (`venus`, `mars`, `earth`…)
- **Blank** — no playbook framing; the prompt you provide is the whole setup

### Syntax

```bash
cspace up <name> --persistent --prompt "text"
cspace up <name> --persistent --prompt-file <path>
```

`--persistent` requires an initial `--prompt` or `--prompt-file` (it has to know what to do with the first turn).

### Examples

```bash
# Start an always-on side agent
cspace up venus --persistent --prompt \
  "You're my side agent. Reply briefly to each message I send."

# Drive it from anywhere
cspace send venus "Find the bug in src/auth/session.ts"
cspace send venus "Now write a regression test for it"

# Sanity-check and control
cspace agent-status venus    # last activity, turn count
cspace interrupt venus       # stop a rambling response
cspace down venus            # tear down when done
```

### Lifecycle

- The session exits when no messages arrive for `idleTimeoutMs` (default 10 minutes). Each incoming `cspace send` resets the timer.
- `SIGTERM` / `SIGINT` / `cspace down` cleanly shut it down.
- Responses stream to the terminal that ran `cspace up` — run it in a dedicated window, or launch with output redirected for background use.

### When to use which

| Use case | Command |
|---|---|
| Solve a single well-defined task and exit | `cspace up <name> --prompt "…"` |
| Keep one agent around for ad-hoc back-and-forth | `cspace up <name> --persistent --prompt "…"` |
| Orchestrate multiple agents against a set of GitHub issues | `cspace coordinate "…"` |
| Run a one-off custom task in a persistent session (chat, review bot, bespoke automation) | `cspace coordinate --system-prompt-file <path> "…"` |

## Configuring the agent's model and effort

Autonomous runs use the Claude model and reasoning effort declared in `.cspace.json`:

```json title=".cspace.json"
{
  "claude": {
    "model": "opus[1m]",
    "effort": "max"
  }
}
```

Defaults are `opus[1m]` (latest Opus + 1M context) and `max` effort — suitable for long, unsupervised tasks. cspace passes these to the supervisor as `--model` / `--effort` flags, which map to SDK `options.model` / `options.effort` and apply only to the autonomous query.

These settings do **not** affect interactive `claude` sessions — those follow project `.claude/settings.json`. See [Configuration reference](/configuration/configuration-reference/#claude) for the full precedence rules and accepted values.
