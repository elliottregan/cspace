---
title: Memory Architecture
description: How cspace persists agent knowledge across sessions — Claude's native memory for personal state, cspace-context for live collaborative knowledge.
sidebar:
  order: 6
---

cspace agents run in ephemeral containers. Without persistence, every `cspace up` starts blank — no memory of prior decisions, no awareness of bugs found by a sibling persona, no record of what the project is even building. Two complementary systems solve this at different layers.

## Two layers, two purposes

| Layer | What it stores | Shared live across containers? | Committed to git? | Location |
| --- | --- | --- | --- | --- |
| **Claude's memory** | Personal agent state — user preferences, feedback, project notes. Written by Claude Code's built-in `/remember` and the four-type memory system. | No — per-session, last-writer-wins | Yes | `.cspace/memory/` |
| **cspace-context** | Collaborative project brain — decisions, discoveries, findings. Written by agents via MCP tools (`log_decision`, `log_finding`, etc.). | **Yes** — all containers see the same directory in real time | Yes | `.cspace/context/` |

The split is deliberate. Claude's memory is about *how to behave* in this project — it tunes an individual agent's approach. cspace-context is about *what the project knows* — it accumulates knowledge that any agent can read and act on. Conflating the two would either make Claude's memory too noisy for agents to parse or make collaborative knowledge too personal to share.

## `.cspace/` directory layout

Everything lives under `.cspace/` at the project root:

```
.cspace/
├── memory/                     Claude Code's native memory
│   ├── MEMORY.md               Index (auto-managed by Claude Code)
│   ├── user_role.md            Example: "user is a senior engineer"
│   ├── feedback_tdd.md         Example: "always use TDD in this repo"
│   └── project_auth.md         Example: "auth rewrite driven by compliance"
│
├── context/                    cspace-context MCP server store
│   ├── direction.md            Human-owned — what we're building and why
│   ├── principles.md           Human-owned — non-negotiable constraints
│   ├── roadmap.md              Human-owned — what's coming next
│   ├── decisions/              Agent-owned — "we chose X over Y because Z"
│   │   └── 2026-04-13-use-awk-for-preamble.md
│   ├── discoveries/            Agent-owned — non-obvious findings
│   │   └── 2026-04-14-firewall-blocks-foo.md
│   └── findings/               Agent-owned — lifecycle-aware issues
│       ├── 2026-04-15-signup-button-unresponsive.md
│       └── 2026-04-15-signup-button-unresponsive.md.lock
```

Both directories are committed to git, so knowledge survives volume wipes, container rebuilds, and fresh clones. The only file that's gitignored is `.cspace.local.json` (per-machine config overrides).

## Layer 1: Claude's memory

### What it is

Claude Code's built-in memory system — the same one that powers `/remember` in any Claude Code session. Four types:

- **user** — who the user is, their role, expertise
- **feedback** — corrections and confirmations about how to work
- **project** — ongoing work context, stakeholder decisions, deadlines
- **reference** — pointers to external resources (Slack channels, dashboards, docs)

Each memory is a markdown file with YAML frontmatter (`name`, `description`, `type`) plus a body. `MEMORY.md` is the index Claude Code reads to decide which memories are relevant.

### How cspace persists it

Inside every cspace container, Claude Code expects its memory at:
```
/home/dev/.claude/projects/-workspace/memory/
```

cspace bind-mounts the project's `.cspace/memory/` directory to that path:

```yaml
# docker-compose.core.yml
- ${PROJECT_ROOT:-.}/.cspace/memory:/home/dev/.claude/projects/-workspace/memory
```

`cspace up` pre-creates the directory with the invoking user's ownership (so Docker doesn't auto-create it as root) and seeds an empty `MEMORY.md` stub on first provision.

### What cspace does NOT do

- **No reconciler.** cspace does not intercept, hook, or rebuild Claude's `MEMORY.md`. Claude Code writes it; cspace just ensures the files land somewhere durable.
- **No cross-container sync.** If agent A in mercury writes a memory and agent B in venus is already running, B won't see it until B's next session start (when Claude Code re-reads MEMORY.md from disk). This is acceptable because Claude's memory is personal tuning, not collaborative state.
- **No format changes.** The file layout, frontmatter schema, and `MEMORY.md` format are entirely Claude Code's own. cspace is a transparent persistence layer.

### Migration

If you have pre-existing memory in the legacy `cspace-<project>-memory` Docker volume:

```bash
cspace memory migrate          # copies volume contents → .cspace/memory/
cspace memory migrate --dry-run  # preview without copying
```

## Layer 2: cspace-context (the collaborative brain)

### What it is

A structured, versioned knowledge base that agents read before starting work and append to as they learn. Three categories of agent-owned entries, plus three human-owned strategic documents:

| Kind | Owner | MCP tools | Lifecycle |
| --- | --- | --- | --- |
| `direction.md` / `principles.md` / `roadmap.md` | Human | `read_context` (read-only for agents) | Edited directly by humans |
| `decisions/` | Agent | `log_decision`, `read_context`, `list_entries` | Terminal — write-once, immutable |
| `discoveries/` | Agent | `log_discovery`, `read_context`, `list_entries` | Terminal — write-once, immutable |
| `findings/` | Agent | `log_finding`, `append_to_finding`, `list_findings`, `read_finding` | Lifecycle — `open → acknowledged → resolved \| wontfix` |

Decisions and discoveries are terminal records: "we chose Redis because X" or "the firewall blocks foo.com by default." Once written, they don't change. Findings are different — they track bugs, observations, and refactor proposals that accumulate updates over time and eventually resolve.

### How live sharing works

Every cspace container in a project bind-mounts the same `.cspace/context/` directory from the host:

```yaml
# docker-compose.core.yml
- ${PROJECT_ROOT:-.}/.cspace/context:/workspace/.cspace/context
```

When agent A in mercury calls `log_finding`, the file lands in the host's `.cspace/context/findings/` directory. Agent B in venus — running its own cspace-context MCP server against the same bind mount — sees the file immediately on its next `list_findings` or `read_context` call. No git push/pull required.

### Concurrent-write safety

Multiple containers writing simultaneously is a core workflow (coordinator dispatches 4 implementers in parallel; each may log findings or decisions mid-task). Two protections:

**New entry creation** (`log_decision`, `log_discovery`, `log_finding`): uses `O_EXCL` on file creation. If two containers race to create the same slug, one gets `EEXIST` and retries with a collision-bumped suffix (`-2`, `-3`). No clobber possible.

**Finding updates** (`append_to_finding`): uses advisory `flock` on a sidecar `.lock` file plus atomic temp-then-rename. The lock file is never renamed, so all writers agree on the same inode — this avoids the classic "rename invalidates flock" problem. Concurrent appends serialize cleanly; all update lines survive.

### How agents use it

**Coordinators** call `read_context(sections=["direction", "roadmap"])` before dispatching sub-agents, injecting strategic context into each implementer's starting prompt. They also call `list_findings(status=["open", "acknowledged"])` to surface relevant open issues.

**Implementers** call `read_context(sections=["decisions", "discoveries"])` before designing, to avoid re-litigating settled questions. After shipping, they call `log_decision` for architectural choices and `log_discovery` for non-obvious learnings. When they encounter bugs or refactor opportunities outside their current task's scope, they call `log_finding` instead of fixing inline.

**Commit-marker convention**: when a commit resolves a finding, agents append `(cs-finding:<slug>)` to the commit message and call `append_to_finding(slug, note, status="resolved")`. A future `cspace findings doctor` can cross-reference git history to detect orphans.

### Findings lifecycle

Findings are the most dynamic context kind. They're designed for the "agent noticed something worth tracking but not fixing right now" case:

```
open → acknowledged → resolved
                   → wontfix
```

Each finding has:
- **Category**: `bug`, `observation`, or `refactor`
- **Status**: transitions via `append_to_finding` calls (not enforced as a strict state machine)
- **Updates**: an append-only `## Updates` section with timestamped `### YYYY-MM-DDTHH:MM:SSZ — @author — status: <status>` subheadings

The `read_context` brain digest includes only `open` + `acknowledged` findings (capped at 10, Updates truncated to the last 3 entries) so the summary stays terse even as findings accumulate.

## Design choices

**Two layers, not one.** Claude Code already has a memory system that agents know how to use. Replacing it with a custom system would mean rewriting Claude Code's tooling, training agents to use a different protocol, and maintaining compatibility as Claude Code evolves. Instead, cspace lets Claude's memory be Claude's memory and builds a separate collaborative layer purpose-built for cross-agent coordination.

**Files on disk, not a database.** Both layers are plain markdown files under git. `git log`, `git blame`, and `grep` all work. Humans can edit or delete entries directly. An agent writing a finding is just appending a markdown file a human could also have written.

**Bind mounts, not volume sharing.** Docker named volumes are per-instance and wiped by `cspace down`. Bind mounts from the host filesystem survive container lifecycle events and make changes visible to `git status` immediately. The trade-off is DooD path-resolution complexity (bind-mount sources resolve on the docker daemon's filesystem, not the calling container's), but for the primary use case — cspace running on the user's actual host — paths align naturally.

**`.cspace/` as the namespace.** Memory, context, project config, and local overrides all live under one directory. This avoids collisions with the project's own `docs/`, `config/`, or other standard directories, and makes it clear at a glance what cspace owns.
