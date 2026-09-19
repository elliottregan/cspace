---
title: cspace agent interrupt reports ok when nothing was interrupted
date: 2026-09-19
kind: finding
status: open
category: bug
tags: supervisor, agent, cli, control-plane, dashboard
---

## Summary
CLAUDE.md documents the supervisor's control surface as answering
`POST /interrupt` with "409 when there's no active task". Observed against a
real sandbox whose agent was idle (plan 4b, Task 8), `cspace agent interrupt
<sandbox>` printed `ok` and exited 0 — no 409, no "nothing to interrupt".
The dashboard inherits the false positive: the supervisor tab's Esc reports
`interrupt ok` in the footer whether or not anything was cancelled.

## Details
Pre-existing and outside plan 4b's diff — it is in the supervisor
(`lib/agent-supervisor-bun/`) and in `cspace agent interrupt`
(`internal/cli/`), neither of which this branch touches. It is recorded
here because the branch gave the bug a second, more visible mouth: before
the supervisor tab existed, the only way to see the wrong `ok` was to run
the CLI command.

What a person loses is the ability to tell "I cancelled a run" from "there
was nothing running" — which matters most in exactly the case the key is
pressed in a hurry.

Two places to look, and they are different bugs depending on which is true:

- The supervisor answers 200 where its documented contract says 409. Then
  the fix is in `routes.ts`'s interrupt handler: an interrupt with no
  in-flight task has to be a 409, and its tests should say so.
- The supervisor answers 409 and the CLI swallows it. Then the fix is in
  `cspace agent interrupt`'s response handling, which should surface the
  status rather than printing `ok` for any non-transport outcome.

The dashboard's own arm (`handleSupervisorKey`'s `esc`) now refuses to send
an interrupt at all unless the row's agent is reported *working*, which
narrows the window but does not close it: the fast ticker's sample can be
up to a second stale, so an agent that finished in between still gets an
interrupt and still gets an `ok`.

## Updates
### 2026-09-19 — status: open
Filed from the plan 4b whole-branch review's ruling on Task 8's observation
3.
