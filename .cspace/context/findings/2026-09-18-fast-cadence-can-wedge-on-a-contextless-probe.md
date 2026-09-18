---
title: the fast poll cadence can wedge forever on a contextless probe
date: 2026-09-18
kind: finding
status: open
category: bug
tags: control-plane, dashboard, polling, concurrency
---

## Summary
`control.Client.InteractiveState(project, sandbox string) InteractiveState`
(`internal/control/interactive.go:52`) takes no `context.Context` — it is a
plain file read with no deadline. It runs inside `liveCmd`'s per-sandbox
goroutines (`internal/controlplane/poll.go:116`), which the dashboard's 1s
fast-poll cadence waits on via an unconditional `wg.Wait()`
(`poll.go:122`) before it can return `liveMsg`. If that read ever blocks, the
fast cadence stops polling silently and permanently, with nothing in the UI
or logs to say so.

## Details
`liveCmd` (`internal/controlplane/poll.go:96-124`) fans out one goroutine per
running/degraded sandbox, each of which calls both
`data.AgentStatus(ctx, ...)` (context-bounded by `fastTimeout`, 3s) and
`inter := data.InteractiveState(k.Project, k.Name)` (`poll.go:116`, no
context at all — `InteractiveState` reads
`AgentStatePath(c.home, project, sandbox)` off disk with no timeout). The
function returns only after `wg.Wait()` (`poll.go:122`) — so a single blocked
`InteractiveState` read (e.g. a wedged mount under `~/.cspace/sessions/`,
network home directory hiccup, or any filesystem stall) hangs the whole
goroutine, and `wg.Wait()` never returns.

The consequence is in `internal/controlplane/model.go`: `pollingFast` is set
`true` when a `fastTickMsg` starts a new `liveCmd()` (`model.go:160-161`) and
is only ever cleared back to `false` when the resulting `liveMsg` lands
(`model.go:182-183`, `case liveMsg: m.pollingFast = false`). The ticker
itself always reschedules (`model.go:158`), but the handler only calls
`m.liveCmd()` again when `!m.pollingFast` (`model.go:160`). So once one
`InteractiveState` call wedges, `pollingFast` is stuck `true`, the ticker
keeps firing every second forever, and the fast cadence — agent state,
interactive glyph — simply stops advancing with no error surfaced anywhere.

Flagged during the final review's Task 5 parked-findings pass as low
probability against a local JSON file but invisible if it ever happens.

Fix: either give `InteractiveState` a `context.Context` parameter so
`liveCmd` can bound it the same way it bounds `AgentStatus`, or have
`liveCmd` select on `ctx.Done()` around the blocked goroutine instead of an
unconditional `wg.Wait()`.

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review's parked-findings
adjudication (Task 5: `InteractiveState` takes no context; a blocked call
kills the fast cadence).
