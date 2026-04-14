# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**cspace** is a CLI for managing isolated Claude Code devcontainer instances. It spins up Docker containers with independent workspaces, browser sidecars, and network firewalls, then runs autonomous Claude agents against GitHub issues. Written in Go (CLI) with a Node.js agent supervisor component.

## Project Context

The `docs/context/` directory holds layered planning context accessible via the `cspace-context` MCP server.

- `direction.md`, `principles.md`, `roadmap.md` — human-owned. Edit directly.
- `decisions/` and `discoveries/` — agent-owned. Written by agents via `log_decision` / `log_discovery`. Curate with the `remove_entry` tool or by deleting files.

Agents call `read_context` at the start of non-trivial work. See `docs/superpowers/specs/2026-04-13-context-mcp-server-design.md` for the full contract.

## Development

The CLI is built with Go using the Cobra framework. The agent supervisor is Node.js ESM.

```bash
# Build and run locally
make build
./bin/cspace-go --help

# Run tests and static analysis
make test
make vet

# Rebuild container image after Dockerfile/template changes
cspace rebuild

# Launch a test instance
cspace up
cspace ssh mercury

# Agent supervisor dependencies (inside container or for local dev)
cd lib/agent-supervisor && pnpm install
```

## Architecture

### CLI (`cmd/cspace/`, `internal/`)

Go binary built with Cobra. Entry point is `cmd/cspace/main.go`, which calls `cli.Execute()`. Commands are organized in `internal/cli/` as `newXxxCmd()` functions registered via `AddCommand()` in `root.go`. Internal packages:

- **config** — Three-layer config merging: embedded `defaults.json` -> `.cspace.json` -> `.cspace.local.json`
- **instance** — Container lifecycle (queries, exec, health checks)
- **compose** — Docker Compose file resolution and environment export
- **ports** — Deterministic port assignment using planet names (mercury=1, venus=2, etc.)
- **supervisor** — Launches the Node.js agent supervisor, NDJSON stream processing, dispatch
- **provision** — Container provisioning (git bundle, compose up, workspace init)
- **docker** — Low-level Docker CLI wrappers
- **assets** — Embedded filesystem (`go:embed`) for templates, scripts, hooks, agents

### Agent Supervisor (`lib/agent-supervisor/`)

Node.js process (ESM) that wraps the Claude Agent SDK's `query()` with:
- An async-queue-backed prompt stream for injecting user turns mid-session
- A Unix socket server (`/logs/messages/{instance}/supervisor.sock`) for host->container commands (`send`, `respond`, `interrupt`)
- NDJSON event streaming to stdout, processed by Go's `ProcessStream()` for terminal rendering
- MCP tools for inter-agent communication (`ask_orchestrator`, `notify_orchestrator` for agents; `list_agent_questions`, `respond_to_agent`, `send_directive` for coordinators)

Key dependency: `@anthropic-ai/claude-agent-sdk`

### Container Environment (`lib/scripts/`, `lib/templates/`)

- **Dockerfile** — Alpine + Node + Claude Code + SSH + Docker-in-Docker
- **docker-compose.core.yml** — Devcontainer + Playwright run-server + Chromium CDP sidecar
- **entrypoint.sh** — Container init: firewall, plugins, workspace setup
- **init-firewall.sh** — iptables allowlist (GitHub, npm, Anthropic + custom domains)
- **init-claude-plugins.sh** — Writes Claude settings.json, hooks config, MCP servers

### Agent Playbooks (`lib/agents/`)

- **implementer.md** — 7-phase autonomous workflow: Setup -> Explore -> Design -> Implement -> Verify -> Ship -> Review
- **coordinator.md** — Multi-agent orchestration with dependency graph resolution and base branch chaining

### Hooks (`lib/hooks/`)

Shell scripts fired by Claude Code's hook system: progress logging on `PostToolUse`, transcript archival on `SessionEnd`.

## Key Patterns

**Config merging**: All config flows through `config.Load()` which deep-merges JSON in precedence order: embedded `defaults.json` -> `.cspace.json` -> `.cspace.local.json`. The merged result is available as `*config.Config`.

**Template overrides**: Users place files in `.cspace/` to override built-in templates (Dockerfile, compose files, agent playbooks). Resolution checks project dir first, falls back to `$ASSETS_DIR/templates/` or `$ASSETS_DIR/agents/`.

**Instance naming**: Auto-assigned from planet names with deterministic port ranges. Custom names get random port assignment.

**Exit code handling**: Exit codes 0 and 2 (stream pipe closed) are success; 141 (SIGPIPE) is expected.

**Adding a CLI command**: Create a `newXxxCmd()` function in a new file under `internal/cli/`, returning a `*cobra.Command`. Register it via `root.AddCommand()` in `root.go`.

**Embedded assets**: Templates, scripts, hooks, and agents are embedded via `go:embed` in `internal/assets/`. Run `make sync-embedded` (automatic with `make build`) to copy `lib/` contents into `internal/assets/embedded/` before building.

## Commit Style

Short imperative sentences describing what changed and why. Examples from history:
- "Fix EPIPE crash in supervisor and $DC reference in cmd_up"
- "Add incremental commit+push after implement and verify phases"
- "Surface stderr from failed container exec commands"
