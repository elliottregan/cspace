package controlplane

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// View lays out the design's fixed geometry: a 24-column sidebar on the left
// carrying the row list and, beneath it, the detail band; on the right a
// tabs row, the main area, and a one-line footer across the bottom.
//
// Rollout step 3 put the band in the main area and the selection's title in
// the tabs row, because there were no panes to compete for either. This is
// the move that comment promised.
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("starting cspace tui…")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := mainWidthFor(m.width)

	side := styleSidebar.Height(bodyHeight).Render(m.sidebarColumn(bodyHeight))

	// The main area's own size comes from paneSize, not from bodyHeight-1
	// and mainWidth-2 recomputed here. They are the same arithmetic at any
	// usable window — and at a tiny one they are not, because paneSize
	// floors, so an emulator sized 2 rows would be rendered into 0. One
	// source keeps the pane's geometry and the box it is drawn in equal,
	// which is the property supervisor.view's read-only design rests on.
	paneCols, paneRows := m.paneSize()
	main := lipgloss.NewStyle().Width(mainWidth).Height(bodyHeight).MaxHeight(bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			m.tabsRow(mainWidth),
			styleMain.Render(m.mainArea(paneCols, paneRows))))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, side, main),
		m.footer()))
	v.AltScreen = true

	// Cell motion, not all motion: it reports clicks, releases, the wheel
	// and drags, which is everything the design asks for, and it is the
	// better supported of the two. In bubbletea v2 this is a property of
	// the view — there is no program option and no command — so it is set
	// on every frame, including the starting one above.
	//
	// The cost is the terminal's own selection: with mouse reporting on,
	// a drag belongs to the program. Ghostty and friends still select on
	// shift+drag, and the help overlay says so.
	v.MouseMode = tea.MouseModeCellMotion

	// The cursor belongs to the focused pane and only when the keyboard is
	// pointed at it: a cursor blinking in a pane the keys do not reach is a
	// lie about where typing goes. The help overlay and the modals
	// (mainArea's first three cases) cover the pane while leaving the focus
	// on it, so they have to be excluded too — otherwise the cursor sits on
	// top of the help text at the position of a pane nobody can see.
	if t := m.focusedTab(); t != nil && t.p != nil && m.focus == focusMain &&
		!m.scrolling && !m.showHelp && m.mode == modeNormal {
		if _, _, exited := t.p.Exited(); !exited {
			x, y := t.p.Cursor()
			v.Cursor = tea.NewCursor(x+sidebarWidth+1, y+1)
		}
	}
	return v
}

// mainWidthFor is the main area's width for a given window width: the window
// less the sidebar, floored at 20 columns so a very narrow terminal still
// gets a usable pane. View and any key handler that has to size a widget to
// match the main area (the teardown confirmation) both go through this, so
// they can never disagree about how wide "the main area" is.
func mainWidthFor(width int) int {
	return max(20, width-sidebarWidth)
}

// tabsRow is the row of pane tabs above the main area. With no panes open it
// carries daemon health instead, so the line is never blank — an empty line
// above the main area reads as a rendering bug rather than as a promise.
func (m Model) tabsRow(width int) string {
	health, style := "daemon unreachable", styleErr
	if m.daemon.Reachable {
		health, style = "daemon "+m.daemon.Version, styleDim
	}
	if len(m.tabs) == 0 {
		gap := width - ansi.StringWidth(health) - 1
		if gap < 1 {
			gap = 1
		}
		return strings.Repeat(" ", gap) + style.Render(fit(health, width-gap))
	}
	// With tabs on the row, health moves to the footer's domain: the tabs
	// are what the row is named for and they get all of it.
	return renderTabs(m.tabs, m.focused, width, m.focus == focusMain)
}

// mainArea is what sits under the tabs row: the help overlay when it is
// open, a prompt while it is unanswered, otherwise the focused pane.
func (m Model) mainArea(width, height int) string {
	switch {
	case m.showHelp:
		return fitLines(m.helpView(width), height)
	case m.mode == modeConfirmDown && m.confirm != nil:
		return fitLines(m.confirm.View(), height)
	case m.mode == modePicker && m.picker != nil:
		return fitLines(m.picker.View(), height)
	}
	return m.paneArea(width, height)
}

// leaderLabel is how the leader is spelled on screen. It comes from the
// binding rather than from a literal, because the leader is configurable
// (tui.keys.leader) — every hardcoded "⌃Space" is a line that lies the
// moment somebody rebinds it.
func (m Model) leaderLabel() string {
	if lead := m.keys.Leader.Help().Key; lead != "" {
		return lead
	}
	return strings.Join(m.keys.Leader.Keys(), "/")
}

// helpView is the full binding list, plus the notes the footer has no room
// for. Rendering it in the main area keeps the sidebar visible, so a person
// can read the keys against the row they were about to act on.
//
// m.help is sized to the whole terminal (Update's WindowSizeMsg case calls
// SetWidth with msg.Width), which is wider than the area this renders into —
// so FullHelpView is driven from a local copy sized to the width actually
// passed in, not m.help itself.
func (m Model) helpView(width int) string {
	h := m.help
	h.SetWidth(width)
	lines := []string{
		styleTabs.Render("keys"),
		"",
		h.FullHelpView(m.keys.FullHelp()),
		"",
		styleDim.Render(fit("panes", width)),
		h.FullHelpView(m.keys.PaneFullHelp()),
		"",
		styleDim.Render(fit("every other key goes to the focused pane; "+m.leaderLabel()+" is the leader", width)),
		styleDim.Render(fit("ctrl+c quits, except in a live pane where it goes to the program", width)),
		styleDim.Render(fit("esc leaves a prompt", width)),
		styleDim.Render(fit("keys the selected row cannot use are hidden from the footer", width)),
		styleDim.Render(fit("bindings come from tui.keys in ~/.cspace/config.json", width)),
	}
	return strings.Join(lines, "\n")
}

// sendBoxPrefix is the label the send box wears, and the thing that decides
// how much of the footer line is left for the input itself.
func sendBoxPrefix(name string) string {
	return "send to " + name + " › "
}

// sendLabelMin is the fewest columns the send box's label is shortened to
// before the line is allowed to overflow instead. At three, fit leaves two
// runes and an ellipsis — still recognizably a label rather than part of the
// message.
const sendLabelMin = 3

// sendBoxLine composes the send box's footer line: a plain label, then the
// textinput's own view.
//
// The label is what gives, never the input. m.input.View() is already styled
// ANSI, and fit() counts display cells while cutting runes — so fitting the
// composed line could cut an escape sequence in half or drop the closing
// reset and bleed the style into whatever the terminal draws next (the same
// hazard tabsLine documents). sendInputWidth floors the input at eight
// columns, so on a window narrow enough for that floor to bite, the label
// shrinks past what it would otherwise take, down to sendLabelMin; beyond
// that the line is allowed to be as wide as the floor demands.
func sendBoxLine(label, input string, width int) string {
	budget := width - lipgloss.Width(input)
	if budget < sendLabelMin {
		budget = sendLabelMin
	}
	if lipgloss.Width(label) > budget {
		label = fit(label, budget)
	}
	return label + input
}

// sendInputWidth is how wide the send box's textinput may be: the footer line
// less the label, the input's own two-column "> " prompt, and the one column
// bubbles/v2 renders past Width() for the cursor.
//
// bubbles/v2 needs this set. Its placeholderView builds a rune slice of
// Width()+1, so with the default Width of 0 it renders exactly one character
// of the placeholder — "message" shows as "m", which reads as the keystroke
// that opened the box having leaked into it. A set width also makes a long
// turn scroll horizontally instead of overflowing the line.
func sendInputWidth(name string, windowWidth int) int {
	return max(8, windowWidth-lipgloss.Width(sendBoxPrefix(name))-3)
}

// footer is the one line at the bottom: whatever the dashboard most needs to
// say, and otherwise the short help for what the selection can do.
func (m Model) footer() string {
	switch {
	case m.mode == modeInput:
		// m.pending, not m.selectedRow(): the send box names the sandbox it
		// was opened against, and a poll landing while it is open must not
		// retarget the label out from under the row Send will actually act
		// on (see updateConfirm's identical reasoning for the teardown
		// confirm).
		return sendBoxLine(sendBoxPrefix(m.pending.Name), m.input.View(), m.width)
	case m.action != "":
		line := m.spinner.View() + " " + m.action + "…"
		if m.actionNote != "" {
			// fit() on the composed line, not on the note alone: the
			// spinner frame is a single rune and the label is plain text,
			// so there is no ANSI in front of the truncation point here —
			// unlike the notice arms, which are styled before they are
			// measured.
			line = fit(line+"  "+m.actionNote, m.width)
		}
		return line
	case m.notice.text != "":
		if m.notice.isErr {
			return styleErr.Render(fit(m.notice.text, m.width))
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		return styleErr.Render(fit(fmt.Sprintf("snapshot failed: %v — showing the last poll (%s); run cspace doctor",
			m.snapErr, formatAge(m.lastSnap, m.now())), m.width))
	}
	// The footer names the keys that will actually work, which depends on
	// where the keyboard is pointed: the sidebar's own keys, or — with a
	// pane focused, where every key but the leader goes to the child — the
	// leader's second keys, prefixed by the leader itself.
	if m.focus == focusMain && m.focusedTab() != nil {
		lead := m.leaderLabel()
		// helpView's pattern, for helpView's reason: m.help is sized to the
		// whole window, this line has the leader's label in front of it, and
		// a local copy is how one call gets a different budget without
		// changing the model's.
		//
		// Not fit(): the composed line is already styled ANSI, and fit drops
		// trailing *runes* — the hazard tabsLine and sendBoxLine both
		// document, where an escape sequence is cut in half and its closing
		// reset lost, so the style bleeds into whatever the terminal draws
		// next. help counts cells and elides between bindings, which is what
		// this line wants anyway: LeaderHelp is trimmed to what fits, and
		// anything that still does not is dropped whole rather than sliced.
		h := m.help
		h.SetWidth(max(1, m.width-ansi.StringWidth(lead)-1))
		leadRendered := lead
		if m.leaderArmed {
			// The held prefix gets the accent style so the armed state is
			// visible — the width budget above is still figured from the
			// unstyled label, since the style adds no cells.
			leadRendered = styleOK.Render(lead)
		}
		return leadRendered + " " + h.ShortHelpView(m.keys.LeaderHelp())
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
}
