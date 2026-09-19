---
title: Two more copies of the tab-focus bookkeeping `focusTab` centralises
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, panes, mouse, altitude
---

## Summary
Rollout step 5 added `focusTab` (`internal/controlplane/mouse.go:129-137`)
so a click on a tab and the leader `n`/`p` bindings share one definition of
"point the keyboard at tab i": `m.focused = i; m.focus = focusMain;
m.scrolling, m.scroll = false, 0`. Two older call sites in
`internal/controlplane/panes.go` still carry their own hand-written copy of
exactly that sequence instead of calling it.

## Details
- `openOrFocus`'s already-open match branch,
  `internal/controlplane/panes.go:235-237`:
  ```go
  m.focused = i
  m.focus = focusMain
  m.scrolling, m.scroll = false, 0
  ```
- `addTab`, `internal/controlplane/panes.go:270-272` (plus
  `m.focused = len(m.tabs) - 1` immediately above at line 269):
  ```go
  m.focus = focusMain
  m.scrolling, m.scroll = false, 0
  ```

Neither is a bug today — all three copies agree — but it is the shape that
let `wheelMain`'s focus guard (Minor 5 in the branch review, filed
separately as leader-bracket-arms-scroll-mode-the-sidebar-cannot-escape)
drift out of step with leader `[` on this same branch: one centralised
helper age out of sync with copies is exactly how that kind of drift
starts. `focusTab` takes no index-bounds-only argument that `addTab` or
`openOrFocus`'s match branch couldn't supply (a freshly appended tab's
index is always `len(m.tabs)-1`, and the match branch already has `i`), so
both could call it instead of repeating its body.

**Failing scenario.** None today — this is an altitude/maintainability
observation, not a reachable bug. Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
adjudication table row T2-a: "`openOrFocus`/`addTab` still hold copies of
`focusTab`'s bookkeeping" — ruled **FILE**, "Altitude, no bug; out of
step-5 scope."

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review's adjudication of a
  parked Task 2 observation, during the fix wave (Important 1, Minor 1).
