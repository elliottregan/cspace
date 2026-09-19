---
title: A paste delivered to a tab clicked away from is silent
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, paste, clipboard, mouse, notice
---

## Summary
`pasteMsg`'s success arm routes the result by tab identity, which is
correct — but it then clears the footer's notice on the theory that "the
report is the path now sitting in the pane." If the operator clicked
another tab while `osascript` was running (a single round trip, but not
instantaneous), that pane is off screen: the path lands correctly, but
there is no report anywhere the operator is looking. It reads as leader `v`
having done nothing.

## Details
`internal/controlplane/model.go:566-587`, the `pasteMsg` success arm:

```go
t, _ := m.tabByID(msg.id)   // by identity, never by position — correct
...
t.p.Paste(msg.text)
// This arm is the action's whole report, and the report is the
// path now sitting in the pane. Anything the footer was carrying
// before belongs to something else...
m.notice = notice{}
```

`tabByID` (`internal/controlplane/panes.go:284-292`) is deliberately
identity-based so a tab closing while the clipboard is being read cannot
misdeliver into whatever moved into that index — that part is right, and
the branch review's cross-trace confirms it. The gap is narrower: routing
by identity means the *delivery* is always correct, but nothing checks
whether the tab the paste was for is the tab currently on screen
(`m.focused`). When it is not, `m.notice = notice{}` throws away the one
chance to say "pasted into <tab title>" and the operator sees a footer
that looks unchanged.

**Failing scenario.** Press leader `v` on tab A, then click over to tab B
before the AppleScript round trip (`clipboardTimeout`, up to 10s, typically
well under a second) completes. The PNG path is typed into tab A correctly
— but tab B is what's on screen, its footer shows no report, and the
operator has no way to tell the paste succeeded without switching back.

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review's Minor 2 ("A
  successful paste into a tab the operator has clicked away from is
  completely silent"), ruled **FILE** ("give the delivered-but-not-focused
  case a short timed notice naming the tab"), during the fix wave
  (Important 1, Minor 1). Source:
  `.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`.
