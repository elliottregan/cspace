---
title: Directory Structure
description: The two .cspace directories — global (in your home) and per-project (in the repo) — and what lives in each.
sidebar:
  order: 0
---

cspace uses two directory trees: a **global** one in your home directory for machine-wide state, and a **per-project** one in each repository for project-specific configuration and knowledge.

## Global: `~/.cspace/`

Machine-wide state that is **not committed to git**. Shared across all projects on this machine.

```
~/.cspace/
├── proxy/                              Global reverse proxy stack
│   ├── docker-compose.yml              Traefik + CoreDNS compose file
│   └── Corefile                        CoreDNS config (*.cspace.local → 127.0.0.1)
│
└── sessions/                           Claude Code session transcripts
    └── <project-name>/                 One subdir per project
        ├── <session-id>.jsonl          Session transcript
        └── ...
```

### `proxy/`

The global [reverse proxy](/architecture/reverse-proxy/) (Traefik + CoreDNS) config. These files are **copied here from the built-in assets** at proxy startup to ensure they're under `$HOME` — Docker Desktop on macOS only shares certain host paths (`/Users`, `/tmp`, `/private`) into containers by default. Bind-mounting from `/opt/` or other paths outside the shared list causes Docker to silently create empty directories, which crashes CoreDNS.

Started automatically on the first `cspace up`. Shared across all projects.

### `sessions/<project>/`

Claude Code session JSONL transcripts, one file per session. These are bind-mounted into every container at `/home/dev/.claude/projects/-workspace/` so sessions survive `cspace down` and are shared across all instances in a project. Teleport reattaches to an existing session by resuming from this directory.

Overridable via `CSPACE_SESSIONS_DIR` for non-standard layouts.

If you have pre-existing sessions in legacy Docker volumes, run `cspace sessions migrate` once to copy them here.

## Per-project: `.cspace/`

Project-specific configuration and knowledge, committed to git (except `.cspace.local.json`). Lives at the repository root.

```
.cspace/
├── memory/                             Claude Code's native memory
│   ├── MEMORY.md                       Index (managed by Claude Code)
│   └── *.md                            Individual memory files
│
├── context/                            Collaborative project brain (MCP server)
│   ├── direction.md                    Human-owned: what we're building and why
│   ├── principles.md                   Human-owned: non-negotiable constraints
│   ├── roadmap.md                      Human-owned: what's coming next
│   ├── decisions/                      Agent-owned: "we chose X over Y because Z"
│   │   └── YYYY-MM-DD-<slug>.md
│   ├── discoveries/                    Agent-owned: non-obvious findings
│   │   └── YYYY-MM-DD-<slug>.md
│   └── findings/                       Agent-owned: lifecycle-tracked issues
│       ├── YYYY-MM-DD-<slug>.md
│       └── YYYY-MM-DD-<slug>.md.lock
│
├── agents/                             Playbook overrides (optional)
│   ├── coordinator.md                  Override the default coordinator workflow
│   └── implementer.md                  Override the default implementer workflow
│
├── hooks/                              Hook script overrides (optional)
│   └── copy-transcript-on-exit.sh      Example: custom transcript handling
│
├── .cspace.json                        Project configuration
└── .cspace.local.json                  Per-machine overrides (gitignored)
```

### `memory/`

Claude Code's built-in memory system — four types (user, feedback, project, reference) as markdown files with YAML frontmatter. `MEMORY.md` is the index Claude Code reads to decide which memories are relevant.

Bind-mounted into every container. Committed to git so learnings survive volume wipes, container rebuilds, and fresh clones. cspace does not intercept or reconcile Claude's memory writes — it's a transparent persistence layer.

See [Memory Architecture](/architecture/memory-architecture/) for details on how this is persisted and the distinction from collaborative context.

If you have pre-existing memory in a legacy Docker volume, run `cspace memory migrate` once.

### `context/`

The collaborative project brain, accessed via the `cspace-context` MCP server. Two ownership tiers:

- **Human-owned** (`direction.md`, `principles.md`, `roadmap.md`) — edit directly. Agents can read but cannot write.
- **Agent-owned** (`decisions/`, `discoveries/`, `findings/`) — written by agents via MCP tools. Humans curate by editing or deleting files.

Bind-mounted from the host into every container, so writes by one agent are visible to siblings in real time without git push/pull.

See [Project Context](/architecture/project-context/) for the data model and MCP tools, and [Project Context: Examples](/architecture/project-context-examples/) for end-to-end walkthroughs.

### `agents/`

Optional playbook overrides. If `.cspace/agents/coordinator.md` exists, cspace uses it instead of the built-in coordinator playbook. Same for `implementer.md`. This lets projects customize the agent workflow without forking cspace.

### `hooks/`

Optional hook script overrides. Scripts here take precedence over the built-in hooks at `/opt/cspace/lib/hooks/`. Used for project-specific behavior like custom transcript handling or additional pre/post-tool checks.

### `.cspace.json`

Project configuration — merged with built-in defaults. Controls container image, verify commands, firewall rules, port assignments, and more. See [Configuration Reference](/configuration/configuration-reference/).

### `.cspace.local.json`

Per-machine config overrides. Gitignored. Use for local paths, custom Docker settings, or anything that varies between developer machines.

## Template resolution

When cspace needs a file (Dockerfile, compose template, agent playbook, script, hook), it checks the project `.cspace/` directory first, then falls back to the built-in assets:

```
.cspace/<name>  →  /opt/cspace/lib/templates/<name>
.cspace/agents/<name>  →  /opt/cspace/lib/agents/<name>
.cspace/scripts/<name>  →  /opt/cspace/lib/scripts/<name>
.cspace/hooks/<name>  →  /opt/cspace/lib/hooks/<name>
```

This means you can override any built-in template, playbook, or script by placing a file with the same name in the project's `.cspace/` directory.
