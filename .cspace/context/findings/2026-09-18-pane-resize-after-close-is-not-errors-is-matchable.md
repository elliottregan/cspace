---
title: Resize after Close returns an error no caller can match with errors.Is
date: 2026-09-18
kind: finding
status: open
category: bug
tags: control-plane, pane, errors
---

## Summary
`(*Pane).Resize` on a closed pane fails with Go's internal
`poll.ErrFileClosing` ("use of closed file"), returned unwrapped by
`RawConn.Control`. It is not `errors.Is(err, os.ErrClosed)`-matchable and
carries no sentinel of this package's own, so the only way to tell "this
pane's pty is gone" from any other ioctl failure is to compare the error
text — which is what `Resize`'s own doc comment currently tells the caller
to do.

## Details
`internal/pane/pane.go:321-334` (`setWinsize`) reaches the descriptor
through `f.SyscallConn()` and `conn.Control(...)`. That is deliberate and
correct — `(*os.File).Fd()` bypasses the incref/decref pair that protects a
syscall from a concurrent `Close`, and `-race` caught the resulting race
against `poll.FD.destroy()` — but `Control` on a closed file returns
`poll.ErrFileClosing` directly, not wrapped in a `*fs.PathError`, and the
`os` package's `ErrClosed` mapping happens in the wrapper it never builds.

`Resize`'s doc says so explicitly:

> That specific error is Go's internal/poll.ErrFileClosing ("use of closed
> file"): RawConn.Control returns it unwrapped, not behind a *fs.PathError,
> so it is NOT errors.Is(err, os.ErrClosed)-matchable — a caller that needs
> to tell "pty gone" apart from any other ioctl failure has to compare the
> error text.

This matters in 4b, which fans a window resize out over every open pane and
must not let one failure stop the rest. A caller that wants to log a real
ioctl failure while staying quiet about a pane that closed underneath it has
no clean predicate; string matching against an unexported error's message is
a trap, because the message is not part of any compatibility promise.

`TestPaneResizeAfterCloseReturnsAnError` (`internal/pane/pane_test.go:516`)
asserts only that the error is non-nil, so nothing pins the current shape
either way.

Fix (small): in `setWinsize`, map the closed-file case onto the standard
sentinel before returning — `if errors.Is(err, os.ErrClosed) || err.Error()
== "use of closed file" { return fmt.Errorf("pane: resize: %w",
os.ErrClosed) }` — or, cleaner, have `Resize` check the pane's own closed
state first and return a package sentinel (`pane.ErrClosed`) without
touching the descriptor at all. Then update `Resize`'s doc and give the test
an `errors.Is` assertion.

## Updates
### 2026-09-18 — status: open
Filed from the `control-plane-4a-pane-engine` final review, which adjudicated
it as a finding: the behaviour is accurately documented today, but a doc
comment that says "compare the error text" is the trap worth recording
before 4b writes the resize fan-out that would have to do it.
