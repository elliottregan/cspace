---
title: TUI row list has no viewport/scrolling; overflows a short terminal
date: 2026-07-20
kind: finding
status: resolved
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

### 2026-09-18 — status: resolved
Closed by the rollout step 3 dashboard (`internal/controlplane`), which
replaces the v1 view this was filed against. `renderSidebar` renders exactly
the number of lines the layout gives it, choosing the window with
`sidebarWindow` — a pure function of the selection rather than a remembered
offset, since rows are rebuilt from scratch on every poll and a carried
offset would drift against a list that grew or shrank underneath it. The
detail band and the footer are laid out at fixed heights beside it, so
neither can be pushed off a short terminal.
