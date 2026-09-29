package controlplane

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/planets"
)

type navigationKind int

const (
	navProject navigationKind = iota
	navContainer
	navSession
	navPane
	navNewSession
	navNewContainer
	navHost
)

type navigationItem struct {
	id       string
	kind     navigationKind
	row      control.Row
	rowIndex int
	session  control.Session
	tabID    int
	label    string
	depth    int
}

func containerNavID(r control.Row) string { return "container:" + r.Project + "/" + r.Name }
func sessionNavID(r control.Row, s control.Session) string {
	return containerNavID(r) + "/session:" + s.ID + ":" + s.Name
}
func paneNavID(id int) string { return "pane:" + strconv.Itoa(id) }

func (m Model) navigation() []navigationItem {
	var items []navigationItem
	rows := append([]control.Row(nil), m.rows...)
	for _, t := range m.tabs {
		if t.kind == KindHostShell {
			continue
		}
		found := false
		for _, row := range rows {
			if row.Kind == control.RowSandbox && row.Project == t.project && row.Name == t.sandbox {
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, control.Row{Kind: control.RowSandbox, Project: t.project, Name: t.sandbox, State: control.StateStopped})
		}
	}
	projects := []string{}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Project != "" && !seen[r.Project] {
			seen[r.Project] = true
			projects = append(projects, r.Project)
		}
	}
	if m.project != "" && !seen[m.project] {
		projects = append(projects, m.project)
	}
	for _, project := range projects {
		pid := "project:" + project
		items = append(items, navigationItem{id: pid, kind: navProject, row: control.Row{Kind: control.RowProject, Project: project, Name: project}, rowIndex: -1, label: project})
		if m.collapsed[pid] {
			continue
		}
		for ri, r := range rows {
			if r.Kind != control.RowSandbox || r.Project != project {
				continue
			}
			if ri >= len(m.rows) {
				ri = -1
			}
			cid := containerNavID(r)
			items = append(items, navigationItem{id: cid, kind: navContainer, row: r, rowIndex: ri, label: r.Name, depth: 1})
			if m.collapsed[cid] {
				continue
			}
			represented := map[int]bool{}
			for _, s := range m.sessions[keyOf(r)] {
				item := navigationItem{id: sessionNavID(r, s), kind: navSession, row: r, rowIndex: ri, session: s, label: s.Label(), depth: 2}
				for _, t := range m.tabs {
					if t.kind == KindClaude && t.attachable() && t.project == r.Project && t.sandbox == r.Name && sessionMatches(t.session, s) {
						item.tabID = t.id
						represented[t.id] = true
						break
					}
				}
				items = append(items, item)
			}
			for _, t := range m.tabs {
				if t.project != r.Project || t.sandbox != r.Name || represented[t.id] {
					continue
				}
				label := t.kind.String()
				if t.kind == KindClaude {
					label = "Claude"
					if t.session.Name != "" {
						label = t.session.Label()
					}
				}
				items = append(items, navigationItem{id: paneNavID(t.id), kind: navPane, row: r, rowIndex: ri, tabID: t.id, label: label, depth: 2})
			}
			if r.State == control.StateRunning || r.State == control.StateDegraded {
				items = append(items, navigationItem{id: cid + "/new", kind: navNewSession, row: r, rowIndex: ri, label: "+ New session", depth: 2})
			}
		}
		items = append(items, navigationItem{id: pid + "/new", kind: navNewContainer, row: control.Row{Kind: control.RowProject, Project: project, Name: project}, rowIndex: -1, label: "+ New container", depth: 1})
	}
	var hosts []navigationItem
	for _, t := range m.tabs {
		if t.kind == KindHostShell {
			hosts = append(hosts, navigationItem{id: paneNavID(t.id), kind: navPane, rowIndex: -1, tabID: t.id, label: fmt.Sprintf("Shell %d", t.id), depth: 1})
		}
	}
	if len(hosts) > 0 {
		items = append(items, navigationItem{id: "host", kind: navHost, rowIndex: -1, label: "Host"})
		if !m.collapsed["host"] {
			items = append(items, hosts...)
		}
	}
	return items
}

func sessionMatches(s1, s2 control.Session) bool {
	if s1.ID != "" || s2.ID != "" {
		return s1.ID == s2.ID && s1.Name == s2.Name
	}
	return s1.Name == s2.Name && s1.Name != "" || s1.Name == "" && s2.Name == control.SessionClaude
}

func (m Model) navigationIndex(items []navigationItem) int {
	for i, n := range items {
		if n.id == m.navID {
			return i
		}
	}
	for i, n := range items {
		if n.kind == navContainer && n.rowIndex == m.selected {
			return i
		}
	}
	return 0
}
func (m Model) selectedNavigation() (navigationItem, bool) {
	items := m.navigation()
	if len(items) == 0 {
		return navigationItem{}, false
	}
	return items[m.navigationIndex(items)], true
}
func (m *Model) selectNavigation(n navigationItem) {
	m.navID = n.id
	if n.rowIndex >= 0 {
		m.selected = n.rowIndex
	}
	m.scrolling, m.scroll = false, 0
}
func (m *Model) moveNavigation(dir int) {
	items := m.navigation()
	if len(items) == 0 {
		return
	}
	i := max(0, min(len(items)-1, m.navigationIndex(items)+dir))
	m.selectNavigation(items[i])
}
func (m *Model) toggleNavigation(id string) {
	next := make(map[string]bool, len(m.collapsed)+1)
	for k, v := range m.collapsed {
		next[k] = v
	}
	next[id] = !next[id]
	m.collapsed = next
}
func (m Model) activateNavigation(n navigationItem) (tea.Model, tea.Cmd) {
	m.selectNavigation(n)
	switch n.kind {
	case navProject, navHost, navContainer:
		m.toggleNavigation(n.id)
		return m, m.eventsCmd()
	case navSession:
		if n.tabID > 0 {
			_, i := m.tabByID(n.tabID)
			return m.focusTab(i), nil
		}
		if m.action != "" {
			return m, nil
		}
		return m.openSession(n.row, control.AttachRequest{Session: n.session.Name, ExpectedID: n.session.ID})
	case navPane:
		_, i := m.tabByID(n.tabID)
		return m.focusTab(i), nil
	case navNewSession:
		if m.action != "" {
			return m, nil
		}
		return m.openSession(n.row, control.AttachRequest{New: true})
	case navNewContainer:
		if m.action != "" {
			return m, nil
		}
		return m.beginCreate(n.row.Project)
	}
	return m, nil
}

func sidebarWidthFor(width int) int {
	if width >= 100 {
		return 28
	}
	return 24
}
func navigationWindow(count, selected, height int) (int, int) {
	if height <= 0 {
		return 0, 0
	}
	from := max(0, selected-height/2)
	if from+height > count {
		from = max(0, count-height)
	}
	return from, min(count, from+height)
}
func planetStyle(name string) lipgloss.Style {
	p := planets.MustGet(name)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", p.Color[0], p.Color[1], p.Color[2])))
}
func (m Model) navigationLine(n navigationItem, width int, selected bool) string {
	label := n.label
	style := lipgloss.NewStyle()
	switch n.kind {
	case navProject, navHost:
		arrow := "▾ "
		if m.collapsed[n.id] {
			arrow = "▸ "
		}
		label = arrow + label
		style = styleProject
	case navContainer:
		arrow := "▾ "
		if m.collapsed[n.id] {
			arrow = "▸ "
		}
		label = arrow + planets.MustGet(n.row.Name).Symbol + " " + label
		style = planetStyle(n.row.Name)
		if n.row.State == control.StateStopped {
			label += "  ✕"
		}
	case navSession:
		label = interactiveGlyph(n.session.State) + " " + label
		if n.tabID > 0 {
			if t, _ := m.tabByID(n.tabID); t != nil && t == m.focusedTab() {
				style = style.Bold(true)
			}
		}
	case navPane:
		label = "· " + label
		if t, _ := m.tabByID(n.tabID); t != nil {
			if t.kind == KindClaude {
				label = interactiveGlyph(m.paneSessionState(t, n.row)) + " " + n.label
				if t.reaped || t.closing || t.p != nil && !t.attachable() {
					label = "✕ " + n.label + " · detached"
				}
			}
			if t == m.focusedTab() {
				style = style.Bold(true)
			}
		}
	case navNewContainer, navNewSession:
		style = styleDim
	}
	if selected {
		style = style.Faint(false).Background(lipgloss.Color("#303544")).Bold(true)
	}
	return style.Render(fit(strings.Repeat(" ", n.depth*2)+label, width))
}
func interactiveGlyph(s control.InteractiveState) string {
	switch s.State {
	case "working":
		return "●"
	case "needs-input":
		return "▲"
	case "starting":
		return "◐"
	case "idle":
		return "○"
	case "exited":
		return "✕"
	}
	return "?"
}

func (m Model) sidebarColumn(height int) string {
	width := sidebarWidthFor(m.width) - 1
	listHeight := max(0, height-environmentHeight(height))
	items := m.navigation()
	selected := m.navigationIndex(items)
	from, to := navigationWindow(len(items), selected, listHeight)
	lines := make([]string, 0, height)
	for i := from; i < to; i++ {
		lines = append(lines, m.navigationLine(items[i], width, i == selected))
	}
	for len(lines) < listHeight {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if height > listHeight {
		lines = append(lines, m.environmentLines(width, height-listHeight)...)
	}
	return strings.Join(lines, "\n")
}
func environmentHeight(height int) int {
	if height < 6 {
		return min(1, height)
	}
	return 3
}
func (m Model) environmentLines(width, height int) []string {
	browser := "○ Browser"
	for _, r := range m.rows {
		if r.Kind == control.RowBrowser && r.Project == m.activeProject() {
			browser = stateGlyph(r, liveState{}) + " Browser"
			break
		}
	}
	daemon := "! Daemon"
	if m.daemon.Reachable {
		daemon = "✓ Daemon"
	}
	buildkit := "○ BuildKit"
	for _, r := range m.rows {
		if r.Kind == control.RowSystem && strings.Contains(strings.ToLower(r.Name), "buildkit") {
			buildkit = stateGlyph(r, liveState{}) + " BuildKit"
			break
		}
	}
	if m.snapErr != nil {
		browser, buildkit = "? Browser", "? BuildKit"
	}
	if height == 1 {
		return []string{styleDim.Render(fit("Environment  ›", width))}
	}
	return []string{styleDim.Render(strings.Repeat("─", width)), fit(browser+"  "+buildkit, width), styleDim.Render(fit(daemon+"   Details ›", width))}
}
func (m Model) activeRow() control.Row {
	if t := m.focusedTab(); t != nil {
		for _, r := range m.rows {
			if r.Kind == control.RowSandbox && r.Project == t.project && r.Name == t.sandbox {
				return r
			}
		}
		if t.kind != KindHostShell {
			return control.Row{Kind: control.RowSandbox, Project: t.project, Name: t.sandbox, State: control.StateStopped}
		}
		return control.Row{}
	}
	r := m.selectedRow()
	if r.Kind == control.RowSandbox {
		return r
	}
	return control.Row{}
}
func (m Model) activeProject() string {
	if r := m.activeRow(); r.Project != "" {
		return r.Project
	}
	if n, ok := m.selectedNavigation(); ok {
		return n.row.Project
	}
	return ""
}
