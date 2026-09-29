---
title: Footer help can exceed the window when an ellipsis does not fit
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, help, layout
---

## Summary
The help library can render past its configured width when there is no room
for an ellipsis, causing the composed dashboard to widen beyond the terminal.

## Details
`bubbles/v2` v2.2.1 `help.Model.shouldAddItem` returns true after exceeding
its width if the ellipsis also exceeds the width. A 100-column dashboard's
footer reached 117 cells after adding the Details binding. Vertical composition
then padded all body rows to the longer footer width.

## Updates
### 2026-09-29 — status: resolved
The dashboard bounds both sidebar and leader footer help with ANSI-aware
truncation. The view geometry and narrow-window help tests cover the result.
