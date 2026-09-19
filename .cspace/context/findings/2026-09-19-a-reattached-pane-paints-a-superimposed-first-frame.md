---
title: A pane reattaching into an existing tmux session paints a superimposed first frame
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, panes, tmux, rendering, tui-smoke
---

## Summary
Opening a Claude pane on a tmux session that already exists — the ordinary
case after the first `leader x`, or on a second run of the dashboard —
leaves the main area showing two layouts at once: Claude's status block
appears ~14 rows above where it belongs, with fragments of tmux's own
attach output ("…9222g mouse on' to ~/.tmux.conf for whee") superimposed on
it. It self-heals on the next terminal resize.

The cheap dashboard-side fix the plan proposed — a one-shot SIGWINCH nudge a
beat after the pane opens — was implemented (b04d193) and **reverted**
(02da95a) because it does not work, and the measurement that shows why also
casts doubt on the whole defect: **the pane's emulator content is already
correct at that moment.** Switching to another tab and back, which makes the
dashboard rewrite every cell of the main area from its own `View()`,
produces a frame byte-identical to the post-resize reference. There is
nothing in the pane for a SIGWINCH to clean; what is wrong is the
incrementally painted screen, as seen through the pty harness.

**Nobody has yet looked at a reattach in a real terminal.** That is the next
step, and it is one minute of work: `cspace tui`, Enter, leader `x`, Enter,
look. Everything below was measured through `scripts/tui-smoke`.

## Details

Measured 2026-09-19 against sandbox `panecheck` (Apple Container 1.3.0,
tmux 3.3a in the image), branch `control-plane-4b-panes`, 120x40 pty. Scripts
and captures under the fix-wave-2 scratchpad (`fixwave/check1.py`,
`fixwave/diag1.py`, `fixwave/diag2.py`); the numbers are quoted in
`fix-wave-2-report.md`, check 1.

Four frames of the same reattach, each compared row-by-row against `R`, the
frame after a 119→120 column round trip (the reference Task 8 used):

| frame | how it was painted | rows differing from `R` |
|---|---|---|
| `F` no nudge | incremental | 11 of 40 |
| `F` single-tick nudge (`Resize(rows-1)`, `Resize(rows)` back to back) | incremental | 10 of 40 |
| `F` split-tick nudge (250 ms apart, so tmux sees two distinct sizes) | incremental | 9 of 40 |
| `F2` after switching to a host-shell tab and back | full rewrite of the main area | **0 of 40** |

The nudge in both shapes removes one or two rows of tmux's own chatter and
leaves the misplacement exactly where it was. `F2` is the decisive one: a
tab switch changes no pane content and issues no SIGWINCH — it only makes
the dashboard emit the whole main area again — and that alone produces the
clean frame. So `Pane.Render()` was already returning the right screen while
the terminal showed the wrong one.

Two candidates for where the incremental paint goes wrong, not
distinguished yet:

- **The harness's screen model diverges, and a real terminal is fine.** The
  harness's `Screen` advances one column per rune, so a double-width glyph
  in Claude's UI puts everything after it one column out on the harness and
  not on a real terminal; a stray scroll (its `_index()` scrolls the whole
  grid when a newline lands on the last row) would shift content up exactly
  the way this frame is shifted. Task 8 flagged this possibility and could
  not resolve it; the `F2` result makes it the leading explanation.
- **bubbletea's renderer really does emit an incomplete diff** for the burst
  of output a reattach produces, in which case a real terminal shows it too.

One more datum, from `diag1.py`: a **same-size** SIGWINCH produces no
repaint at all — the harness's screen (wiped by its own `resize`) stayed
blank for four seconds across several render ticks. The dashboard only ever
writes the diff against its own previous frame, so once a terminal's actual
state diverges from that model, nothing short of a real size change or a
full-area rewrite resynchronizes it. That is worth knowing for any future
tui-smoke work: **`Tui.resize` to the same geometry blinds the harness
rather than refreshing it.**

If a real terminal does show the superimposed frame, the fix is not a
SIGWINCH — it is making the dashboard repaint the main area in full when a
pane's first frames arrive (bubbletea has no public "repaint everything"
message, so this would mean rendering the pane through something that
forces a full line rewrite, or clearing the main area once on the first
`paneOutputMsg` of a newly opened tab). If it does not, close this and add
the wide-glyph caveat to `scripts/tui-smoke/tuilib.py` instead, because
every future capture of a pane inherits it.

## Updates
### 2026-09-19 — status: open
Filed from plan 4b's fix wave 2, group C.6, after the group E experiment
failed in both of its two tries. The nudge commit b04d193 is reverted by
02da95a; the unit tests that came with it are reverted too, so nothing of
it remains in the tree.
