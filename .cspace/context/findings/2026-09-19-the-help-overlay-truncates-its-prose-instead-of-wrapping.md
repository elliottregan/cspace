---
title: The help overlay truncates its prose instead of wrapping
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, help, footer, layout
---

## Summary
`helpView` renders seven prose lines through `fit()`, which truncates to
the overlay's width with an ellipsis rather than wrapping. At 80 columns —
a narrow but ordinary terminal width — every one of those lines is cut,
including all four that predate rollout step 5 and the three this branch
added (the mouse/wheel line, the shift-for-terminal-mouse line, and
whichever else falls past the cut). Not a regression this branch
introduced — the truncation behaviour is uniform with the pre-existing
lines — but this branch is what added the content that makes it visible in
the case that matters most: the help overlay is exactly where an operator
goes to learn what the mouse does, and that is one of the lines now cut at
narrow widths.

## Details
`helpView`, `internal/controlplane/view.go:141-160`:
```go
styleDim.Render(fit("mouse: click a row, a tab or the pane; the wheel scrolls both", width)),
styleDim.Render(fit("hold shift for the terminal's own mouse: drag selects, click opens a link", width)),
```
`fit()` (defined elsewhere in the package) truncates to `width` runes with
an ellipsis; it does not wrap. At 80 columns both of the lines above (and
several of the other five) lose their tail.

**The test that would need to change first.**
`TestHelpOverlayNamesTheMouseAndTheSelectionEscapeHatch`
(`internal/controlplane/input_test.go:594-608`) currently asserts the
opposite property on purpose — that **no line is wider than the overlay's
area**:
```go
for i, l := range strings.Split(help, "\n") {
    if w := len([]rune(l)); w > cols {
        t.Errorf("help line %d is %d cells, wider than the %d the overlay is drawn in: %q", i, w, cols, l)
    }
}
```
Switching `helpView` from truncation to wrapping means each logical line
becomes multiple rendered lines, so this assertion (and the loop's
per-line indexing) has to change along with it — the two are coupled, not
independent fixes.

**Failing scenario.** Open the help overlay (`?` / leader Help) in an
80-column terminal. The mouse line and the shift-escape-hatch line are cut
mid-word rather than wrapped to a second line.

Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
Task 7 concerns table: "`helpView` truncates all seven prose lines at 80
cols" — ruled **FILE** ("Uniform with the four lines that predate this
plan, so not a regression — and note for whoever takes it that
`TestHelpOverlayNamesTheMouseAndTheSelectionEscapeHatch` now asserts no
line is wider than the area, so wrapping means changing that assertion
too.").

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review, during the fix
  wave (Important 1, Minor 1).
