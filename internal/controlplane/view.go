package controlplane

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

// View lays out the design's fixed geometry: a 24-column sidebar on the
// left; on the right a tabs line, the main area, and a one-line footer
// across the bottom.
func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		v := tea.NewView("starting cspace tui…")
		v.AltScreen = true
		return v
	}

	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := mainWidthFor(m.width)

	side := styleSidebar.Height(bodyHeight).Render(
		renderSidebar(m.rows, m.live, m.ports, m.selected, bodyHeight))

	// MaxHeight as well as Height: Height only pads, and a detail band with
	// a long event tail would otherwise push the footer off the window.
	main := lipgloss.NewStyle().Width(mainWidth).Height(bodyHeight).MaxHeight(bodyHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			m.tabsLine(mainWidth),
			m.mainArea(mainWidth, bodyHeight-1)))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, side, main),
		m.footer()))
	v.AltScreen = true
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

// tabsLine is the row of pane tabs the design reserves above the main area.
// Step 3 has no panes, so it carries the selection's title on the left and
// daemon health on the right: the line is occupied and the geometry beneath
// it is the one step 4 inherits.
func (m Model) tabsLine(width int) string {
	row := m.selectedRow()
	title := "cspace"
	switch {
	case row.Kind == control.RowProject:
		title = row.Name
	case row.Name != "" && row.Project != "":
		title = row.Project + "/" + row.Name
	case row.Name != "":
		title = row.Name
	}

	health, style := "daemon unreachable", styleErr
	if m.daemon.Reachable {
		health, style = "daemon "+m.daemon.Version, styleDim
	}

	// On a narrow window there is not room for both: shrink the title first
	// (daemon health is the more important of the two to keep intact), then
	// — since styleTabs' one-column padding on each side isn't in that
	// budget — clamp health too if the line still doesn't fit. Both fits
	// land on the plain text before either is styled: fitting an
	// already-rendered string would risk truncating mid-escape-sequence and
	// dropping the style's closing reset, bleeding it into whatever the
	// terminal draws next.
	if ansi.StringWidth(title)+1+ansi.StringWidth(health) > width {
		budget := width - ansi.StringWidth(health) - 1
		if budget < 0 {
			budget = 0
		}
		title = fit(title, budget)
	}
	titleBlock := ansi.StringWidth(title) + 2 // styleTabs' padding, one column each side
	if healthBudget := width - titleBlock - 1; ansi.StringWidth(health) > healthBudget {
		if healthBudget < 0 {
			healthBudget = 0
		}
		health = fit(health, healthBudget)
	}

	// styleTabs pads by one column on each side; account for that so the
	// right-hand text lands on the last column.
	gap := width - ansi.StringWidth(title) - 2 - ansi.StringWidth(health)
	if gap < 1 {
		gap = 1
	}
	return styleTabs.Render(title) + strings.Repeat(" ", gap) + style.Render(health)
}

// mainArea is what sits under the tabs line: the help overlay when it is
// open, the teardown confirmation while it is unanswered, otherwise the
// detail band for the selection.
//
// The confirmation renders here rather than in the footer because a widget's
// height depends on its theme, and the footer is exactly one line. Rollout
// step 4 replaces this with the focused pane and moves the band under the
// sidebar; renderDetail already takes its width, so that is a layout change
// and not a rewrite.
func (m Model) mainArea(width, height int) string {
	style := styleMain.Height(height).MaxHeight(height)
	switch {
	case m.showHelp:
		return style.Render(m.helpView(width - 2))
	case m.mode == modeConfirmDown && m.confirm != nil:
		return style.Render(m.confirm.View())
	}
	row := m.selectedRow()
	k := keyOf(row)
	return style.Render(renderDetail(row, m.live[k], m.ports[k], m.portsErr, m.events, m.eventsErr,
		m.memory[row.Container], width-2))
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
		styleDim.Render(fit("ctrl+c quits from anywhere · esc leaves a prompt", width)),
		styleDim.Render(fit("keys the selected row cannot use are hidden from the footer", width)),
		styleDim.Render(fit("bindings come from tui.keys in ~/.cspace/config.json", width)),
	}
	return strings.Join(lines, "\n")
}

// footer is the one line at the bottom: whatever the dashboard most needs to
// say, and otherwise the short help for what the selection can do.
func (m Model) footer() string {
	switch {
	case m.mode == modeInput:
		return fit("send to "+m.selectedRow().Name+" › "+m.input.View(), m.width)
	case m.action != "":
		return m.spinner.View() + " " + m.action + "…"
	case m.notice.text != "":
		if m.notice.isErr {
			return styleErr.Render(fit(m.notice.text, m.width))
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		return styleErr.Render(fit(fmt.Sprintf("snapshot failed: %v — showing the last poll (%s); run cspace doctor",
			m.snapErr, formatAge(m.lastSnap, m.now())), m.width))
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
}
