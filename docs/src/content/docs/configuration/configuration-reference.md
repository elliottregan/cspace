---
title: Configuration reference
description: .cspace.json schema, merge order, and the fields that matter in v1.
sidebar:
  order: 1
---

cspace configuration is a single JSON file at `<project-root>/.cspace.json`, layered over `defaults.json` shipped with the binary.

## Merge order

Highest precedence first:

1. `<project>/.cspace.local.json` — gitignored per-machine override
2. `<project>/.cspace.json` — checked-in project config
3. Embedded `defaults.json` — cspace's built-in baseline

Each layer is JSON-merged: missing keys inherit from the lower layer; explicit keys override.

## Common fields

```json
{
  "project": {
    "name": "my-project",
    "repo": "github-org/my-project"
  },
  "resources": {
    "cpus": 4,
    "memoryMiB": 4096
  },
  "plugins": {
    "enabled": true,
    "install": [
      "superpowers",
      "frontend-design",
      "github"
    ]
  }
}
```

## `project`

| Field | Type | Default | Notes |
|---|---|---|---|
| `name` | string | dir name | Used for sandbox container naming and DNS hostnames. |
| `repo` | string | derived from `git remote` | `<owner>/<repo>` for issue-driven workflows. |

## `resources`

Per-sandbox CPU/memory caps. Apple Container's hard memory cap means an OOM inside the guest kills processes with no host safety net.

| Field | Type | Default | Notes |
|---|---|---|---|
| `cpus` | int | 4 | Override at boot with `cspace up --cpus N`. |
| `memoryMiB` | int | 4096 | Override at boot with `cspace up --memory N`. |

## `plugins`

Claude Code plugin install at sandbox boot.

| Field | Type | Default | Notes |
|---|---|---|---|
| `enabled` | bool | `true` | `false` skips plugin install entirely. |
| `install` | string[] | recommended set | Plugin names. Bare names default to `@claude-plugins-official` marketplace. |

The recommended set in `defaults.json` includes `superpowers`, `frontend-design`, `context7`, `code-review`, `code-simplifier`, `github`, `feature-dev`, `security-guidance`, `commit-commands`, `pr-review-toolkit`, `agent-sdk-dev`, `plugin-dev`. Plugins also install from the project's `/workspace/.claude/settings.json enabledPlugins` (the union is what gets installed).

## CLI flag overrides

Per-launch overrides bypass `.cspace.json`:

```bash
cspace up --cpus 2 --memory 8192     # heavier sandbox
cspace up --no-attach                # don't auto-launch claude
cspace up --no-overlay               # skip the boot animation
cspace up --browser                  # spawn the playwright sidecar
cspace up --workspace ./other-dir    # bind a non-cloned dir as /workspace
```

See `cspace up --help` for the full list.

## Project init hook

cspace runs `/workspace/.cspace/init.sh` (if present and executable) once per sandbox boot, after the workspace is mounted and before the supervisor starts. Use this for:

- Local-backend provisioning (convex, postgres, redis seed data)
- Eager `pnpm install` / `bun install` so the agent's first dev-server start is fast
- Generating env files from templates

The script runs as the `dev` user with `/workspace` as cwd. Make it idempotent — container recreation wipes everything, so the script should self-skip if already done (typically a `[ -f .cspace-init-done ] && exit 0` guard against a marker file inside `/workspace`).

Failures don't abort sandbox boot — output is captured to `~/.claude/cspace-init.log` for inspection. Example:

```bash
#!/usr/bin/env bash
# .cspace/init.sh
set -euo pipefail
cd /workspace
[ -f .cspace-init-done ] && exit 0

# Eager install
corepack pnpm install

# Project-specific bootstrap (convex, db migrations, etc.)
./scripts/ensure-env.sh

touch .cspace-init-done
```

## Credentials

Credential precedence (highest first):

1. `<project>/.cspace/secrets.env` — project-scoped, gitignored
2. `~/.cspace/secrets.env` — user-global manual entry
3. macOS Keychain (`cspace-<KEY>`, set via `cspace keychain init`)
4. Auto-discovery (Claude Code OAuth, `gh auth token`)

Format is dotenv (`KEY=value`). Common keys:

- `ANTHROPIC_API_KEY` — Anthropic API key (`sk-ant-api-...`)
- `CLAUDE_CODE_OAUTH_TOKEN` — Anthropic OAuth token (`sk-ant-oat-...`)
- `GH_TOKEN` — GitHub PAT (also wired as `GITHUB_TOKEN` and `GITHUB_PERSONAL_ACCESS_TOKEN` automatically)

cspace passes through only what's set, so claude CLI's "Auth conflict" warning never fires.

Run `cspace keychain status` to see where each credential is sourced.
