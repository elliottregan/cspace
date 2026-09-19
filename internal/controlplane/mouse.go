package controlplane

import (
	tea "charm.land/bubbletea/v2"
)

// The mouse, hit-tested against Model.geom.
//
// Nothing here reaches a child. The design forwards no mouse event to the
// pane (spec Non-goals), and the sandbox's own tmux is configured `mouse
// off` besides — so a pane's mouse is the dashboard's, and that is the
// whole of the rule: there is no code path from a mouse message to
// Pane.SendKey.

// handleClick routes a left-button press. Every branch either acts on the
// dashboard or does nothing; the release that follows is dropped in Update.
func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	// An error notice stays until the next input, and a click is input —
	// the same rule handleKey applies to a keypress. Above the button
	// guard, because dismissing what the footer is saying is the one thing
	// a click means whichever button made it: handleKey clears on EVERY
	// key, and a right-click that left a stale error in the footer would be
	// the only input in the dashboard that cannot dismiss one.
	if m.notice.isErr {
		m.notice = notice{}
	}
	if msg.Button != tea.MouseLeft {
		// Beyond that, middle and right have no meaning here, and
		// inventing one for them is how a stray thumb button tears a
		// sandbox down.
		return m, nil
	}

	if m.leaderArmed {
		// A half-typed chord. The click is not its second key, and leaving
		// it armed would send the *next* ordinary key somewhere the person
		// did not ask for. Disarm and swallow.
		m.leaderArmed = false
		return m, nil
	}
	if m.showHelp {
		// The overlay swallows the very next input whatever it is, which
		// is exactly what handleKey does with a key. Dismiss and swallow.
		m.showHelp = false
		return m, nil
	}
	if m.mode != modeNormal {
		// The send box, the teardown confirmation and the new-pane picker
		// are unanswered questions holding state. A click is not an answer
		// and must not throw one away, so it is swallowed WITHOUT
		// dismissing: esc still cancels, and the form keeps what was typed.
		return m, nil
	}

	g := m.geom
	x, y := msg.X, msg.Y
	switch {
	case g.list.contains(x, y):
		return m.selectListRow(y - g.list.y)

	case g.sidebar.contains(x, y):
		// The vertical rule, the detail band, the padding under a short
		// list: still the sidebar, so point the keyboard at it — but there
		// is no row under the pointer to select.
		m.focus = focusSidebar
		m.scrolling, m.scroll = false, 0
		return m, nil

	case y == g.tabsY && x >= g.main.x:
		// Bounded on x as well as y. The sidebar arms above already claim
		// every column left of the main area on this row, so today the
		// bound changes nothing — but tabsY is 0 only because the tabs row
		// happens to be the top line, and a future layout that moves it,
		// or gives the sidebar a header, would otherwise have this arm
		// silently claiming the whole screen row.
		for _, s := range g.tabs {
			if x >= s.from && x < s.to {
				return m.focusTab(s.index), nil
			}
		}
		// An elision marker, the empty end of the row, or the daemon
		// health line that stands in for the tabs while none are open.
		// None of them is a tab, so none of them does anything.
		return m, nil

	case g.main.contains(x, y):
		if len(m.tabs) == 0 {
			// The same refusal the focusMain binding makes: there is
			// nothing to point the keyboard at, and a focus that renders
			// no cursor and takes no keys is a focus nobody can see.
			return m, nil
		}
		m.focus = focusMain
		return m, nil
	}
	// The footer, and anything a future layout leaves uncovered.
	return m, nil
}

// selectListRow moves the selection to whatever the clicked line of the row
// list belongs to.
//
// A line that belongs to no row (the "— system —" divider, the padding
// below the last one) and a row that cannot be selected (a project header,
// a sidecar) leave the selection where it was. The click still points the
// keyboard at the sidebar: that much the person did ask for by clicking in
// it.
//
// A sandbox's port lines carry their sandbox's index, so clicking a URL
// selects the sandbox it belongs to rather than nothing.
func (m Model) selectListRow(line int) (tea.Model, tea.Cmd) {
	m.focus = focusSidebar
	m.scrolling, m.scroll = false, 0
	if line < 0 || line >= len(m.geom.listRows) {
		return m, nil
	}
	idx := m.geom.listRows[line]
	if idx < 0 || idx >= len(m.rows) || !m.rows[idx].Selectable || idx == m.selected {
		return m, nil
	}
	m.selected = idx
	// The same re-read j/k do: the detail band's event tail belongs to the
	// selection, and without this it would keep showing the old row's.
	return m, m.eventsCmd()
}

// focusTab points the keyboard at one tab by index. It is what the leader's
// n/p do once they have worked out which tab they mean, and what a click on
// a tab does directly.
func (m Model) focusTab(i int) Model {
	if i < 0 || i >= len(m.tabs) {
		return m
	}
	m.focused = i
	m.focus = focusMain
	m.scrolling, m.scroll = false, 0
	return m
}

// wheelLines is how far one notch moves a scrollback or a viewport. Three
// is what a terminal's own wheel does; one line per notch makes reading a
// long pane feel broken, and a page per notch overshoots.
const wheelLines = 3

// handleWheel routes one notch. It never moves the focus: a wheel is a
// look, not a commitment, and a person reading the sidebar while typing
// into a pane should stay typing into the pane.
func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	var dir int
	switch msg.Button {
	case tea.MouseWheelUp:
		dir = 1 // toward older output, toward the row above
	case tea.MouseWheelDown:
		dir = -1
	default:
		// MouseWheelLeft / MouseWheelRight: nothing here scrolls sideways.
		return m, nil
	}
	if m.leaderArmed {
		// A half-typed chord, same hazard handleClick disarms for a click:
		// the notch is not its second key, and leaving it armed sends the
		// next ordinary key wherever the chord's second key means —
		// including past scroll mode's own "any other key returns to live"
		// banner, opening the new-pane picker or closing a tab under it.
		m.leaderArmed = false
		return m, nil
	}
	if m.mode != modeNormal || m.showHelp {
		// A modal or the overlay owns the screen; there is nothing behind
		// it the person can see to scroll.
		return m, nil
	}

	g := m.geom
	switch {
	case g.sidebar.contains(msg.X, msg.Y):
		// The list is windowed on the selection (sidebarWindow), so moving
		// the selection IS scrolling the sidebar — and it is the move the
		// person can act on afterwards, which a detached scroll offset
		// would not be.
		before := m.selected
		m.moveSelection(-dir)
		if m.selected == before {
			// At either end there is nothing new for the detail band to
			// follow, and a trackpad fires notches far faster than key
			// repeat — the click path (selectListRow) already skips the
			// read for the same reason.
			return m, nil
		}
		return m, m.eventsCmd()

	case g.main.contains(msg.X, msg.Y):
		return m.wheelMain(dir)
	}
	return m, nil
}

// wheelMain is the wheel over the main area: the focused pane's scrollback,
// or the supervisor view's viewport.
//
// Nothing is forwarded to the child. A pane whose history lives inside the
// child — every tmux-backed one, which is every sandbox pane — gets the
// same refusal leader [ gives, rather than silence that reads as a dropped
// event.
func (m Model) wheelMain(dir int) (tea.Model, tea.Cmd) {
	t := m.focusedTab()
	if t == nil {
		return m, nil
	}
	if t.sup != nil {
		// Not a pane and not scrollback: the supervisor view is a viewport
		// over the event tail, and pgup/pgdn already move it. The wheel is
		// the same gesture with a smaller step.
		if dir > 0 {
			t.sup.vp.ScrollUp(wheelLines)
		} else {
			t.sup.vp.ScrollDown(wheelLines)
		}
		return m, nil
	}
	if t.p == nil {
		return m, nil
	}
	if t.p.ScrollbackLen() == 0 {
		// Checked before the direction, so a wheel either way over a
		// tmux-backed pane says the same thing. Silence in one direction
		// and an explanation in the other would read as a bug in the
		// explanation.
		m.notice = noScrollbackNotice()
		return m, nil
	}
	if m.scrolling {
		m.scroll = clampScroll(m.scroll+dir*wheelLines, t.p.ScrollbackLen())
		return m, nil
	}
	if dir < 0 {
		// Live already: there is nowhere below the live screen to go, and
		// arming scroll mode to sit at offset 0 would swallow the next key.
		return m, nil
	}
	if m.focus != focusMain {
		// Scroll mode is escapable only through handlePaneKey, which the
		// keyboard reaches only while the main area has focus. Arming it
		// from here would leave the pane under a banner that says any key
		// returns to live while every key went to the sidebar instead,
		// with leader g the only way out. A wheel is a look: it does not
		// take the focus, so it does not arm a mode that needs it either.
		return m, nil
	}
	// Exactly what leader [ does, plus the notch that asked for it.
	m.scrolling = true
	m.scroll = clampScroll(wheelLines, t.p.ScrollbackLen())
	return m, nil
}
