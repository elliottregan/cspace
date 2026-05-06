---
title: No convex-init equivalent in v1 — projects with local Convex backends can't dev-mode
date: 2026-05-05
kind: finding
status: acknowledged
category: observation
tags: substrate, services, post-1.0
---

## Summary
v0 cspace had a convex-init mechanism (e.g. `just convex-init <instance>`) that provisioned a local self-hosted Convex backend, populated `~/.convex-env` with admin keys, and wrote a `/workspace/.convex-init-done` marker. v1 has no equivalent. resume-redux's `scripts/ensure-env.sh` waits 60s for that marker and bails if it doesn't appear, so `pnpm run dev` (local mode, the default) is unusable without manual setup. Projects can work around by switching to MODE=develop (uses a remote Convex deployment) but lose offline / first-PR-test capability.

## Details


## Updates
### 2026-05-05T00:43:07Z — @agent — status: open
filed

### 2026-05-05T03:50:23Z — @agent — status: acknowledged
Partial mitigation in 3b8d1c4: the new project init hook (`/workspace/.cspace/init.sh`, runs once per sandbox boot before the supervisor) gives projects a place to drive convex-init themselves — they can ship a script that downloads the convex binary, writes the admin key, etc. No change to cspace's own image surface.

Full resolution (a generic services block / devcontainer.json adoption that orchestrates per-project sidecars uniformly) is post-1.0. The init-hook bridge is enough for v1.0.0 final because it lets resume-redux and similar projects ship a project-side fix without cspace knowing about convex specifically.
