---
title: daemon stop uses system-wide pkill; respawn gate polls TCP only
date: 2026-07-16
kind: finding
status: open
category: bug
tags: daemon, lifecycle, pkill, race
---

## Summary
`stopRegistryDaemon` (`internal/cli/cmd_daemon.go:483`) runs `pkill -f "daemon serve"` — a system-wide argv **substring** match with no scoping to a process cspace owns. It's invoked automatically by `ensureRegistryDaemon` on every version-mismatched `cspace up`, not just explicit `cspace daemon stop`, so any unrelated process with "daemon serve" in its command line gets SIGTERM'd as collateral. The respawn gate `waitPortFree` (`cmd_daemon.go:500-510`) polls TCP only (the daemon's fatal bind includes the UDP DNS listener), never returns an error, and gives up silently after 3s — producing intermittent "daemon not accepting connections within 3s" boot failures when the old daemon is slow to release ports.

## Details
- The danger is acknowledged in-repo: `cmd_daemon_test.go` refuses to run the stop test when a real daemon is present.
- Suggested direction: write a pidfile at spawn (`~/.cspace/daemon.pid`, next to the existing `daemon.log`), kill by pid after verifying the argv matches, and fall back to `pkill` only with a much tighter pattern (full binary path). Make `waitPortFree` also probe the UDP listen and return a real error instead of silently timing out.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
