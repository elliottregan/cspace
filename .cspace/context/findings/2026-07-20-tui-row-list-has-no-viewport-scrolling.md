---
title: TUI row list has no viewport/scrolling; overflows a short terminal
date: 2026-07-20
kind: finding
status: open
category: observation
tags: tui, view, viewport, rendering
---

## Summary
The TUI's `View` (internal/tui/view.go) renders all rows unbounded. On a host with many containers or a short terminal window, the row list overflows `m.height` and pushes the detail pane and footer off-screen (under alt-screen the terminal scrolls). `m.height` is captured from `tea.WindowSizeMsg` but never used to clamp or scroll output. Surfaced by the cspace TUI final whole-branch review (PR #92, branch `feat/cspace-tui`).

## Details
- Not required by the v1 plan (scrolling was explicitly out of scope), so it is not a merge blocker — but it is the first thing that will bite on a busy host or a small window.
- Fix direction (if wanted): clamp the rendered rows to the available height (rows region = `m.height` minus header/banner/detail/footer lines) with a scroll offset that follows the selection, or adopt a `bubbles/viewport` for the row list. Keep the detail pane and footer pinned.

## Updates
### 2026-07-20T08:44:02Z — @agent — status: open
Filed from the cspace TUI final whole-branch review (PR #92). Deferred as a v2 follow-up.
