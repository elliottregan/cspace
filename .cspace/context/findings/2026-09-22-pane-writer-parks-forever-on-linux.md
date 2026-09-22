---
title: The pane's writer parks forever on Linux when the child dies with its input full
date: 2026-09-22
kind: finding
status: open
category: bug
tags: control-plane, pane, pty, linux, ci
---

## Summary
`internal/pane`'s teardown is built on one fact measured on macOS: when the
child dies, a `ptmx.Write` parked because the child never read its input
returns. On Linux it does not. The writer goroutine stays in `write(2)`
through SIGHUP, SIGKILL and reaping, `Close` reports `writer did not stop`,
and the goroutine leaks. cspace runs on macOS, so no operator sees this;
CI's ubuntu runners did, from the day the engine landed.

## Details
Measured 2026-09-22 in a Linux container (`golang:1.26-bookworm`, kernel
6.x) against this package's creack/pty master, with the child
`sh -c 'stty raw -echo; sleep 30'` and a 16 MiB flood of 4 KiB writes:

| master fd | after SIGKILL + reap | writer returned |
|---|---|---|
| creack's blocking `*os.File` (the engine today) | 3 s wait | never |
| `dup` + `O_NONBLOCK` + `os.NewFile` (Go's poller), then `Close()` | — | in 9 µs, `ErrClosed` |

The mechanism: a master write goes into the slave's flip buffer; when the
slave never reads, the buffer fills and `pty_write_room` answers 0, and the
writer sleeps on `write_wait`. Closing the slave wakes it, but the flip
buffer is only freed when the *master* closes, so the woken writer sees no
room and sleeps again. A blocking fd outside Go's poller cannot be closed
out from under a parked syscall; a pollable one can — `(*os.File).Close`
on a poller-registered file cancels the parked operation.

`TestPaneDropsInputForAChildThatNeverReads` is skipped off macOS for this
reason (its leaked writer then also failed
`TestPaneCloseIsIdempotentAndLeavesNoGoroutines`, which passes alone).

## Proposed fix
Register the master with Go's poller at open: `dup` the fd creack hands
back, set `O_NONBLOCK`, wrap with `os.NewFile`, close the original. Then
`Close` can release a parked writer or pump on any OS by closing the
descriptor after the child is reaped, and the engine's documented caveat
("ptmx.Close() cannot unblock parked goroutines") goes away with the
Linux-only leak. macOS needs re-measuring under the poller before this
lands (kqueue and pty masters have a history), and the 4a teardown tests
plus `make test-race` are the gate.

## Updates
### 2026-09-22 — status: open
Filed while fixing CI after the control-plane merge; the test is skipped
off macOS rather than the engine changed, because the product is macOS-only
and the fix touches the engine's core teardown right after a release.
