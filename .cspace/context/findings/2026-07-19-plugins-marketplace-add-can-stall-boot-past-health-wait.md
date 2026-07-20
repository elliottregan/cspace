---
title: unbounded marketplace-add in plugins install can stall boot past cspace up's 60s health wait
date: 2026-07-19
kind: finding
status: resolved
category: bug
tags: entrypoint, plugins, boot, timeout, ux
---

## Summary
Observed on a fresh rc.38 boot (cdp-verify sandbox): `claude plugins marketplace add anthropics/claude-plugins-official` hung on a slow GitHub fetch for several minutes with no timeout. The boot status sat in the `plugins` phase past `cspace up`'s 60s supervisor-health wait, so `cspace up` reported `Error: waiting for sandbox /health: /health did not respond in 1m0s` — while the sandbox went on to finish booting successfully a few minutes later (plugins completed, supervisor came up and answered with its auth-enforced 401). Net effect: a false boot failure on the console for a sandbox that is actually fine, with no hint that the plugins phase is the slow step.

## Details
- Two compounding gaps: `cspace-install-plugins.sh` runs network-bound `claude plugins marketplace add` steps with no timeout/retry; and `cspace up`'s health wait (60s) doesn't account for a slow plugins phase or surface WHICH phase the boot is stuck in (`/sessions/cspace-init.status` already carries the phase — the waiter could report "still in plugins phase" and keep waiting with a longer budget instead of failing blind).
- Suggested direction: bound each marketplace/plugin-install step (`timeout 120 claude plugins …`, retry once, warn-and-continue on persistent failure — plugins are enhancement, not boot-critical), and make the up-side wait phase-aware (read the status file; extend patience while phases are visibly progressing; error with the phase name when genuinely stuck).
- Related: `2026-07-16-claude-update-each-boot-vs-pinned-settings-gates` (same script family, same moving-target fragility).

## Updates
### 2026-07-19T21:15:00Z — @agent — status: open
filed during rc.38 CDP-relay live verification; the sandbox self-recovered, only the up-side reporting was wrong

### 2026-07-20T04:47:30Z — @agent — status: resolved
Both compounding gaps are fixed:

- **Bounded plugin installs** — `lib/runtime/scripts/cspace-install-plugins.sh`'s `run_bounded()` (lines 38-50) wraps every `claude plugins marketplace add` / `plugins install` call in `timeout -k 10 120 "$@"`: a 120s budget, SIGTERM at expiry, and a hard SIGKILL 10s later if the process ignores SIGTERM. On timeout or failure it retries once, then logs a warning and continues without that plugin/marketplace rather than stalling or failing the boot — plugins are an enhancement, not boot-critical.
- **Phase-aware boot health wait** — `internal/cli/cmd_up.go`'s `waitSupervisorHealth()` (lines 1108-1155) no longer uses a flat timeout. It reads `/sessions/cspace-init.status` (bind-mounted host-side copy of the sandbox's `/sessions/cspace-init.status`) on every poll; the patience budget resets whenever the phase value changes, so a boot that's visibly progressing (e.g. still in `plugins`, working through marketplace entries) keeps waiting. Only when the budget elapses with no phase change does it give up, and the error names the last-seen phase (`sandbox stuck in %q phase after %s`) instead of a blind `/health did not respond` — so the console blames the actual bottleneck.
- Also landed in the same effort: the health-check token is now sent as an `Authorization: Bearer` header (`cmd_up.go`'s `waitSupervisorHealth`) rather than a URL query param — the supervisor (`lib/agent-supervisor-bun/src/main.ts`) only ever checked the header, so a query-string token would have 401'd every real boot's health poll.
