---
title: Session discovery loses tmux records separated by literal tabs
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: tui, sessions, tmux, attach
---

## Summary
The new managed-session query used literal tabs in its tmux format, but a
non-UTF-8 tmux client replaced those separators with underscores. The parser
then silently ignored the full line as an unmanaged session name, making the
TUI catalog empty and hiding existing numbered sessions from allocation.

## Details
Observed in the disposable `cspace-compat15-mercury` sandbox during Apple
Container 1.5 validation. Real Claude successfully started and survived the
host client's SIGHUP with zero submitted turns. Its discovery record was:

```text
cspace-claude_$0_1790712000_408_1790712000_
```

The same tmux query with `-u` preserved actual tab separators. A separate
`container exec ... printf 'transport\tcontrol\n'` also preserved the tab,
locating the transformation in tmux rather than the container transport.
This is not established as an Apple Container 1.5 regression: no comparison
with an earlier substrate version was made.

Both session creation and listing now share a printable colon delimiter;
tmux normalizes colons in session names to underscores, and validated identity
fields cannot contain a colon. This also leaves unrelated pipe-containing
session names unambiguous. Tests
model tmux's control-character replacement and cover an empty legacy token.

## Updates
### 2026-09-29 — status: open
Found during live validation. Scoped printable-format fix and regression
tests added; live validation is pending.

### 2026-09-29 — status: resolved
Printable colon format passed focused tests and real CLI/TUI validation:
existing sessions rediscovered, two independent `attach --new` launches
completed, targeted reconnect preserved exact process identities, and the
TUI listed detached sessions on fresh startup. The TUI's New session action
also created and discovered its separate process. The generation guards
were unchanged. Reports: `/private/tmp/cspace-compat15-sessions-20260929-140835/`
and `/private/tmp/cspace-compat15-sessions-20260929-141051/` on the test host.
