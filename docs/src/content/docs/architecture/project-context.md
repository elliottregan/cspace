---
title: Project Context
description: How agents share a persistent project brain — direction, principles, decisions, discoveries — across one-shot cspace sessions.
sidebar:
  order: 4
---

Claude Code agents are stateless between sessions. Each `cspace up` starts a fresh process with no memory of prior decisions ("we chose Redis over Postgres because X"), prior findings ("the firewall blocks `foo.com` by default"), or the project's overall direction. Without a shared layer, every agent rediscovers the same things, contradicts prior decisions, and re-asks questions that were already answered.

**Project Context** is the shared layer: a versioned, repo-local directory that agents read before starting work and append to as they learn. It turns one-shot sessions into something that accumulates knowledge.

## Data model

All context lives under `docs/context/` in the project repository:

```
docs/context/
├── direction.md        Human-owned: what we're building and why
├── principles.md       Human-owned: non-negotiable constraints
├── roadmap.md          Human-owned: what's coming next
├── decisions/
│   └── YYYY-MM-DD-<slug>.md     Agent-owned: "we chose X over Y"
└── discoveries/
    └── YYYY-MM-DD-<slug>.md     Agent-owned: non-obvious findings
```

### Ownership split

| Section                            | Owner  | Writer                                  |
| ---------------------------------- | ------ | --------------------------------------- |
| `direction.md` / `principles.md` / `roadmap.md` | Human  | Edits directly (git, editor)            |
| `decisions/`                       | Agents | `log_decision` MCP tool                 |
| `discoveries/`                     | Agents | `log_discovery` MCP tool                |

This split is deliberate. The load-bearing "why we're building this" and "what's non-negotiable" stays under editorial control — agents can read these but cannot write them. Operational knowledge that an agent uncovers (architecture notes, gotchas, design trade-offs) goes into subdirectories where a human can later curate or delete entries without affecting the strategic top-matter.

### Entry format

Each agent-written file is a small markdown document with YAML frontmatter:

```markdown
---
title: Use awk for preamble substitution
date: 2026-04-14
kind: decision
---

## Context
python3 isn't installed in `node:alpine`, and the previous `python3 -c "..."` snippet
interpolated a file's content into a Python string literal — a code-injection path.

## Alternatives
- Install python3 in the Dockerfile (+30 MB, extra maintenance)
- Inline bash string manipulation (doesn't handle multi-line well)
- awk with `getline` from the file (always available, literal-string ops)

## Decision
awk with `getline`. Passes the file path via `-v`, reads with `getline`, and
substitutes with `index()`/`substr()` — no scripting-language interpretation.

## Consequences
Works on any POSIX system. BusyBox awk is sufficient (`node:alpine` verified).
The preamble content is treated as opaque bytes.
```

Files are plain markdown so `git log`, `git blame`, and `grep` all work. No tool is required to read the brain — just read the files.

## MCP tool surface

The context system exposes five tools over stdio MCP:

| Tool           | Purpose                                                                 |
| -------------- | ----------------------------------------------------------------------- |
| `read_context` | Read direction/principles/roadmap + recent decisions/discoveries. Filterable by section and date. **Side-effect-free** — no files created on a fresh repo. |
| `log_decision` | Append a new `decisions/YYYY-MM-DD-<slug>.md`. Seeds the three human-owned files if missing. |
| `log_discovery` | Append a new `discoveries/YYYY-MM-DD-<slug>.md`. Same seeding behavior. |
| `list_entries` | Metadata-only listing (no bodies) for browsing and curation.           |
| `remove_entry` | Delete a single agent-written file by its filename. For human curation passes. |

The server is a single Go binary invoked as `cspace context-server --root <repo>`. It's a pure filesystem wrapper — no in-memory state, no IPC, no sync protocol. Every call reads disk fresh, which means:

- Restart is free.
- Two callers never conflict over cached state.
- A human editing `direction.md` in vim is picked up immediately by the next `read_context`.

## Implementation

The Go implementation is split into two packages:

```
┌─────────────────────────────────────────────────────────────┐
│ internal/cli/context_server.go                              │
│   MCP tool registration, JSON schema, handler glue          │
│   Depends on github.com/modelcontextprotocol/go-sdk         │
└───────────────────────────────┬─────────────────────────────┘
                                │ calls
                                ▼
┌─────────────────────────────────────────────────────────────┐
│ internal/contextstore/                                      │
│   Store.LogDecision / LogDiscovery / ReadEntries /          │
│   ListEntries / ReadHuman / RemoveEntry                     │
│   Pure file I/O, zero MCP dependency                        │
│   Slug generation, frontmatter render/parse, date filtering │
└─────────────────────────────────────────────────────────────┘
```

The `contextstore` package is unit-testable without spinning up an MCP server — its tests cover slug generation, frontmatter round-trips, path traversal guards, and single-pass scans. The MCP layer is thin glue that a single end-to-end test (`TestContextServerE2E`) exercises in full.

## Wiring

The same `cspace context-server` binary is registered in two places:

**Host** — `.mcp.json` at the repo root makes the server available to every `claude` session started inside the repo on your machine:

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

**Container** — `lib/scripts/init-claude-plugins.sh` registers the server at devcontainer startup, so every cspace instance launched by `cspace up` sees it too:

```bash
sudo -u dev "$CLAUDE_BIN" mcp add --scope user cspace-context -- \
    cspace context-server --root /workspace
```

Because the binary uses `--root <path>` to resolve `docs/context/`, the host and container invocations both read and write the same files (via the bind-mounted workspace volume).

## How agents use it

### Coordinator — preamble injection

The [coordinator playbook](/architecture/multi-agent-coordination/) fetches direction + roadmap at dispatch time and inlines them into every sub-agent's starting prompt via the `${STRATEGIC_CONTEXT_PREAMBLE}` placeholder. Sub-agents get strategic context baked into their first turn — they don't need to make an MCP call to discover the project's goals.

### Implementer — on-demand lookup

The [implementer playbook](/architecture/autonomous-agent-workflow/) calls `read_context` on demand:

- Early in exploration: `sections=["decisions", "discoveries"]` to check for prior work in the area.
- After shipping: `log_decision` for architectural choices worth remembering; `log_discovery` for non-obvious things learned.

The split is intentional. Coordinators need strategic direction to shape dispatch; implementers need granular history to make micro-decisions. They fetch different slices of the same brain.

For complete walkthroughs showing actual tool calls and outputs, see [Project Context: Examples](/architecture/project-context-examples/).

## Design choices

**Files on disk, not a database.** `git log` and `git blame` work. Humans can delete or edit directly. An agent writing to this system is just appending markdown files a human could also have written.

**Human / agent separation.** If agents could write to `direction.md`, they could silently drift the project's stated purpose. By moving those three files outside the agent write path, direction stays a deliberate human editorial decision.

**Side-effect-free reads.** `read_context` is called frequently (every coordinator dispatch; every implementer exploration phase). If it created files on first call, a CI check that ran `read_context` would leave commit noise. Making reads pure means calls are always safe.

**Pure filesystem, no cache.** No in-memory state means crashes don't lose data, two callers don't conflict, and the filesystem is the single source of truth. The cost is a few file reads per call; the benefit is dramatically simpler reasoning.

**Strict slug validation.** MCP tools are called by AI models — treat their arguments as untrusted input. `remove_entry` validates that the slug contains only `[a-z0-9-]` before touching the filesystem, so a hallucinated or adversarial path cannot reach `os.Remove`.
