---
title: env precedence is smeared across cmd_up.go; --env loses to ambient credentials
date: 2026-07-16
kind: finding
status: open
category: bug
tags: env, secrets, precedence, cmd-up, github, hardening
---

## Summary
The sandbox env map is assembled by ~8 sequential merge blocks inside `cmd_up.go`'s RunE, with ordering enforced only by comments. `docs/env-cspace.md` states `--env` merges last and always wins, but for credential keys it does not: host-shell `ANTHROPIC_API_KEY`/`CLAUDE_CODE_OAUTH_TOKEN` passthrough (`cmd_up.go:434-438`), host-shell `GH_TOKEN` passthrough (`:471-473`), the GitHub 401-fallback override (`:476-478`), and `propagateFamily` (`:479`) all run **after** the `--env` loop (`:420-426`). Net effect: `cspace up --env GITHUB_TOKEN=<narrow-pat>` is silently clobbered by an ambient `GH_TOKEN` and spread across the whole GitHub family.

## Details
- Effective order today: `.cspace/secrets.env` → compose `env_file` (`.env`/`.env.cspace`) → devcontainer `containerEnv` → `--env` → host-shell Anthropic passthrough + dual-carrier dedup → host-shell `GH_TOKEN` → gh-401-fallback → `propagateFamily`.
- **Constraint for any fix:** the GitHub family propagation is required behavior, not a bug — `gh` CLI reads `GH_TOKEN`, the GitHub MCP server reads `GITHUB_PERSONAL_ACCESS_TOKEN`, Actions-style tooling reads `GITHUB_TOKEN`, so one value must reach all three names. The defect is ordering only: pick the family's source of truth by precedence (explicit `--env` > host shell > secrets file > auto-discovery/fallback), then propagate once from the winner.
- The Anthropic dual-carrier dedup logic is duplicated verbatim (preflight `cmd_up.go:130-138` vs final merge `:444-461`); the copies can drift, making the preflight warn about a different credential than the one actually injected.
- Related: `internal/secrets/secrets.go:150-163` (`normalizeAnthropicCarrier`) deletes a misfiled `ANTHROPIC_API_KEY` holding an `sk-ant-oat` value even when `CLAUDE_CODE_OAUTH_TOKEN` already holds a *different* token — a valid credential can be silently discarded.
- Suggested direction: extract one ordered resolver (a table of sources with explicit precedence) with a table-driven test enumerating source × key collisions; then align `docs/env-cspace.md` with the code. This is where the rc.36-era env_file/secrets collision bug came from too — the structure that produced it is still in place.
- Priority: flagged by Elliott as a top hardening priority (2026-07-16); GitHub env-var confusion has caused real debugging sessions before.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
