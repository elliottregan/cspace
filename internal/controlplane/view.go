package controlplane

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// View renders the navigation and active pane, then places dialogs above them.
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("starting cspace tui…")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	bodyHeight := max(1, m.height-1)
	sw := sidebarWidthFor(m.width)
	side := styleSidebar.Width(sw).MaxWidth(sw).Height(bodyHeight).Render(m.sidebarColumn(bodyHeight))
	paneCols, paneRows := m.paneSize()
	mainWidth := mainWidthFor(m.width)
	main := lipgloss.NewStyle().Width(mainWidth).Height(bodyHeight).MaxHeight(bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left, styleMain.Render(m.planHeader(paneCols).text), styleMain.Render(m.paneArea(paneCols, paneRows))))
	content := lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinHorizontal(lipgloss.Top, side, main), m.footer())
	if m.hasModal() {
		box, frame := m.modalView()
		content = lipgloss.NewCompositor(lipgloss.NewLayer(content), lipgloss.NewLayer(box).X(frame.x).Y(frame.y).Z(1)).Render()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if t := m.focusedTab(); t != nil && t.p != nil && m.focus == focusMain && !m.scrolling && !m.hasModal() && m.mode == modeNormal {
		if _, _, exited := t.p.Exited(); !exited {
			x, y := t.p.Cursor()
			v.Cursor = tea.NewCursor(x+sw+1, y+2)
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
	return max(20, width-sidebarWidthFor(width))
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

// helpView wraps the complete binding list and interaction notes. The dialog
// supplies its own scrolling so no binding disappears at narrow widths.
func (m Model) helpView(width int) string {
	var lines []string
	lines = append(lines, "Navigation", "")
	for _, group := range m.keys.FullHelp() {
		for _, b := range group {
			if !b.Enabled() {
				continue
			}
			h := b.Help()
			lines = append(lines, h.Key+"  "+h.Desc)
		}
	}
	lines = append(lines, "", "Panes · leader "+m.leaderLabel()+" then a key", "")
	for _, group := range m.keys.PaneFullHelp() {
		for _, b := range group {
			if !b.Enabled() {
				continue
			}
			h := b.Help()
			lines = append(lines, h.Key+"  "+h.Desc)
		}
	}
	lines = append(lines, "", "Every other key goes to the focused pane.", "Mouse: click a session to attach, a container to collapse, or a header link to open it.", "Hold shift for the terminal's own mouse: drag selects, click opens a link.", "ctrl+c quits, except in a live pane where it goes to the program.", "Escape closes dialogs. Arrows and the wheel scroll; Tab selects dialog links and actions.", "Bindings come from tui.keys in ~/.cspace/config.json.")
	return ansi.Hardwrap(strings.Join(lines, "\n"), max(1, width), true)
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
// reset and bleed the style into whatever the terminal draws next . sendInputWidth floors the input at eight
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
			return errorFooter(m.notice.text, m.width)
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		text := fmt.Sprintf("snapshot failed: %v · last success %s", m.snapErr, formatAge(m.lastSnap, m.now()))
		if strings.Contains(strings.ToLower(m.snapErr.Error()), "xpc connection") {
			text = "Apple Container unavailable; check container system status"
		}
		return errorFooter(text, m.width)
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
		// trailing *runes* — the hazard sendBoxLine documents, where an escape sequence is cut in half and its closing
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
		return ansi.Truncate(leadRendered+" "+h.ShortHelpView(m.keys.LeaderHelp()), m.width, "…")
	}
	row := m.selectedRow()
	h := m.help
	h.SetWidth(m.width)
	return ansi.Truncate(h.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp()), m.width, "…")
}

func errorFooter(text string, width int) string {
	// Command errors may contain newlines; keep the footer one line and make
	// the wrapped original available in Environment Details.
	text = strings.Join(strings.Fields(text), " ")
	hint := " · click for details"
	if width <= ansi.StringWidth(hint) || (ansi.StringWidth(text) <= width && ansi.StringWidth(text+hint) > width) {
		return styleErr.Render(fit(text, width))
	}
	return styleErr.Render(fit(text, width-ansi.StringWidth(hint)) + hint)
}
