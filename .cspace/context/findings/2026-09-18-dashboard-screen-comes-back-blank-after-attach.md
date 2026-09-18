---
title: the dashboard's screen comes back mostly blank after an attach returns
date: 2026-09-18
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, bubbletea, attach, rendering
---

## Summary
Pressing `enter` in `cspace tui` suspends the dashboard, runs the interactive
`claude` session, and restores the dashboard when the session ends. The
restore is incomplete: bubbletea clears the terminal and then repaints only
the lines that changed while the child owned the screen. Everything else —
the sidebar's vertical rule below the last occupied row, and the footer — is
left blank on a screen that was just erased.

The visible symptoms are that the `attach ok` confirmation is never seen at
all, and that the dashboard keeps a half-drawn frame afterwards.

## Details
Measured on 2026-09-18 against a live `cspace-cspace-dashcheck`, driving the
dashboard through a pty (task 9 of rollout step 3). Before the attach the
program emits 39 sidebar rule characters per full frame; in the ten seconds
after the session ends it emits 7 in total, and the footer row stays empty
for exactly `noticeLifetime` (3 s) before the key hints reappear.

The model is not at fault. Driven directly, `Update(Result("attach", nil))`
leaves `m.action == ""`, `m.notice.text == "attach ok"`, and a 40-line view
whose last line is `attach ok`. The frame is correct; it is not written.

The raw stream on resume is `ESC[?1049h … ESC[H ESC[2J` followed by content
for the occupied rows only. bubbletea's `RestoreTerminal` re-enters the alt
screen and erases the display, but the renderer's model of the physical
screen (`cursedRenderer.scr`, charm.land/bubbletea/v2 v2.0.9) still holds the
pre-suspend frame, so the diff that follows the erase considers the rest of
the screen already correct.

Two workarounds were tried against the live sandbox and both were worse than
the defect, so neither was kept:

- `tea.ClearScreen` batched with the attach result: no change (the erase
  lands before the restore finishes and is undone by it).
- `tea.ClearScreen` deferred behind a 100 ms tick, batched or sequenced: the
  footer then never came back at all.

A fix likely belongs upstream, or needs a full-repaint hook the v2 renderer
does not currently expose. Whatever is tried has to be checked in a real
terminal — every measurement here is through a pty, and the discriminating
question (what a real terminal's alt-screen buffer holds at `?1049h` when the
alt screen is already active) cannot be answered without one.

Everything else about the attach handoff verified clean: the dashboard
suspends, `claude` takes the terminal, `tmux list-clients -t cspace-claude`
shows exactly one tty, `~/.cspace/controlplane/<project>/<sandbox>/` gains
`cspace-claude.<tty>.json` beside `attach.lock`, and leaving the session
removes the record, empties the client list and returns the dashboard in
about 0.6 s.

## Updates
- 2026-09-18: filed from task 9's live verification of rollout step 3.
- 2026-09-18: resolved. The renderer was never the problem. `container exec
  -it` leaves O_NONBLOCK set on the terminal's open file description when it
  exits; cspace shares that description with the child, and the flag lives on
  the description rather than the descriptor, so it outlives the attach
  (confirmed directly: `container exec -it buildkit true` run from a pty
  returns with fd 1 non-blocking). Go does not register the standard
  descriptors with its poller, so the first write that does not fit in the
  tty's buffer comes back short with EAGAIN. bubbletea drops that error
  (`_ = p.renderer.flush(false)`, tea.go:1427) while `cursedRenderer.flush`
  has already recorded the frame as painted — `s.lastView`, and ultraviolet's
  `curbuf` inside `Render` — so the truncated tail is never drawn again and
  the screen keeps a hole for the rest of the session. The restore itself is
  correct and complete: `RestoreTerminal` → `startRenderer` →
  `cursedRenderer.start` → `enterAltScreen` → `scr.Erase()` makes the next
  flush a full `clearUpdate`, i.e. `ESC[H ESC[2J` plus every non-blank row.
  That is why both `tea.ClearScreen` workarounds changed nothing: the repaint
  was issued and then cut off mid-write.

  Reproduced without a sandbox, under a 120x40 pty, with a child that sets
  O_NONBLOCK on fd 1: 14 of 39 sidebar rule rows repainted and the footer
  never painted at all, against 39 of 39 with a well-behaved child.
  `tea.ClearScreen`, a re-emitted `tea.WindowSizeMsg` and
  `tea.RequestWindowSize` batched with the attach result all measured
  byte-identical to no fix.

  Fixed by handing the descriptors back in the mode they were lent out in:
  `restoreBlockingStreams` (internal/cli/child_streams.go), deferred in
  `attachExec.Run` and in `runAttachChild` — so plain `cspace attach` stops
  leaving the shell's stdout non-blocking too. Verified live against
  `cspace-cspace-dashcheck` through the same pty harness: the restore paints
  39 of 39 rule rows and `attach ok` reaches the footer, against 7 rows and
  no notice before.
