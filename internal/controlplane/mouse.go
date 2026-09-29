package controlplane

import (
	tea "charm.land/bubbletea/v2"
	"github.com/elliottregan/cspace/internal/control"
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
	var clickedError string
	if m.notice.isErr {
		clickedError = m.notice.text
		m.notice = notice{}
	}
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	if m.leaderArmed {
		m.leaderArmed = false
		return m, nil
	}
	if m.hasModal() {
		if m.mode == modeDetails && m.dialog != nil {
			frame, w, h := m.modalFrame()
			lines := m.detailsLines(w)
			from := min(m.modalScroll, max(0, len(lines)-h))
			body := rect{frame.x + 2, frame.y + 3, w, h}
			if body.contains(msg.X, msg.Y) {
				i := from + msg.Y - body.y
				if i < len(lines) && lines[i].action != nil {
					return m.runDialogAction(*lines[i].action)
				}
			}
		}
		return m, nil
	}
	if m.mode != modeNormal {
		return m, nil
	}
	if msg.Y == m.height-1 && msg.X >= 0 && msg.X < m.width && m.action == "" && (clickedError != "" || m.snapErr != nil) {
		next, cmd := m.openDetails(m.activeRow(), true)
		m = next.(Model)
		m.dialog.message = clickedError
		return m, cmd
	}
	g := m.geom
	switch {
	case g.list.contains(msg.X, msg.Y):
		at := msg.Y - g.list.y
		if at >= 0 && at < len(g.navItems) {
			m.focus = focusSidebar
			return m.activateNavigation(g.navItems[at])
		}
		return m, nil
	case g.environment.contains(msg.X, msg.Y):
		return m.openDetails(m.activeRow(), true)
	case g.sidebar.contains(msg.X, msg.Y):
		m.focus = focusSidebar
		m.scrolling, m.scroll = false, 0
		return m, nil
	case g.header.contains(msg.X, msg.Y):
		x, y := msg.X-g.header.x, msg.Y-g.header.y
		for _, l := range g.headerLinks {
			if l.y == y && x >= l.x && x < l.x+l.width {
				if l.details {
					return m.openDetails(m.activeRow(), false)
				}
				return m.openURL(l.url)
			}
		}
	case g.main.contains(msg.X, msg.Y):
		if len(m.tabs) > 0 {
			m.focus = focusMain
			m.scrolling, m.scroll = false, 0
		}
	}
	return m, nil
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
	t := m.tabs[i]
	if t.kind == KindHostShell && m.collapsed["host"] {
		m.toggleNavigation("host")
	}
	for _, n := range m.navigation() {
		if n.tabID == t.id {
			m.selectNavigation(n)
			return m
		}
	}
	if t.project != "" {
		row := control.Row{Project: t.project, Name: t.sandbox}
		for _, id := range []string{"project:" + t.project, containerNavID(row)} {
			if m.collapsed[id] {
				m.toggleNavigation(id)
			}
		}
		for _, n := range m.navigation() {
			if n.tabID == t.id {
				m.selectNavigation(n)
				break
			}
		}
	}
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
	if m.notice.isErr {
		m.notice = notice{}
	}
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
	if m.hasModal() {
		m.scrollModal(-dir * wheelLines)
		return m, nil
	}
	if m.mode != modeNormal {
		// A modal or the overlay owns the screen; there is nothing behind
		// it the person can see to scroll.
		return m, nil
	}

	g := m.geom
	switch {
	case g.sidebar.contains(msg.X, msg.Y):
		// The list is windowed on the selection (navigationWindow), so moving
		// the selection IS scrolling the sidebar — and it is the move the
		// person can act on afterwards, which a detached scroll offset
		// would not be.
		before := m.navID
		m.moveNavigation(-dir)
		if m.navID == before {
			// At either end there is nothing new for the selection to
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
