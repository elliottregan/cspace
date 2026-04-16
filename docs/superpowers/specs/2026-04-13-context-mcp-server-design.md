# Context MCP Server — Design

**Date:** 2026-04-13
**Status:** Approved for implementation planning

## Problem

Strategic context — why we're building what we're building, what decisions have been made, and what's coming next — gets lost between sessions. A prior approach synced GitHub milestones into a generated markdown file; it was removed because it coupled planning to GitHub's data model and added complexity without sufficient value.

Agents and humans need a shared, persistent project brain that survives across sessions without relying on any external service as the source of truth. In cspace specifically, the coordinator and implementer agents should be grounded in the same context a human would bring to the work.

## Solution

A local MCP server, shipped as a `cspace` subcommand, that abstracts a `.cspace/context/` directory in the repo behind a small tool interface. The directory holds layered planning context with structured ownership: humans own direction and roadmap, agents own decisions and discoveries.

The coordinator injects direction + roadmap into each sub-agent's prompt at dispatch time. Sub-agents pull decisions and discoveries on demand via the `read_context` tool.

## Storage

All context lives in the repo at `.cspace/context/`:

```
.cspace/context/
  direction.md              ← human-owned
  principles.md             ← human-owned
  roadmap.md                ← human-owned
  decisions/                ← agent-owned
    YYYY-MM-DD-slug.md
  discoveries/              ← agent-owned
    YYYY-MM-DD-slug.md
```

Version-controlled. Travels with the repo. Agent-written entries are isolated in their own subdirectories so human curation is easy.

On first call to any write tool, the server creates `.cspace/context/` and the subdirectories if missing, and seeds `direction.md`, `principles.md`, and `roadmap.md` with commented templates explaining what goes in each. No separate init command.

`read_context` deliberately does **not** trigger seeding: on a fresh repo it returns empty strings for the human-owned sections and empty arrays for decisions/discoveries. This keeps read calls side-effect-free so a human or tool can inspect the current state without accidentally creating files. Callers that need the templates to exist should call `log_decision` or `log_discovery` first (or edit the files by hand).

## Ownership model

| Section          | Owner  | Read     | Write                       |
| ---------------- | ------ | -------- | --------------------------- |
| `direction.md`   | Human  | Everyone | Human only (direct edit)    |
| `principles.md`  | Human  | Everyone | Human only (direct edit)    |
| `roadmap.md`     | Human  | Everyone | Human only (direct edit)    |
| `decisions/`     | Agents | Everyone | Agents via MCP tools        |
| `discoveries/`   | Agents | Everyone | Agents via MCP tools        |

The MCP server enforces ownership by not exposing write tools for direction, principles, or roadmap. Agents can read them but have no write path.

## Architecture

A new Go subcommand `cspace context-server` runs a stdio MCP server using `github.com/modelcontextprotocol/go-sdk` (the official SDK). It reads and writes files under `.cspace/context/` in the repo root. It has no knowledge of cspace runtime state — just files.

Wired into two places:

- **Host:** `.mcp.json` at the project root, picked up by any `claude` session on the host.
- **Container:** added to the MCP config written by `lib/scripts/init-claude-plugins.sh` so supervised agents get it automatically.

Stateless: every call reads the filesystem fresh. No in-memory cache, no lock files, no IPC with the supervisor.

### Why a separate server, not an in-process SDK MCP tool

Existing SDK MCP tools in `lib/agent-supervisor/sdk-mcp-tools.mjs` (`ask_orchestrator`, `notify_orchestrator`, etc.) share live state with the supervisor — the questions queue, directive channel, agent registry. Those must stay in-process.

This server's state is just files in the repo. A separate process is natural and lets non-supervised Claude sessions on the host use it too. Rule of thumb: in-process SDK tools for live runtime state, standalone servers for resource wrappers (files, DB, external API).

## Tools

Five tools, all on the `cspace-context` server.

### `read_context`

Returns direction, principles, roadmap, and recent decisions/discoveries.

Inputs (all optional):
- `sections` — array of `direction | principles | roadmap | decisions | discoveries`. Default: all.
- `since` — ISO date, filters decisions/discoveries by file date.
- `limit` — max entries per section. Default: 20 most recent each.

Output: a structured object with each requested section's content. Human-owned sections are returned verbatim; decisions/discoveries are returned as `[{date, slug, title, body}]`.

This is what agents call at the start of a session (or what the coordinator pre-calls when dispatching).

### `log_decision`

Records a significant architectural or design decision.

Inputs:
- `title` — short description, becomes the slug.
- `context` — why this decision came up.
- `alternatives` — what else was considered.
- `decision` — what was chosen.
- `consequences` — what follows from this choice.

Writes `.cspace/context/decisions/YYYY-MM-DD-<slug>.md`. Returns the file path.

### `log_discovery`

Records something learned that isn't a decision but is worth preserving.

Inputs:
- `title` — short description, becomes the slug.
- `finding` — what was found.
- `impact` — why it matters for future work.

Writes `.cspace/context/discoveries/YYYY-MM-DD-<slug>.md`. Returns the file path.

### `list_entries`

Lists decisions and/or discoveries with optional date range filtering.

Inputs:
- `kind` — `decisions | discoveries | both`. Default: `both`.
- `since`, `until` — ISO date bounds.

Output: `[{kind, date, slug, title, path}]`. No bodies. For scanning what's been logged.

### `remove_entry`

Deletes a decision or discovery by slug. For human curation passes.

Inputs:
- `kind` — `decisions | discoveries`.
- `slug` — the on-disk filename, with or without the `.md` extension (e.g. `2026-04-13-use-go-mcp-sdk` or `2026-04-13-use-go-mcp-sdk.md`). Must contain only `[a-z0-9-]`; bare title slugs without a date prefix are not accepted.

### What agents should NOT log

(Documented in the implementer playbook and CLAUDE.md.)

- Code conventions or commands (belongs in CLAUDE.md).
- Things obvious from reading the code or git history.
- Ephemeral session-specific notes.
- Duplicate entries covering the same decision or discovery.

## File format

Each entry file is markdown with YAML frontmatter for machine-readable metadata.

**Decision (`decisions/YYYY-MM-DD-<slug>.md`):**

```markdown
---
title: <title>
date: <YYYY-MM-DD>
kind: decision
---

## Context
<context>

## Alternatives
<alternatives>

## Decision
<decision>

## Consequences
<consequences>
```

**Discovery (`discoveries/YYYY-MM-DD-<slug>.md`):**

```markdown
---
title: <title>
date: <YYYY-MM-DD>
kind: discovery
---

## Finding
<finding>

## Impact
<impact>
```

Human-owned files have no required format — plain markdown. `read_context` returns them verbatim.

### Slug generation

Lowercase the title, strip punctuation, collapse runs of non-alphanumerics to single hyphens, trim leading/trailing hyphens, truncate to ~60 characters. Collisions with an existing file (same date + slug) get a numeric suffix: `-2`, `-3`, etc.

## Coordinator & implementer integration

### Coordinator (`lib/agents/coordinator.md`)

Add a step in the dispatch phase. Before launching each sub-agent task, the coordinator calls `read_context` with `sections: ["direction", "roadmap"]` and prepends the result to the sub-agent's task prompt as:

```
## Project Context

<direction.md contents>

<roadmap.md contents>

_Call `read_context` with `sections: ["decisions", "discoveries"]` if your task touches architecture or prior design choices._
```

### Implementer (`lib/agents/implementer.md`)

- **Setup phase:** "If a `## Project Context` header is present in your task, you already have direction and roadmap. Call `read_context` with `sections: [\"decisions\", \"discoveries\"]` if the task touches architecture or prior design choices."
- **Ship phase:** "If you made a significant design decision, call `log_decision`. If you learned something non-obvious about the code or infrastructure, call `log_discovery`. Only log things that would save a future session time — not every minor implementation choice."

### CLAUDE.md

Short section pointing humans at `.cspace/context/` and describing ownership. Replaces the removed "Strategic Context" pointer to `docs/milestone-context.md`.

## Wiring

**Host** — `.mcp.json` at project root (committed to the repo):

```json
{
  "mcpServers": {
    "cspace-context": {
      "command": "cspace",
      "args": ["context-server"]
    }
  }
}
```

**Container** — `lib/scripts/init-claude-plugins.sh` writes the same entry into the MCP config it generates. The `cspace` binary is already on PATH in the container.

**Working directory** — the server resolves `.cspace/context/` relative to the current working directory. Both host and container invocations start in the project root, so no flag is needed. If an override is ever needed, add `--root <path>`.

## Testing

- Go unit tests for the file layer: slug generation, frontmatter rendering, date filtering, collision handling, seeding of missing dirs and files.
- One end-to-end test that spawns `cspace context-server` over stdio, calls each tool via the official MCP Go SDK client, and asserts the filesystem state.
- No tests for the playbook edits — those are markdown.

## Out of scope

- Porting existing in-process MCP tools (`ask_orchestrator`, etc.) to Go — they need live supervisor state and belong in the supervisor process.
- Editing human-owned files (`direction.md`, `principles.md`, `roadmap.md`) via the MCP server.
- Search/indexing across entries beyond date filtering.
- Syncing with GitHub milestones or any external system.
- A `cspace context-init` command — seeding happens implicitly on first write.
