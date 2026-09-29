package controlplane

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

// Compact status indicators used by navigation and environment summaries.
const (
	glyphStopped    = "✕"
	glyphBooting    = "◐"
	glyphDegraded   = "!"
	glyphWorking    = "●"
	glyphIdle       = "○"
	glyphNeedsInput = "▲"
	glyphHealthy    = "✓"
)

// stateGlyph is a row's one-character state, in the design's precedence
// order: lifecycle first — stopped, booting, degraded — then, for a running
// sandbox, the interactive session's state when a hook has ever written one,
// else the supervisor's, else idle.
//
// An interactive "exited" (or any state this does not know) falls through to
// the supervisor: the person's session being over says nothing about whether
// the headless agent is still working.
func stateGlyph(row control.Row, live liveState) string {
	switch row.State {
	case control.StateStopped:
		return glyphStopped
	case control.StateBooting:
		return glyphBooting
	case control.StateDegraded:
		return glyphDegraded
	}
	if row.Kind == control.RowBrowser {
		if row.Browser.Reachable {
			return glyphHealthy
		}
		return glyphDegraded
	}
	if row.Kind != control.RowSandbox {
		return glyphHealthy
	}
	if live.Interactive.Known() {
		switch live.Interactive.State {
		case "working":
			return glyphWorking
		case "needs-input":
			return glyphNeedsInput
		case "idle":
			return glyphIdle
		case "starting":
			return glyphBooting
		}
	}
	if a := agentOf(row, live); a.Reachable && a.State == "working" {
		return glyphWorking
	}
	return glyphIdle
}

// fit pads s with spaces, or truncates it with an ellipsis, so it occupies
// exactly w cells. Width is measured in display cells (ansi.StringWidth), so
// a glyph like ● or a multi-byte name is never cut mid-character.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width := ansi.StringWidth(s); width <= w {
		return s + strings.Repeat(" ", w-width)
	}
	runes := []rune(s)
	for len(runes) > 0 && ansi.StringWidth(string(runes))+1 > w {
		runes = runes[:len(runes)-1]
	}
	out := string(runes) + "…"
	if pad := w - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}
