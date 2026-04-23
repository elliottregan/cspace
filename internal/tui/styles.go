// internal/tui/styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorActive  = lipgloss.Color("#3fb950")
	colorIdle    = lipgloss.Color("#e3b341")
	colorStuck   = lipgloss.Color("#f85149")
	colorExited  = lipgloss.Color("#6e7681")
	colorBlue    = lipgloss.Color("#58a6ff")
	colorDim     = lipgloss.Color("#8b949e")
	colorBorder  = lipgloss.Color("#21262d")
	colorBgLight = lipgloss.Color("#161b22")

	styleSection   = lipgloss.NewStyle().Foreground(colorDim).PaddingLeft(1)
	styleAgentName = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e6edf3"))
	styleRole      = lipgloss.NewStyle().Foreground(colorDim)

	styleDotActive = lipgloss.NewStyle().Foreground(colorActive)
	styleDotIdle   = lipgloss.NewStyle().Foreground(colorIdle)
	styleDotStuck  = lipgloss.NewStyle().Foreground(colorStuck)
	styleDotExited = lipgloss.NewStyle().Foreground(colorExited)

	styleStuckWarn = lipgloss.NewStyle().Foreground(colorStuck)
	styleContent   = lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9"))
	styleMeta      = lipgloss.NewStyle().Foreground(colorDim)
	styleBorder    = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(colorBorder)

	styleEvTime     = lipgloss.NewStyle().Foreground(colorDim).Width(8)
	styleEvInstance = lipgloss.NewStyle().Width(10)
	styleEvType     = lipgloss.NewStyle().Width(8)

	styleEvTypeTool   = styleEvType.Copy().Foreground(colorIdle)
	styleEvTypeText   = styleEvType.Copy().Foreground(colorDim)
	styleEvTypeResult = styleEvType.Copy().Foreground(colorActive)

	styleTitle = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorBlue).
			Bold(true)
	styleStatusBar = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorDim)
	styleKeyHint   = lipgloss.NewStyle().Background(colorBgLight).Foreground(lipgloss.Color("#8b949e"))
	styleFutureKey = lipgloss.NewStyle().Background(colorBgLight).Foreground(lipgloss.Color("#30363d"))
)

// instanceColor returns a stable per-instance color from a fixed palette.
func instanceColor(name string) lipgloss.Color {
	palette := []lipgloss.Color{
		"#58a6ff", "#bc8cff", "#f0883e", "#f85149", "#3fb950", "#e3b341",
	}
	h := 0
	for _, r := range name {
		h = (h*31 + int(r)) % len(palette)
	}
	return palette[h]
}
