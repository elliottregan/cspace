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

Inline prompt and `--prompt-file` are mutually exclusive.

### Description

Creates a dedicated coordinator instance and launches the coordinator agent. The coordinator can manage parallel agents across multiple tasks with dependency tracking and merge ordering.

**How it works:**
1. Provisions a new instance (or reuses one if `--name` matches an existing instance)
2. Loads the `coordinator.md` playbook from `.cspace/agents/coordinator.md` (project override) or the default bundled playbook
3. Builds the full prompt by concatenating the playbook + `USER INSTRUCTIONS:` + the user's prompt
4. Re-copies the host `.env` into the container so the coordinator inherits `GH_TOKEN` and other environment variables
5. Launches the agent supervisor with `--role coordinator`
6. Streams real-time status output to the terminal via `stream-status.sh`

The coordinator's Unix control socket is reachable for mid-run interaction via `cspace send`, `cspace respond`, and `cspace interrupt`.

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
```

---

## One-Shot Agents with `cspace up`

You can run a one-shot autonomous agent using `cspace up` with the `--prompt` or `--prompt-file` flag. This routes the agent through the supervisor (identical to the coordinator path) with messenger MCP tools, structured logging, and socket-based control.

### Syntax

```bash
cspace up [name] --prompt "text"
cspace up [name] --prompt-file <path>
```

### Description

Unlike the interactive mode (plain `cspace up`), the autonomous path:
- Routes through the agent supervisor with `--role agent`
- Enables messenger MCP tools for inter-agent communication
- Creates a Unix control socket for `cspace send`/`cspace respond`/`cspace interrupt`
- Streams NDJSON status events through `stream-status.sh`
- Writes structured logs to the shared logs volume

### Examples

```bash
# One-shot agent with inline prompt
cspace up mercury --prompt "Refactor the auth module to use JWT tokens"

# One-shot agent with prompt from file
cspace up mercury --prompt-file ./tasks/refactor-auth.md

# Auto-named one-shot agent
cspace up --prompt-file ./tasks/fix-tests.md
```
