---
title: the fast poll's own timeout can misreport a busy host as "agent unreachable"
date: 2026-09-18
kind: finding
status: open
category: bug
tags: control-plane, dashboard, polling, timeout
---

## Summary
The dashboard's fast poll (`liveCmd`, `internal/controlplane/poll.go:96-124`)
bounds its whole 1s cadence to `fastTimeout` = 3s (`poll.go:25`), fans out at
most `liveConcurrency` = 8 sandboxes concurrently (`poll.go:35`), and each
individual supervisor probe inside that fan-out is separately capped at
`probeTimeout` = 800ms (`internal/control/client.go:19`). Past roughly 32
running sandboxes on one host, the arithmetic (`ceil(n/8) * 800ms`) exceeds
the 3s ceiling before every target's probe can even run, and because the
resulting context-deadline error is discarded, those rows render as "agent:
supervisor unreachable" instead of simply "not polled yet this tick."

## Details
`liveCmd` creates one shared `ctx` bounded by `fastTimeout` (3s) for the
whole fan-out (`poll.go:99`), then bounds concurrency with
`sem := make(chan struct{}, liveConcurrency)` (`poll.go:104`,
`liveConcurrency = 8`, `poll.go:35`). Each goroutine calls
`agent, _ := data.AgentStatus(ctx, k.Project, k.Name)` (`poll.go:118`) —
the error is explicitly discarded (the comment above it says "An unreachable
supervisor is not an error here"). `AgentStatus` in turn calls
`c.probeStatus(ctx, e)` (`internal/control/agent.go:35`,
`internal/control/snapshot.go:170-203`), whose HTTP client fails as soon as
`ctx`'s deadline passes; `probeStatus` returns `(AgentStatus{}, false)` in
that case, which `AgentStatus` turns into `AgentStatus{}` (i.e.
`Reachable: false`) with a `nil` error.

With `liveConcurrency = 8`, more than 32 targets means at least
`ceil(n/8) = 5` sequential batches through the semaphore; each batch is
bounded below by `probeTimeout` (800ms, `internal/control/client.go:19`) for
any probe that actually runs. `5 * 800ms = 4000ms` already exceeds
`fastTimeout` (3000ms) — and the crossover happens even earlier in practice,
since goroutines queued behind the semaphore that don't get a slot before
`ctx` expires fail immediately with no probe having run at all. Any sandbox
whose goroutine loses this race gets `Reachable: false`, and
`internal/controlplane/view_detail.go:43` renders
`"agent: supervisor unreachable — send and interrupt are off"` for it —
indistinguishable in the UI from a genuinely dead supervisor, even though the
sandbox is fine and simply wasn't reached inside this tick's budget.

Flagged during the final review's Task 5 parked-findings pass.

Fix: raise `fastTimeout` relative to `liveConcurrency * probeTimeout`, or
lower `probeTimeout`/raise `liveConcurrency`, or (more robust) have
`liveCmd` distinguish "this probe's ctx expired" from "the supervisor
answered and said no" and render the former as a stale/skipped state rather
than reusing `AgentStatus{Reachable: false}`.

## Updates
### 2026-09-18 — status: open
Filed from the control-plane-3-dashboard final review's parked-findings
adjudication (Task 5: `fastTimeout` (3s) exceeds `fastInterval` (1s);
~32 running sandboxes render as "agent unreachable" rather than stale).
