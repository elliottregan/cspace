---
title: Host SIGTERM can leave attach waiting forever before tmux cleanup
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: attach, apple-container, signals, lifecycle
---

## Summary

Apple Container 1.5.0's host `container exec -it` process can remain alive
after SIGTERM without delivering that signal to its guest. `runAttachChild`
forwarded SIGTERM but only applied a force-kill deadline to SIGHUP, so an
attach stopped by PID could wait indefinitely before detaching its tmux client.

## Details

Live validation sent SIGTERM to a cspace attach process; it was still alive
30 seconds later. SIGHUP completed through the existing two-second fallback.
A separate disposable container reproduced the underlying transport behavior:
direct `container exec -it` remained alive five seconds after host SIGTERM,
and a trapping guest shell wrote no TERM acknowledgment. Killing and reaping
only that host exec allowed cleanup of the disposable container afterward.

This is the bounded-shutdown gap in the lifecycle described by
`2026-09-17-attach-orphans-claude-when-the-host-terminal-closes`: the host child
must finish before cspace can explicitly detach its guest tmux client.

## Updates

### 2026-09-29 — status: open

Reproduced with Apple Container 1.5.0 in an isolated disposable container.
No managed Claude sessions were involved in the transport reproduction.

### 2026-09-29 — status: resolved

SIGTERM and SIGHUP now forward their original signal and share one two-second
host-child kill deadline. Repeated shutdown signals cannot extend it, and the
timer stops when the child exits. This lets the existing attach cleanup detach
its guest client without terminating the tmux session. SIGINT remains unchanged.
Subprocess tests exercise both shutdown signals against a child ignoring them.

### 2026-09-29 — live verification

With the rebuilt CLI on Apple Container 1.5.0, SIGTERM completed in 2.093
seconds. The original Claude process and tmux session identity survived;
its attached-client count and host client-record count both returned to zero.
