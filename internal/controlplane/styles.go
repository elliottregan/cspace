package controlplane

import "charm.land/lipgloss/v2"

// The dashboard's palette.
//
// lipgloss v2 has no global renderer and does no TTY sniffing at Render
// time: Render always emits ANSI and the program's renderer downsamples on
// write. Tests therefore compare ansi-stripped output (see plain() in
// view_sidebar_test.go), never raw bytes.
var (
	styleProject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8888ff"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5fffaf"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf"))
	stylePort     = lipgloss.NewStyle().Foreground(lipgloss.Color("#87afff"))

	// styleSidebar draws the vertical rule that separates the list from the
	// main area. The rule is the sidebar's 24th column, and lipgloss v2's
	// Width is the *whole block's* width — Render subtracts the border size
	// before wrapping — so this is sidebarWidth, not sidebarInner. Giving it
	// sidebarInner would leave sidebarContent columns for a line that is
	// sidebarInner cells wide and wrap every single row onto two lines.
	// MaxWidth is the same number for the same reason: it truncates after
	// the border is applied, so anything smaller would cut the rule off.
	styleSidebar = lipgloss.NewStyle().
			Width(sidebarWidth).MaxWidth(sidebarWidth).
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("#444444"))

	// styleTabs titles the reserved tabs line and the help overlay. The
	// one-column padding on each side is what tabsLine's arithmetic
	// accounts for.
	styleTabs = lipgloss.NewStyle().Bold(true).Padding(0, 1)

	// styleMain pads the main area off the sidebar's rule.
	styleMain = lipgloss.NewStyle().Padding(0, 1)
)

// lipglossStyle is lipgloss.Style under a local name, so view code can hand
// styles around without every file importing lipgloss. The zero value
// renders text unchanged, which is what an unstyled row wants.
type lipglossStyle = lipgloss.Style
