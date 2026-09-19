---
title: ScrollbackView can tear between its Render and its scrollback reads
date: 2026-09-18
kind: finding
status: open
category: bug
tags: control-plane, pane, scrollback, concurrency
---

## Summary
`(*Pane).ScrollbackView` takes two independent snapshots of the emulator —
the live screen via `Render()`, then the history via `Scrollback()` — under
two separate acquisitions of the adapter's lock. The output pump can push a
line out of the screen and into the history between them, so a single
rendered view can show one line twice, or skip one entirely. No data race,
no corruption: a transient visual tear that will read in a capture as a
rendering bug.

## Details
`internal/pane/pane.go:359-366`:

```go
screen := strings.Split(p.emu.Render(), "\n")
sb := p.emu.Scrollback()
history := sb.Len()
```

`vtEmulator.Render` is not taken under `emuMu` (x/vt's `SafeEmulator`
already wraps it), and `vtScrollback.Len`/`Line` take `emuMu` for read one
call at a time — each call is internally consistent, none of them is
consistent with the others. The pane's output pump calls `emu.Write` from
its own goroutine, which takes `emuMu` for write and can scroll a line off
the screen at any point between the reads above.

Two shapes:

- A line scrolls out **after** `Render()` and **before** `sb.Len()`: it is
  in the screen slice and also in the history the view then indexes, so it
  is drawn twice.
- The screen shrinks or a line scrolls in the other direction between the
  `Len()` and the per-line `Line(i)` calls: an index computed from the old
  length reads a line that has since moved, or an out-of-range one, which
  `vtScrollback.Line` deliberately answers with `""` rather than a panic.

Both are single-frame: the next redraw (which the write itself signalled via
`markDirty`) is consistent again. The reason to record it rather than fix it
is that a 4b verification capture is a still frame — a duplicated or missing
line in a scrollback screenshot looks exactly like a viewport arithmetic bug
in the dashboard, and the arithmetic is where anyone would look first.

Fix, if it is ever worth the lock: give `Emulator` one call that returns the
screen and the history length together (the way `CursorPosition` already
returns both coordinates from one call, for precisely this reason — see
`internal/pane/vt.go`'s comment on it), and have `ScrollbackView` use it.
The per-line `Line(i)` calls would still be individually locked, so the
tear would narrow rather than close completely; closing it entirely means
rendering the whole view inside one lock hold.

## Updates
### 2026-09-18 — status: open
Filed from the `control-plane-4a-pane-engine` final review, which adjudicated
it as cosmetic and transient but worth recording before 4b starts capturing
scrollback screens.
