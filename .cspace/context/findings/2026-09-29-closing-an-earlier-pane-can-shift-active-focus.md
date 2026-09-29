---
title: Delayed close of an earlier pane shifts focus to a different pane
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, panes, focus
---

## Summary
Closing pane A, focusing pane B before A's asynchronous detach completes,
and then applying A's completion could make pane C active.

## Details
`dropTab` kept the focused slice index after removing an earlier element.
It only corrected indexes that exceeded the slice, rather than preserving
the active pane's identity.

## Updates
### 2026-09-29 — status: resolved
`dropTab` decrements the focused index when an earlier pane is removed.
A regression test covers a delayed close while another pane is focused.
