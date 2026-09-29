---
title: Failed container discovery hides independent daemon health and error context
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, errors, daemon
---

## Summary
When Apple Container is unavailable at dashboard startup, the daemon indicator
incorrectly stays unreachable even if cspace's registry daemon responds. The
footer can truncate the command's error before the useful XPC failure reason.

## Details
The UI returned from snapshot application on a container-list error before
applying independently queried daemon health. Browser and BuildKit indicators
could also retain stale state after subsequent failures. The snapshot's early
missing-dependency paths skipped the daemon query entirely.

Observed with Apple Container 1.5.0 after its system service was no longer
registered with launchd. The cspace rc.49 daemon was healthy. Starting the
Apple Container service restored listing of eight stopped containers. Planet
suggestion still worked because it reads registry reservations independently.

## Updates
### 2026-09-29 — status: resolved
Daemon health is probed and applied independently of container discovery;
failed discovery retains rows and marks dependent environment indicators
unknown. Error footer clicks open the complete wrapped message in Environment
Details, including snapshot age. Regression tests cover startup failure,
changing daemon health, full errors at 80/100/140 columns, focus restoration,
and modal isolation.
