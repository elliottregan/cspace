---
title: claude update every boot vs. version-pinned settings gates — recurring wizard-regression trap
date: 2026-07-16
kind: finding
status: open
category: bug
tags: entrypoint, claude-code, settings, drift, plugins
---

## Summary
`cspace-entrypoint.sh` runs `claude update` on every boot (pulling the newest CLI), while the gate-suppression `settings.json` it writes is a wholesale heredoc clobber whose keys are "verified against Claude Code v2.1.183" (`cspace-entrypoint.sh:98-109`). Any settings-schema change in a newer CLI can silently re-introduce the first-run gates (theme picker, trust prompt, bypass-permissions warning) — the exact regression class already fixed once in commit 644205e ("suppress interactive Claude first-run gates on Claude Code 2.1.x"). The structure guarantees it recurs: the moving part (CLI) auto-updates, the compensating part (settings keys) is frozen per image.

## Details
- Related fragility with the same root cause: `cspace-install-plugins.sh:59,112` gates idempotency on grepping the human-readable `claude plugins marketplace list` output — another moving-target parse against the un-pinned CLI.
- Suggested directions (pick one posture): **pin** the Claude Code version in the image and bump it deliberately with each cspace release (image rebuilds already exist as a mechanism), or **keep auto-update** but add a boot-time smoke check that a non-interactive `claude` invocation actually starts without gates, failing loudly instead of stranding `cspace attach` in a wizard. Independently: merge settings via jq (as the `~/.claude.json` block at `:248-264` already does) rather than heredoc-clobbering the whole file.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
