---
title: unbounded marketplace-add in plugins install can stall boot past cspace up's 60s health wait
date: 2026-07-19
kind: finding
status: open
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
