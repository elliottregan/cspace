# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**cspace** is a CLI for managing isolated Claude Code devcontainer instances. It spins up Docker containers with independent workspaces, browser sidecars, and network firewalls, then runs autonomous Claude agents against GitHub issues. Written in Bash (CLI) with a Node.js agent supervisor component.

## Development

There is no build system, test suite, or linter. The CLI is plain Bash sourcing library files; the agent supervisor is Node.js ESM. Development is done by editing files directly and testing via `cspace up`.

```bash
# Run cspace locally (set CSPACE_HOME to this repo)
export CSPACE_HOME=/path/to/this/repo
./bin/cspace --help

# Rebuild container image after Dockerfile/template changes
cspace rebuild

# Launch a test instance
cspace up
cspace ssh mercury

# Agent supervisor dependencies (inside container or for local dev)
cd lib/agent-supervisor && pnpm install
```

## Architecture

### CLI (`bin/cspace`)

Single Bash script (~800 lines) that dispatches subcommands via `cmd_<name>()` functions. Sources five core libraries from `lib/core/`:

- **config.sh** — Three-layer config merging: `lib/defaults.json` -> `.cspace.json` -> `.cspace.local.json`
- **instance.sh** — Container lifecycle (create, destroy, exec, health checks)
- **compose.sh** — Docker Compose file resolution and template overrides
- **ports.sh** — Deterministic port assignment using planet names (mercury=1, venus=2, etc.)
- **supervisor.sh** — Launches the Node.js agent supervisor, handles restart logic and stream piping

### Agent Supervisor (`lib/agent-supervisor/`)

Node.js process (ESM) that wraps the Claude Agent SDK's `query()` with:
- An async-queue-backed prompt stream for injecting user turns mid-session
- A Unix socket server (`/logs/messages/{instance}/supervisor.sock`) for host->container commands (`send`, `respond`, `interrupt`)
- NDJSON event streaming to stdout, consumed by `stream-status.sh` for terminal rendering
- MCP tools for inter-agent communication (`ask_orchestrator`, `notify_orchestrator` for agents; `list_agent_questions`, `respond_to_agent`, `send_directive` for coordinators)

Key dependency: `@anthropic-ai/claude-agent-sdk`

### Container Environment (`lib/scripts/`, `lib/templates/`)

- **Dockerfile** — Alpine + Node + Claude Code + SSH + Docker-in-Docker
- **docker-compose.core.yml** — Devcontainer + Playwright run-server + Chromium CDP sidecar
- **entrypoint.sh** — Container init: firewall, plugins, workspace setup
- **setup-instance.sh** — Host-side provisioning: git bundle, port assignment, compose up, copy repo into container
- **init-firewall.sh** — iptables allowlist (GitHub, npm, Anthropic + custom domains)
- **init-claude-plugins.sh** — Writes Claude settings.json, hooks config, MCP servers

### Agent Playbooks (`lib/agents/`)

- **implementer.md** — 7-phase autonomous workflow: Setup -> Explore -> Design -> Implement -> Verify -> Ship -> Review
- **coordinator.md** — Multi-agent orchestration with dependency graph resolution and base branch chaining

### Hooks (`lib/hooks/`)

Shell scripts fired by Claude Code's hook system: progress logging on `PostToolUse`, transcript archival on `SessionEnd`.

## Key Patterns

**Config merging**: All config flows through `load_config()` which merges JSON with `jq` in precedence order. Access merged values via `cfg_get <key>`.

**Template overrides**: Users place files in `.cspace/` to override built-in templates (Dockerfile, compose files, agent playbooks). Resolution checks project dir first, falls back to `$CSPACE_HOME/lib/templates/` or `$CSPACE_HOME/lib/agents/`.

**Instance naming**: Auto-assigned from planet names with deterministic port ranges. Custom names get random port assignment.

**Exit code handling**: Exit codes 0 and 2 (stream pipe closed) are success; 141 (SIGPIPE) is expected.

**Adding a CLI command**: Add a `cmd_<name>()` function in `bin/cspace`, add a case in the main dispatcher at the bottom, and document in `usage()`.

## Commit Style

Short imperative sentences describing what changed and why. Examples from history:
- "Fix EPIPE crash in supervisor and $DC reference in cmd_up"
- "Add incremental commit+push after implement and verify phases"
- "Scope compose project names with project prefix for isolation"
