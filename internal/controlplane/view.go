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
		return tea.NewView("starting cspace tui…")
	}

	bodyHeight := m.height - 1 // the footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	mainWidth := m.width - sidebarWidth
	if mainWidth < 20 {
		mainWidth = 20
	}

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

	// styleTabs pads by one column on each side; account for that so the
	// right-hand text lands on the last column.
	gap := width - ansi.StringWidth(title) - 2 - ansi.StringWidth(health)
	if gap < 1 {
		gap = 1
	}
	return styleTabs.Render(title) + strings.Repeat(" ", gap) + style.Render(health)
}

// mainArea is the detail band for the selection. Rollout step 4 replaces
// this with the focused pane and moves the band under the sidebar; the
// renderer already takes its width, so that is a layout change, not a
// rewrite.
func (m Model) mainArea(width, height int) string {
	row := m.selectedRow()
	k := keyOf(row)
	body := renderDetail(row, m.live[k], m.ports[k], m.portsErr, m.events, m.eventsErr,
		m.memory[row.Container], width-2)
	return styleMain.Height(height).Render(body)
}

// footer is the one line at the bottom: whatever the dashboard most needs to
// say, and otherwise the short help for what the selection can do.
func (m Model) footer() string {
	switch {
	case m.action != "":
		return m.spinner.View() + " " + m.action + "…"
	case m.notice.text != "":
		if m.notice.isErr {
			return styleErr.Render(fit(m.notice.text, m.width))
		}
		return styleOK.Render(fit(m.notice.text, m.width))
	case m.snapErr != nil:
		return styleErr.Render(fit(fmt.Sprintf("container ls failed: %v — showing the last poll (%s); run cspace doctor",
			m.snapErr, formatAge(m.lastSnap, m.now())), m.width))
	}
	row := m.selectedRow()
	return m.help.ShortHelpView(m.keys.forRow(row, m.live[keyOf(row)]).ShortHelp())
}
