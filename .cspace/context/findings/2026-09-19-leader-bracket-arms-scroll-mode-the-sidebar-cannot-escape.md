---
title: Leader `[` arms scroll mode the sidebar cannot escape
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, mouse, scroll-mode, focus
---

## Summary
Rollout step 5's `wheelMain` refuses to arm scroll mode unless
`m.focus == focusMain`, because scroll mode is only escapable through
`handlePaneKey`, which the keyboard reaches solely while the main area has
focus. Leader `[`, three screens up in the same file family, has no such
guard — it arms scroll mode regardless of which area is focused. The two
paths into the same mode now disagree about a precondition one of them
added for a real reason.

## Details
`wheelMain`'s guard, `internal/controlplane/mouse.go:241-249`:
```go
if m.focus != focusMain {
    // Scroll mode is escapable only through handlePaneKey, which the
    // keyboard reaches only while the main area has focus. Arming it
    // from here would leave the pane under a banner that says any key
    // returns to live while every key went to the sidebar instead,
    // with leader g the only way out. A wheel is a look: it does not
    // take the focus, so it does not arm a mode that needs it either.
    return m, nil
}
```
Leader `[`'s `Scroll` arm, `internal/controlplane/leader.go:97-110`, has no
equivalent check on `m.focus` — it arms `m.scrolling` whenever the focused
tab's pane has scrollback, whether or not the sidebar is focused.

Pre-existing from rollout step 4b (leader `[` shipped before the wheel
did), but the branch review notes this step is what makes the disagreement
visible: it is the one that gave scroll mode a second, guarded entry point,
which is what exposes the first one as unguarded.

**Failing scenario.** Focus the sidebar (leader s, or a click on a sidebar
row), then press leader `[` while the previously-focused pane still has
scrollback. Scroll mode arms with the sidebar focused; per the banner,
"any other key returns to live" — but with the sidebar focused, every key
goes through the sidebar's own key handling, not `handlePaneKey`, and
leader `g` is the only way out (mirroring the wheel's guard's own
reasoning about what it was protecting against).

Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
Minor 5 ("Leader `[` still arms scroll mode with the sidebar focused") and
adjudication row T3-b, ruled **FILE** — "fix with the same guard."

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review, during the fix
  wave (Important 1, Minor 1).
