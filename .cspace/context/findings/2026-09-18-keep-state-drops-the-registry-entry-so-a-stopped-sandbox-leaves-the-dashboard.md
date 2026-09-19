---
title: "cspace down --keep-state" unregisters the sandbox, so it vanishes from the dashboard instead of showing stopped
date: 2026-09-18
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, down, registry
---

## Summary
`teardownSandbox` in `internal/cli/cmd_down.go` calls `r.Unregister(project,
name)` unconditionally, before the `wipeState` (i.e. `!--keep-state`) check
that gates whether the clone/sessions/volumes are actually reclaimed. So
`cspace down --keep-state` — meant to preserve a sandbox's on-disk state for
a later resume — still removes it from the registry, and a sandbox removed
from the registry no longer appears in the dashboard at all. Since the
dashboard's boot action (`u`) is offered only on rows whose state is
`StateStopped`, and `--keep-state` never produces such a row, `u` is
currently unreachable through any `cspace` command — only a substrate-level
`container stop` or a crash produces a stopped row.

## Details
`teardownSandbox` (`internal/cli/cmd_down.go:167-244`) is called with
`wipeState = !keepState` (`cmd_down.go:125`,
`teardownSandbox(ctx, a, r, targetProject, name, cmd.OutOrStdout(), !keepState)`).
Inside it, `_ = r.Unregister(project, name)` runs at `cmd_down.go:231` — well
before the `if wipeState { wipeSandboxState(...) }` guard at
`cmd_down.go:241` that gates the clone/sessions/volume reclaim
`--keep-state` is meant to control. `Unregister` has no `wipeState` argument
and nothing else in `teardownSandbox` conditions it, so it runs every time
`cspace down` runs, `--keep-state` or not.

Verified live in Task 9 (`task-9-report.md`, Step 11): "`cspace down
dashcheck2 --keep-state` calls `r.Unregister` unconditionally
(`teardownSandbox`, cmd_down.go) *and* `Adapter.Stop` is `container rm
--force`, so both the registry entry and the container go. `--keep-state`
preserves the clone, sessions and volumes only. Verified: after it,
`container ls -a` had no `dashcheck2` and the registry had only
`cspace:dashcheck`." The only way that run could then produce a
dashboard-visible stopped row was `container stop
cspace-cspace-dashcheck2` directly against the substrate.

Consequence: `internal/controlplane/keys.go:198-200` gates the boot action
(`ActionBoot`, key `u`) to `r.Kind == control.RowSandbox && r.State ==
control.StateStopped`. A registry entry that `Unregister` removed produces
no row at all (stopped or otherwise) — the sandbox simply disappears from
the dashboard — so `cspace down --keep-state` can never itself produce the
one row-state (`StateStopped`) that would let a person reboot the sandbox
from the dashboard. As the report puts it: "no sequence of commands could
get such a sandbox running again from the dashboard" via `cspace`'s own
surface; only an external `container stop` or a crash gets there.

If "suspend and resume from the dashboard" is meant to be a supported
workflow, `--keep-state` should probably keep the registry entry too (and
presumably record/report the container as stopped rather than removed) —
flagged in the review as a design question for step 4, not a step-3 bug in
isolation, but recorded here as a bug because the current behavior silently
defeats what `--keep-state`'s own flag description promises ("preserve
clone, sessions, and volumes").

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review's Task 9 observations,
confirmed live against `cmd_down.go:231` (`_ = r.Unregister(...)`,
unconditional).

### 2026-09-18 — status: resolved
`teardownSandbox` now unregisters only when `wipeState` is true; with
`--keep-state` it calls the new `registry.MarkStopped`, so the sandbox stays
in the registry with `state: "stopped"`, `Correlate` gives it a selectable
`StateStopped` row, and the dashboard's boot key is reachable. The shared
browser's reference count is now taken over entries whose state is not
`"stopped"`, rather than over every entry: a kept entry's container is gone
and holds no claim on the sidecar, and counting by identity would have left
the browser running after `cspace down --all --keep-state`.
