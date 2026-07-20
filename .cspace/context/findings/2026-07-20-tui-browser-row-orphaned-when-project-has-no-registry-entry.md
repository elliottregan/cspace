---
title: TUI groups the shared browser under its project only while that project has a registry entry
date: 2026-07-20
kind: finding
status: open
category: observation
tags: tui, correlate, browser, grouping
---

## Summary
`Correlate` (internal/tui/correlate.go) derives the set of projects solely from the sandbox registry entries. A running `cspace-<project>-browser` (or an orphaned compose sidecar) whose project has zero registry entries therefore falls through into the dimmed `— system —` rows instead of rendering as a `RowBrowser` under its project header. Surfaced by the general-agent branch's final whole-branch review of the cspace TUI (PR #92, branch `feat/cspace-tui`).

## Details
- Normally harmless: the browser sidecar is ref-counted and torn down with the last sandbox of a project, so a project with a live browser almost always still has a registry entry. The gap only shows if a browser (or sidecar) outlives every registry entry for its project — e.g. a crash that removed the entries but left the container, or a manual `container` start.
- Consequence when it happens: the browser row is not selectable-as-browser (it becomes a non-actionable system row), so `b` (restart) can't target it from the TUI; the user would restart via `cspace browser restart`.
- Fix direction (if wanted): seed `projectNames` from browser/sidecar container names (`cspace-<project>-…`) in addition to registry entries, so a project with only containers still gets its header + browser row. Keep registry entries authoritative for sandbox rows (they carry ControlURL/Token).

## Updates
### 2026-07-20T08:44:02Z — @agent — status: open
Filed from the cspace TUI final whole-branch review (PR #92). Rated minor / acceptable-as-is; not a merge blocker.
