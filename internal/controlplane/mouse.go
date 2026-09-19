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
	if msg.Button != tea.MouseLeft {
		// Middle and right have no meaning here, and inventing one for
		// them is how a stray thumb button tears a sandbox down.
		return m, nil
	}
	// An error notice stays until the next input, and a click is input —
	// the same rule handleKey applies to a keypress.
	if m.notice.isErr {
		m.notice = notice{}
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

	case y == g.tabsY:
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
