---
title: Scroll mode never reaches a tmux-backed pane's history
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, panes, tmux, scrollback
---

## Summary
The design gives a focused pane a scroll mode — leader `[`, then arrows and
the page keys walk the scrollback, any other key returns to live. In
practice it is unavailable for every *sandbox* pane. tmux switches the
terminal to the alternate screen the moment it starts (measured against the
image's tmux 3.3a: its first bytes are `ESC[?1049h`), and nothing written to
the alternate screen ever enters the emulator's scrollback. Only a host
shell, which runs no tmux, can ever scroll. Commit f3e2155 makes the refusal
honest — leader `[` on a pane with no scrollback says so instead of arming a
mode that could only report "0 lines back" and eat the next key — but the
capability gap is in the design, not in that commit.

## Details
The history is real; it is just on the child's side. tmux's copy-mode holds
it, and Claude Code scrolls its own transcript with PgUp/PgDn — which work
today precisely *because* scroll mode is off and those keys reach the child.
So nothing is lost that a person cannot reach; what is missing is the
dashboard-level, uniform way of reaching it that the spec describes.

The refusal is implemented in `handleLeaderKey`'s `Scroll` arm
(`internal/controlplane/leader.go`), gated on `t.p.ScrollbackLen() == 0`,
and the notice tells the operator where the history actually is. The spec's
scroll-mode line now carries the same caveat.

Two ways out, if this is ever worth closing:

- **Drive tmux's copy-mode.** Leader `[` sends `tmux copy-mode` to the pane
  and then forwards arrows/page keys straight through, exiting copy-mode on
  any other key. Cheap in code, and it makes the same keys do the same
  thing in both pane kinds — but the dashboard's own scroll counter
  ("N lines back") has no equivalent, and the mode's state then lives in
  the guest rather than in the model.
- **Read `capture-pane -p -S -N`.** On entering scroll mode, exec
  `tmux capture-pane -p -S -<n>` into the pane's scrollback view and page
  through that snapshot. Keeps the dashboard's own mode and counter, costs
  an exec per entry, and the snapshot is frozen while the child keeps
  writing.

Neither belongs in a fix wave: both change what scroll mode *is*.

## Updates
### 2026-09-19 — status: open
Filed from the plan 4b whole-branch review's ruling on Task 8's observation
1, and the Task 8 review's spec-compliance note on spec lines 425-427.
