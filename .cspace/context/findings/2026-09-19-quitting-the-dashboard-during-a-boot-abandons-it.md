---
title: Quitting the dashboard during a boot abandons the boot and leaves the registry at starting
date: 2026-09-19
kind: finding
status: open
category: bug
tags: control-plane, dashboard, registry, up, teardown
---

## Summary
`u` on a stopped sandbox starts `cspace up` through `cpActor.Up`, which
runs under its own `context.Background()` deadline rather than the model's.
Quitting the dashboard while that is in flight — `q`, leader `q` or Ctrl+C —
runs `quitCmd` (`internal/controlplane/panes.go:415`), which tears every
open pane down and quits. It neither waits for, cancels, nor mentions the
boot. Observed (plan 4b, Task 8): the sandbox is left half-created and its
registry entry stays at `starting`, where the next `cspace tui` shows it
indefinitely. `cspace down --keep-state <name>` repairs it.

## Details
`quitCmd`'s deliberate scope is panes: its doc explains that the detach
still has to run on the way out, and it does that concurrently under one
deadline. An `Actor` command is a different animal — it is not a pane, it
holds no tab, and the model has no handle on it beyond `m.action` being
non-empty.

Three ways it could behave instead, in increasing cost:

- **Say what it is abandoning.** If `m.action != ""`, print one line on the
  way out naming the action and the sandbox ("quitting during up mercury;
  it is still running — check `cspace tui` or `cspace down --keep-state
  mercury`"). Cheapest, and it turns a silent half-state into something the
  operator was told about.
- **Wait for it.** Quit blocks on the in-flight action the way it blocks on
  the pane closes. Correct, but `Actor.Up`'s deadline is minutes, and a
  dashboard that will not close for minutes is worse than the bug.
- **Cancel it.** Give `Actor` commands a context the model owns and cancel
  on quit. That makes `up` interruptible, which is a change to `up`'s own
  contract — a cancelled boot still leaves the registry mid-write unless
  `up` itself is made to roll back.

The first is the right size for the problem and is a change to quit's
*contract* rather than a bug fix, which is why it was filed rather than
folded into the fix wave.

## Updates
### 2026-09-19 — status: open
Filed from the plan 4b whole-branch review's ruling on Task 8's observation
4.
