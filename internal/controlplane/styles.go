package controlplane

import "charm.land/lipgloss/v2"

// The dashboard's palette.
//
// lipgloss v2 has no global renderer and does no TTY sniffing at Render
// time: Render always emits ANSI and the program's renderer downsamples on
// write. Tests therefore compare ansi-stripped output (see plain() in
// view_sidebar_test.go), never raw bytes.
var (
	styleProject = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8888ff"))
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf"))
	stylePort    = lipgloss.NewStyle().Foreground(lipgloss.Color("#87afff"))

	// The responsive width is applied by View; the final column is the rule.
	styleSidebar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("#444444"))

	// styleMain pads the main area off the sidebar's rule.
	styleMain = lipgloss.NewStyle().Padding(0, 1)
)
