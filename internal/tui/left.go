// internal/tui/left.go
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/elliottregan/cspace/internal/diagnostics"
)

func statusPriority(s diagnostics.AgentStatus) int {
	switch s {
	case diagnostics.StatusStuck:
		return 0
	case diagnostics.StatusActive:
		return 1
	case diagnostics.StatusIdle:
		return 2
	default:
		return 3
	}
}

// RenderLeft renders the left panel at the given width and height.
func RenderLeft(agents []diagnostics.AgentSnapshot, services []ServiceStatus, width, _ int) string {
	sorted := make([]diagnostics.AgentSnapshot, len(agents))
	copy(sorted, agents)
	sort.Slice(sorted, func(i, j int) bool {
		pi, pj := statusPriority(sorted[i].Status), statusPriority(sorted[j].Status)
		if pi != pj {
			return pi < pj
		}
		return sorted[i].Instance < sorted[j].Instance
	})

	var sb strings.Builder
	sb.WriteString(styleSection.Render("AGENTS") + "\n")
	for _, a := range sorted {
		sb.WriteString(renderAgentBlock(a, width))
	}

	if len(services) > 0 {
		sb.WriteString(strings.Repeat("─", width-2) + "\n")
		sb.WriteString(styleSection.Render("SHARED SERVICES") + "\n")
		for _, svc := range services {
			sb.WriteString(renderServiceRow(svc) + "\n")
		}
	}

	sb.WriteString(strings.Repeat("─", width-2) + "\n")
	sb.WriteString(styleSection.Render("DIAGNOSTICS") + "\n")
	sb.WriteString(" " + styleDotActive.Render("●") + " " + styleMeta.Render("diagnostics :8384") + "\n")
	return sb.String()
}

func renderAgentBlock(a diagnostics.AgentSnapshot, _ int) string {
	dot, borderColor := agentDotAndColor(a.Status)

	var block strings.Builder
	block.WriteString(dot + " " + styleAgentName.Render(a.Instance) +
		"  " + styleRole.Render(a.Role) + "\n")
	block.WriteString(styleMeta.Render(
		fmt.Sprintf("  %s · t:%d · $%.3f", string(a.Status), a.Turns, a.CostUsd),
	) + "\n")

	if a.Status == diagnostics.StatusStuck && a.PendingTool != nil {
		elapsed := time.Since(a.PendingTool.StartedAt).Round(time.Second)
		block.WriteString(styleStuckWarn.Render(
			fmt.Sprintf("  %s · %s", a.PendingTool.Tool, elapsed),
		) + "\n")
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(borderColor).
		PaddingLeft(1)
	return border.Render(block.String()) + "\n"
}

func agentDotAndColor(status diagnostics.AgentStatus) (string, lipgloss.Color) {
	switch status {
	case diagnostics.StatusActive:
		return styleDotActive.Render("●"), colorActive
	case diagnostics.StatusIdle:
		return styleDotIdle.Render("●"), colorIdle
	case diagnostics.StatusStuck:
		return styleDotStuck.Render("●"), colorStuck
	default:
		return styleDotExited.Render("●"), colorExited
	}
}

func renderServiceRow(svc ServiceStatus) string {
	dot := styleDotStuck.Render("●")
	if svc.Running {
		dot = styleDotActive.Render("●")
	}
	port := ""
	if svc.Port != "" {
		port = " " + lipgloss.NewStyle().Foreground(colorBlue).Render(svc.Port)
	}
	return " " + dot + " " + styleContent.Render(svc.Name) + port + " " + styleMeta.Render(svc.Label)
}
