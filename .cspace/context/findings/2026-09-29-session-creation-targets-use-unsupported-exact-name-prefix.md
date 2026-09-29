---
title: Session creation uses an exact-name prefix unsupported by tmux option targets
date: 2026-09-29
kind: finding
status: resolved
category: bug
tags: sessions, tmux, attach
---

## Summary
`attach --new` launched a separate Claude process but failed while setting
its identity token: `set-option -t =cspace-claude-2` reported no such session.
The process remained alive, but creation returned an error and no client
attached. Default TUI creation also used the unsupported target prefix for
its final identity query.

## Details
Observed against real tmux in the disposable compat15 sandbox. The new
Claude process had its own `CSPACE_AGENT_STATE_FILE` and no submitted turns.
Read-only probes distinguished the command behavior:

```text
show-options -t =cspace-claude-2 @cspace_id -> no such session
show-options -t cspace-claude-2 @cspace_id  -> invalid option (session found)
display-message -p -t =cspace-claude-2 '#{session_name}' -> empty line
display-message -p -t cspace-claude-2 '#{session_name}'  -> cspace-claude-2
```

The creation command chain now targets its freshly created, validated exact
name without `=`. This runs under the existing sandbox attach lock and
retains create-only semantics. The returned incarnation still includes the
server generation, tmux ID, creation time, and random token; later client
attachment uses the numeric tmux ID and an in-server generation comparison.
No Apple Container version regression is established by this observation.

## Updates
### 2026-09-29 — status: open
Scoped target fix and regression fixture added; real-session retest pending.

### 2026-09-29 — status: resolved
The corrected command chain passed focused control/CLI tests and real
`attach --new` launches of Claude 3 and Claude 4. Both returned complete
incarnations, attached successfully, retained distinct state-file env vars,
and survived detach. The TUI's New session action also passed. The failed
Claude 2 attempt was preserved and only its missing identity metadata was
repaired from its observed state-file token; no session was killed to make
the retest pass. Reports: `/private/tmp/cspace-compat15-sessions-20260929-140835/`
and `/private/tmp/cspace-compat15-sessions-20260929-141051/` on the test host.
