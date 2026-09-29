package controlplane

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/planets"
)

type headerLink struct {
	x, y, width int
	url         string
	details     bool
}
type headerPlan struct {
	text  string
	links []headerLink
}

func (m Model) planHeader(width int) headerPlan {
	if width < 1 {
		return headerPlan{}
	}
	row := m.activeRow()
	title := "cspace"
	titleStyle := lipgloss.NewStyle().Bold(true)
	if row.Name != "" {
		title = planets.MustGet(row.Name).Symbol + " " + row.Name
		titleStyle = planetStyle(row.Name).Bold(true)
	}
	if t := m.focusedTab(); t != nil {
		label := t.kind.String()
		if t.kind == KindClaude {
			label = "Claude"
			if t.session.Name != "" {
				label = t.session.Label()
			}
		}
		title += " · " + label
		if t.kind == KindClaude {
			state := m.paneSessionState(t, row)
			if state.Known() {
				title += " · " + state.State
			}
		}
	}
	details := "[Details]"
	dw := ansi.StringWidth(details)
	titleWidth := max(0, width-dw-1)
	line1 := titleStyle.Render(fit(title, titleWidth)) + " " + styleDim.Render(details)
	if width < dw+2 {
		line1 = titleStyle.Render(fit(title, width))
	}
	plan := headerPlan{}
	if row.Name != "" && width >= dw+2 {
		plan.links = append(plan.links, headerLink{x: titleWidth + 1, y: 0, width: dw, details: true})
	}
	sample := m.headers[keyOf(row)]
	type part struct {
		text, url string
		style     lipgloss.Style
	}
	var parts []part
	if pr := sample.status.PR; pr != nil {
		label := fmt.Sprintf("PR #%d", pr.Number)
		if sample.status.PRStale {
			label += " ~"
		}
		parts = append(parts, part{text: label, url: pr.URL, style: prStyle(pr.CheckStatus)})
	} else if sample.err != nil {
		parts = append(parts, part{text: "PR unavailable", style: styleDim})
	} else if sample.status.Branch != "" && !sample.status.Detached {
		parts = append(parts, part{text: "No PR", style: styleDim})
	}
	services := m.services[keyOf(row)]
	if services == nil {
		for _, p := range m.ports[keyOf(row)] {
			services = append(services, control.ServiceStatus{Port: p.Port, Label: p.Label, URL: p.URL, State: control.ServiceRunning})
		}
	}
	for _, name := range []string{"dev", "preview"} {
		for _, s := range services {
			if strings.ToLower(strings.TrimSpace(s.Label)) != name {
				continue
			}
			label, url, style := "○ "+name, "", styleDim
			switch s.State {
			case control.ServiceRunning:
				label, url, style = "● "+name, s.URL, styleOK
				if name == "preview" {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color("#e5c07b"))
				}
			case control.ServiceUnknown:
				label = "? " + name
			}
			parts = append(parts, part{text: label, url: url, style: style})
		}
	}
	occupied := 0
	for _, p := range parts {
		occupied += ansi.StringWidth(p.text) + 2
	}
	branch := sample.status.Branch
	if branch == "" {
		if sample.err != nil {
			branch = "git unavailable"
		} else if sample.status.Detached {
			branch = "detached HEAD"
		}
	}
	if sample.status.Dirty {
		branch += " *"
	}
	budget := max(0, width-occupied)
	line2 := styleDim.Render(fit(branch, budget))
	x := budget
	for _, p := range parts {
		pw := ansi.StringWidth(p.text)
		if x+2+pw > width {
			break
		}
		line2 += "  " + p.style.Hyperlink(p.url).Render(p.text)
		if p.url != "" {
			plan.links = append(plan.links, headerLink{x: x + 2, y: 1, width: pw, url: p.url})
		}
		x += 2 + pw
	}
	plan.text = ansi.Truncate(line1, width, "") + "\n" + ansi.Truncate(line2, width, "")
	return plan
}

func (m Model) paneSessionState(t *tab, row control.Row) control.InteractiveState {
	for _, s := range m.sessions[keyOf(row)] {
		if sessionMatches(t.session, s) {
			return s.State
		}
	}
	if t.session.ID == "" && (t.session.Name == "" || t.session.Name == control.SessionClaude) {
		return m.live[keyOf(row)].Interactive
	}
	return control.InteractiveState{}
}
func prStyle(state string) lipgloss.Style {
	color := "#9096a4"
	switch state {
	case "success":
		color = "#5fffaf"
	case "failure":
		color = "#ff5555"
	case "running":
		color = "#da7756"
	case "blocked":
		color = "#e5c07b"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
