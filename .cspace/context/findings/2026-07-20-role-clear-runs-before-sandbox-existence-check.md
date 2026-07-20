---
title: agent-role sync runs before any sandbox-existence check; re-up of a live sandbox can silently drop its role
date: 2026-07-20
kind: finding
status: open
category: bug
tags: cmd-up, role, ordering, naming
---

## Summary
`cspace up`'s role sync (write `--role` / clear when absent, cmd_up.go ~198-207) runs before any check that the named sandbox already exists and is running. `cspace up <running-sandbox>` without `--role` therefore deletes the live sandbox's staged `/sessions/agent-role.md`; the running supervisor keeps its in-memory role, but the next OOM/crash respawn resolves no override and the role is silently lost. Compounds the known custom-name collision gap (`2026-07-16-custom-sandbox-names-bypass-collision-check`).

## Details
- Fix direction: when the collision/existence guard lands (per the older finding), order it BEFORE the role sync so a re-up of a live sandbox errors out before mutating its session state.

## Updates
### 2026-07-20T05:30:00Z — @agent — status: open
filed from the general-agent branch's final whole-branch review
