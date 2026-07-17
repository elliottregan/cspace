---
title: custom sandbox names bypass collision detection; registry blind-overwrites
date: 2026-07-16
kind: finding
status: open
category: bug
tags: registry, naming, cmd-up
---

## Summary
`pickPlanetName` guards only the auto-name path (`cmd_up.go:59-67`, `1131-1153`). An explicit `cspace up <name>` with a name already in use reuses `containerName = cspace-<project>-<name>`, and `Registry.Register` (`internal/registry/registry.go:113-122`) overwrites the existing `project:name` map entry with no exists-check. The first sandbox keeps running but is orphaned: its registry entry (token, IP) is stomped, DNS now points at the new sandbox, and `cspace down <name>` only sees the new entry — leaking the old container and its resources.

## Details
- Matters more now that agents are encouraged to spawn sandboxes with descriptive custom names (`issue-<n>`, task labels) — collisions between an agent-spawned name and an existing one are plausible, not hypothetical.
- Suggested direction: `up` checks the registry (and/or live containers) for the name before provisioning and errors with "already exists — use a different name, or `cspace down <name>` first"; optionally a `--replace` flag for the deliberate case. `Registry.Register` could also refuse to overwrite an entry whose container is still alive.

## Updates
### 2026-07-17T03:42:21Z — @agent — status: open
filed from the 2026-07-16 hardening survey
