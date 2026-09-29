---
title: Enter on a stopped container silently collapses an empty group
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: control-plane, dashboard, sessions, keyboard
---

## Summary
Enter on a stopped sandbox appeared to do nothing, leaving the user unable to
tell why a new agent session could not open.

## Details
The sidebar only offers New session under running containers. The Enter
handler attached to running containers, but a stopped container fell through
to the heading's collapse action. With no child sessions the only visible
change was the disclosure arrow. Observed after Apple Container restarted
with Mercury and Venus stopped.

## Updates
### 2026-09-29 — status: resolved
Enter on a stopped container now shows its name and the configured boot key.
The empty pane also explains the boot prerequisite and replacement semantics
instead of advertising attach and shell actions that cannot run. The existing
boot action remains explicit. Tests cover stopped-container
feedback, custom boot bindings, and Enter creating an independent session
under a running container. Documentation explains the boot prerequisite and
the existing re-provisioning semantics.
