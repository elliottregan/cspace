---
title: The wheel is the only input that cannot dismiss an error notice
date: 2026-09-19
kind: finding
status: open
category: observation
tags: control-plane, dashboard, mouse, notice, footer
---

## Summary
`handleClick` clears `m.notice` unconditionally, above every button guard,
on the reasoning that any input dismisses a sticky notice. `handleWheel`
does not clear it at all, so wheeling the sidebar's selection down (or the
main pane's scrollback) leaves an error notice on screen that belongs to a
row or action two notches back — the only input surface in the dashboard
that cannot clear the footer's error state.

## Details
`handleClick`, `internal/controlplane/mouse.go:17-25` — notice is cleared
before any button/mode guard runs:
```go
func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.notice.isErr {
		m.notice = notice{}
	}
	if msg.Button != tea.MouseLeft {
```
vs. `handleWheel`, `internal/controlplane/mouse.go:148-172`, which routes
straight into `moveSelection`/`wheelMain` with no equivalent
`m.notice = notice{}`.

A second, related gap the same review paragraph raises: `wheelMain`
(`internal/controlplane/mouse.go:205-249`) can itself *write* a notice
while an action is in flight (`noScrollbackNotice()` at line ~229), and
`footer()` ranks the action spinner above the notice — so a notice written
mid-action surfaces only later, on whichever arm sets no notice of its own
(`paneOpenedMsg` success, `paneClosedMsg` success, `pasteMsg`'s tab-closed
branch). This shape predates rollout step 5 (leader `[` could already
write a notice this way); the wheel is simply a second writer into the
same pre-existing gap.

**Failing scenario.** Trigger any sticky error notice (a failed paste, a
failed close), then scroll the sidebar with the wheel instead of clicking
or pressing a key. The error notice stays on screen, now describing a
state the selection has moved past, until something else eventually
overwrites it.

Source:
`.superpowers/sdd/2026-09-19-control-plane-5-mouse-and-image-paste/review-branch.md`,
Minor 3 ("A wheel notch cannot dismiss a sticky error notice") and
adjudication row T3-a, ruled **FILE**.

## Updates
- 2026-09-19: filed from the plan 5 whole-branch review, during the fix
  wave (Important 1, Minor 1).
