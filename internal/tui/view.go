package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleHeader    = lipgloss.NewStyle().Bold(true)
	styleProject   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8888ff"))
	styleDim       = lipgloss.NewStyle().Faint(true)
	styleSelected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5fffaf"))
	styleErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleOK        = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf"))
	styleFooterKey = lipgloss.NewStyle() // enabled key: normal weight, reads brighter than the dimmed/disabled ones
)

// formatMemory renders bytes as a compact G/M string; 0 -> "-".
func formatMemory(b int64) string {
	switch {
	case b <= 0:
		return "-"
	case b >= 1<<30:
		return fmt.Sprintf("%dG", b/(1<<30))
	default:
		return fmt.Sprintf("%dM", b/(1<<20))
	}
}

// formatUptime renders a duration as ↑<h>h<m>m / ↑<m>m / ↑<s>s.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("↑%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("↑%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("↑%ds", int(d.Seconds()))
	}
}

func stateGlyph(r Row) string {
	switch r.State {
	case StateRunning:
		return "●"
	case StateDegraded:
		return "◐"
	case StateBooting:
		return "◍"
	default:
		return "○"
	}
}

// stateLabel is the short status word a row prints, derived from its State so a
// stopped/degraded sidecar, browser, or system container is not mislabeled.
func stateLabel(r Row) string {
	switch r.State {
	case StateRunning:
		return "running"
	case StateDegraded:
		return "degraded"
	case StateBooting:
		return "booting"
	default:
		return "stopped"
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	var b strings.Builder

	// header
	daemon := "unreachable"
	dStyle := styleErr
	if m.daemon.Reachable {
		daemon = "ok " + m.daemon.Version
		dStyle = styleOK
	}
	b.WriteString(styleHeader.Render("cspace tui"))
	b.WriteString("   ")
	b.WriteString(dStyle.Render("daemon: " + daemon))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", min(m.width, 78)))
	b.WriteString("\n")

	// list-failure banner, with an "as of" marker over the kept last-known rows
	if m.snapErr != nil {
		banner := "container ls failed: " + m.snapErr.Error() + " — run cspace doctor"
		if !m.lastPoll.IsZero() {
			banner += "  (as of " + m.lastPoll.Format("15:04:05") + ")"
		}
		b.WriteString(styleErr.Render(banner) + "\n")
	}

	// rows (with the "— system —" divider and stale-dimming handled inside)
	b.WriteString(m.renderRows())

	b.WriteString(strings.Repeat("─", min(m.width, 78)))
	b.WriteString("\n")

	// detail pane for the selected sandbox
	b.WriteString(m.renderDetail())

	// footer
	b.WriteString(m.renderFooter())
	return b.String()
}

// systemDividerRendered tracks whether renderRows already emitted the
// "— system —" divider; the divider prints once, before the first RowSystem.
func (m Model) renderRows() string {
	var b strings.Builder
	dividerDone := false
	for i, r := range m.rows {
		if r.Kind == RowSystem && !dividerDone {
			b.WriteString(styleDim.Render("— system —") + "\n")
			dividerDone = true
		}
		line := renderRow(r)
		if i == m.selected && r.Selectable {
			line = styleSelected.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		if m.snapErr != nil {
			line = styleDim.Render(line) // stale snapshot: dim last-known rows
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func renderRow(r Row) string {
	switch r.Kind {
	case RowProject:
		return styleProject.Render(r.Name)
	case RowSandbox:
		agent := "agent: ?"
		if r.Agent.Reachable {
			agent = "agent: " + r.Agent.State + "  q:" + fmt.Sprintf("%d", r.Agent.QueueDepth)
		}
		return fmt.Sprintf("%s %-14s %-18s %-15s %-4s %s",
			stateGlyph(r), r.Name, agent, r.IP, formatMemory(r.MemoryB), formatUptime(r.Uptime))
	case RowSidecar:
		return styleDim.Render(fmt.Sprintf("  ├ %-16s %-9s %-15s %s",
			r.Name, stateLabel(r), r.IP, formatMemory(r.MemoryB)))
	case RowBrowser:
		return fmt.Sprintf("%s %-16s %-9s %-15s %s",
			stateGlyph(r), r.Name, stateLabel(r), r.IP, formatMemory(r.MemoryB))
	case RowSystem:
		return styleDim.Render(fmt.Sprintf("  %-18s %-9s %-15s %s",
			r.Name, stateLabel(r), r.IP, formatMemory(r.MemoryB)))
	}
	return r.Name
}

func (m Model) renderDetail() string {
	row := m.selectedRow()
	switch row.Kind {
	case RowBrowser:
		if row.Browser.Reachable {
			v := row.Browser.Version
			if v == "" {
				v = "reachable"
			}
			return fmt.Sprintf("%s · CDP :9222 · %s\n", row.Name, v)
		}
		return styleErr.Render(row.Name+" · CDP unreachable — press b to restart") + "\n"
	case RowSandbox:
		// falls through to the sandbox detail below
	default:
		return styleDim.Render("select a sandbox for details") + "\n"
	}
	var b strings.Builder
	if row.Agent.Reachable {
		fmt.Fprintf(&b, "%s · session %s · %s · lastEvent %s\n",
			row.Name, row.Agent.Session, row.Agent.State, lastEventLabel(row.Agent))
	} else {
		b.WriteString(styleDim.Render(row.Name+" · no running agent") + "\n")
	}
	if len(m.events) == 0 {
		b.WriteString(styleDim.Render("no events yet") + "\n")
	} else {
		for _, e := range m.events {
			b.WriteString(styleDim.Render(fmt.Sprintf("  %s %-10s %s", shortTs(e.Ts), e.Type, e.Subtype)) + "\n")
		}
	}
	return b.String()
}

func lastEventLabel(a AgentStatus) string {
	if a.LastEventType == "" {
		return "-"
	}
	if a.LastEventSubtype != "" {
		return a.LastEventType + "/" + a.LastEventSubtype
	}
	return a.LastEventType
}

func shortTs(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19] // HH:MM:SS from an ISO8601 string
	}
	return ts
}

func (m Model) renderFooter() string {
	switch m.mode {
	case modeConfirmDown:
		return styleErr.Render(fmt.Sprintf("down %s? [y/N]", m.selectedRow().Name))
	case modeInput:
		return "send to " + m.selectedRow().Name + "› " + m.input.View()
	}
	if m.action != "" {
		return m.spinner.View() + " " + m.action + "…"
	}
	if m.notice.text != "" {
		if m.notice.isErr {
			return styleErr.Render(m.notice.text)
		}
		return styleOK.Render(m.notice.text)
	}
	if m.help {
		// Multi-line help overlay listing every key.
		lines := []string{
			"keys:",
			"  ↑/k, ↓/j   move selection",
			"  enter      attach to the selected sandbox",
			"  s          send a message to the agent",
			"  i          interrupt the agent's current task",
			"  d          tear down the sandbox (confirms)",
			"  b          restart the project's browser sidecar",
			"  r          refresh now",
			"  ?          toggle this help",
			"  q, ctrl+c  quit",
		}
		return styleDim.Render(strings.Join(lines, "\n"))
	}
	// Contextual hints: enabled keys for the selection read bright, invalid ones dim.
	parts := make([]string, 0, 7)
	for _, h := range footerHints(m.selectedRow()) {
		if h.on {
			parts = append(parts, styleFooterKey.Render(h.label))
		} else {
			parts = append(parts, styleDim.Render(h.label))
		}
	}
	parts = append(parts, styleDim.Render("[?] help"), styleDim.Render("[q]uit"))
	return strings.Join(parts, "  ")
}

type footerHint struct {
	label string
	on    bool
}

// footerHints is the pure per-selection key/enabled decision the footer renders.
// Extracted so the contextual on/off logic is testable without ANSI styling.
func footerHints(row Row) []footerHint {
	return []footerHint{
		{"[enter] attach", canAttach(row)},
		{"[s]end", canSend(row)},
		{"[i]nterrupt", canInterrupt(row)},
		{"[d]own", canDown(row)},
		{"[b]rowser restart", canBrowser(row)},
	}
}
