---
title: CLI Reference
description: Complete reference for all cspace commands, grouped by category.
sidebar:
  order: 1
---

cspace provides a comprehensive set of commands for managing devcontainer instances, running autonomous agents, and coordinating multi-agent workflows.

## Command Summary

### Instance Management

| Command | Description |
|---------|-------------|
| [`cspace up`](/cli-reference/instance-management/#cspace-up) | Create/reconnect instance and launch Claude Code |
| [`cspace ssh`](/cli-reference/instance-management/#cspace-ssh) | Shell into a running instance |
| [`cspace list`](/cli-reference/instance-management/#cspace-list) | List running instances |
| [`cspace ports`](/cli-reference/instance-management/#cspace-ports) | Show port mappings for an instance |
| [`cspace down`](/cli-reference/instance-management/#cspace-down) | Destroy instance and volumes |
| [`cspace warm`](/cli-reference/instance-management/#cspace-warm) | Pre-provision containers without launching Claude |
| [`cspace rebuild`](/cli-reference/instance-management/#cspace-rebuild) | Rebuild the container image (with `--reindex` to also refresh search on every running instance) |

### Autonomous Agents

| Command | Description |
|---------|-------------|
| [`cspace coordinate`](/cli-reference/autonomous-agents/#cspace-coordinate) | Launch a multi-task coordinator agent |
| [`cspace up --prompt-file`](/cli-reference/autonomous-agents/#one-shot-agents-with-cspace-up) | Run a one-shot agent with a free-text prompt |

### Supervisor Commands

| Command | Description |
|---------|-------------|
| [`cspace send`](/cli-reference/supervisor-commands/#cspace-send) | Inject a user message into a running agent session |
| [`cspace send _coordinator`](/cli-reference/supervisor-commands/#cspace-send) | Send a message to the coordinator |
| [`cspace interrupt`](/cli-reference/supervisor-commands/#cspace-interrupt) | Interrupt a running agent session |
| [`cspace agent-status`](/cli-reference/supervisor-commands/#cspace-agent-status) | Show supervisor status as JSON |
| [`cspace restart-supervisor`](/cli-reference/supervisor-commands/#cspace-restart-supervisor) | Restart agent supervisor (preserves workspace) |

### Project Setup

| Command | Description |
|---------|-------------|
| [`cspace init`](/cli-reference/project-setup/#cspace-init) | Initialize cspace project configuration |
| [`cspace self-update`](/cli-reference/project-setup/#cspace-self-update) | Update cspace to the latest version |

### Semantic Search

| Command | Description |
|---------|-------------|
| `cspace search init` | Bootstrap search for a project: writes `search.yaml`, installs lefthook hooks, runs initial indexes. |
| `cspace search code "<query>"` | Natural-language search over the code corpus. |
| `cspace search commits "<query>"` | Same, against the commit history corpus. |
| `cspace search context "<query>"` | Same, against the `.cspace/context/` planning artifacts. |
| `cspace search issues "<query>"` | Same, against the repo's GitHub issues and PRs. |
| `cspace search {code,commits,context,issues} index` | (Re)build the named index. Auto-runs on commit via lefthook if installed. |
| `cspace search {code,commits,context,issues} clusters` | Discover thematic clusters and write `cluster_id` into the index. |
| `cspace search mcp` | Stdio MCP server exposing all four corpora as tools (launched by Claude via `.mcp.json`, not invoked directly). |

See [Semantic Search](/features/semantic-search/) for a conceptual overview, corpus selection guidance, and MCP tool contracts.

### Other

| Command | Description |
|---------|-------------|
| `cspace` | Interactive TUI menu (requires `gum`) |
| `cspace version` | Show cspace version |
| `cspace help` | Show help text |
| `cspace context-server` | Stdio MCP server exposing `.cspace/context/` (launched by Claude via `.mcp.json`, not invoked directly). See [Project Context](/architecture/project-context/). |
