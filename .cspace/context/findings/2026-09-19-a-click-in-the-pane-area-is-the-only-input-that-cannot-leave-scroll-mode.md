---
title: A click in the pane area is the only input that cannot leave scroll mode
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, mouse, scroll-mode
---

## Summary
Once scroll mode is armed (leader `[` or a wheel notch), every documented
way out works except one: clicking the pane itself. Leader `g`, a click on
a list row, a click elsewhere in the sidebar, a click on a tab, and any
keypress all clear `m.scrolling`/`m.scroll` — a click inside the main pane
area does not, so a person trying to "get back to typing" by clicking the
pane gets nothing, and the *next* key they press is the one scroll mode
eats as its own exit.

## Details
`handleClick`'s main-area arm, `internal/controlplane/mouse.go:86-94`:

```go
case g.main.contains(x, y):
    if len(m.tabs) == 0 {
        return m, nil
    }
    m.focus = focusMain
    return m, nil
```

It sets focus but never touches `m.scrolling`/`m.scroll`. Every other exit
from scroll mode does:
- leader `g` — `internal/controlplane/leader.go:114-115`
- a click on a list row — `selectListRow`, `internal/controlplane/mouse.go:111-125` (clears at :113)
- a click elsewhere in the sidebar — `internal/controlplane/mouse.go:61-66`
- a click on a tab — `focusTab`, `internal/controlplane/mouse.go:136`
- any key — `handlePaneKey`'s `default` arm

The scroll banner does say "any other key returns to live", so there is a
documented way out — but a click reads as an equally reasonable one, and it
silently does nothing (worse: it arms the *next* keypress as the exit,
which is surprising the first time).

**Failing scenario.** Focus a tmux-backed pane with scrollback, wheel up
(or leader `[`) to arm scroll mode, then click inside the pane to resume
typing. Nothing happens; the click is swallowed by the `focus = focusMain`
no-op, and the very next character typed is consumed as the "return to
live" key instead of reaching the child. Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
Minor 4 ("A click in the pane area neither scrolls nor leaves scroll
mode") and adjudication row T2-b, ruled **FILE**.

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review, during the fix
  wave (Important 1, Minor 1).
