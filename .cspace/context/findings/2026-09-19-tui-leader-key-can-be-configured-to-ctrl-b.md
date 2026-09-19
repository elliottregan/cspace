---
title: The TUI leader key can be configured to ctrl+b, which Claude Code needs
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, panes, keybindings, config
---

## Summary
The dashboard's leader is `Ctrl+Space` by default and overridable through
`tui.keys.leader` in `~/.cspace/config.json`. The design says it "must not
be `Ctrl+b`, which Claude Code uses to background a task"
(`docs/superpowers/specs/2026-09-17-control-plane-design.md`, the leader
paragraph), but that is a default plus a doc comment, not a guard:
`NewKeyMap` (`internal/controlplane/keys.go`) accepts
`"leader": ["ctrl+b"]` and binds it. From then on Ctrl+b arms the leader
instead of reaching the child, and Claude Code inside a pane silently stops
being able to background a task.

## Details
`NewKeyMap(cfg map[string][]string) KeyMap` has no error and no warning
channel — it returns a `KeyMap` and nothing else — so refusing or warning
about a binding would mean changing its signature and threading a warning
out to `cspace tui`'s startup. That is why this is filed rather than fixed
in the plan 4b fix wave: it is a signature change, and the failure is both
self-inflicted (the operator typed the binding) and self-evident (the one
key Claude needs stops reaching it).

Every other binding is equally overridable and none of them has this
problem, because none of the others is a *prefix* — the leader is the only
key that is deliberately withheld from the child.

Two remedies, either acceptable:

- **Refuse with a warning.** Give `NewKeyMap` a second return (a `[]string`
  of warnings, or an error for the refusal) and have `cmd_tui.go` print it
  before the program starts, falling back to the default leader. Costs a
  signature change and one call-site update; the drift test that locks Go
  and `lib/defaults.json` together would need the same.
- **Document it.** Name the constraint where the binding is configured —
  `lib/defaults.json`'s comment and the `?` help overlay's "bindings come
  from tui.keys" line — so an operator choosing a leader is told before
  they choose.

## Updates
### 2026-09-19 — status: open
Filed from the plan 4b whole-branch review's parked-findings adjudication
(Task 4: "Leader != ctrl+b is a default plus a doc comment, not a config
guard" — ruled FILE, not FIX NOW).
