package controlplane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

const (
	// sidebarWidth is the sidebar's total width, fixed by the design at 24
	// columns. The last of them is the vertical rule separating it from the
	// main area, and the first of what remains is the selection marker, so
	// a row's text gets sidebarContent.
	sidebarWidth   = 24
	sidebarInner   = sidebarWidth - 1
	sidebarContent = sidebarInner - 1
)

// The sidebar's state glyphs. One character each: at 24 columns there is no
// room for a word, and the detail band spells the state out anyway.
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

// sidebarLine is one rendered line and the row it belongs to. Rows expand to
// more than one line (a sandbox owns a line per labeled port), so the window
// arithmetic works in lines while the model keeps thinking in rows. row is
// -1 for a line that belongs to no row, like the system divider.
type sidebarLine struct {
	text string
	row  int
}

// sidebarLines renders every row, and each sandbox's known ports beneath it,
// into flat lines of at most sidebarInner cells.
func sidebarLines(rows []control.Row, live map[sandboxKey]liveState, ports map[sandboxKey][]control.Port, selected int) []sidebarLine {
	out := make([]sidebarLine, 0, len(rows)+4)
	dividerDone := false
	// parent is the sandbox the following sidecar rows belong to: Correlate
	// names a sidecar "<sandbox>-<service>", and only the row order says
	// where that prefix ends.
	parent := ""
	for i, row := range rows {
		if row.Kind == control.RowSystem && !dividerDone {
			out = append(out, sidebarLine{text: styleDim.Render(fit("— system —", sidebarInner)), row: -1})
			dividerDone = true
		}
		if row.Kind == control.RowSandbox {
			parent = row.Name
		}

		marker, isSelected := " ", i == selected && row.Selectable
		text, style := sidebarRow(row, live[keyOf(row)], parent)
		if isSelected {
			// The marker alone carries the selection at 24 columns on a
			// terminal with no colour; styleSelected carries it everywhere
			// else. Only selectable rows can be selected, and their rows
			// come back unstyled, so nothing is being overridden here.
			marker, style = "▸", styleSelected
		}
		out = append(out, sidebarLine{text: marker + style.Render(fit(text, sidebarContent)), row: i})

		if row.Kind != control.RowSandbox {
			continue
		}
		for _, p := range ports[keyOf(row)] {
			label := strings.TrimSpace(fmt.Sprintf("%d %s", p.Port, p.Label))
			// The URL rides as an OSC 8 hyperlink on the visible label, so
			// the row stays inside 24 columns and the terminal still opens
			// the real address.
			out = append(out, sidebarLine{
				text: " " + stylePort.Hyperlink(p.URL).Render(fit("   "+label, sidebarContent)),
				row:  i,
			})
		}
	}
	return out
}

// sidebarRow is one row's text and style, without the selection marker.
// parent is the sandbox a sidecar hangs under, whose name prefixes it.
func sidebarRow(row control.Row, live liveState, parent string) (string, lipglossStyle) {
	switch row.Kind {
	case control.RowProject:
		return "▾ " + row.Name, styleProject
	case control.RowSidecar:
		// Sidecars nest under their sandbox; Correlate left that sandbox's
		// name on the front of theirs, which is redundant here. Trimming by
		// the parent rather than at the first "-" is what keeps a
		// hyphenated sandbox name (issue-42) from eating its service name.
		name := strings.TrimPrefix(row.Name, parent+"-")
		return "   ├ " + name, styleDim
	case control.RowSystem:
		return "  " + row.Name, styleDim
	}
	return stateGlyph(row, live) + " " + row.Name, lipglossStyle{}
}

// sidebarWindow is the half-open range of lines to render so the selected
// row stays on screen in a region of `height` lines. It is a pure function
// of the selection rather than a remembered scroll offset: rows are rebuilt
// from scratch every poll, and an offset carried across that would drift
// against a list that grew or shrank underneath it.
func sidebarWindow(lines []sidebarLine, selected, height int) (from, to int) {
	if height <= 0 || len(lines) == 0 {
		return 0, 0
	}
	if len(lines) <= height {
		return 0, len(lines)
	}
	anchor := 0
	for i, l := range lines {
		if l.row == selected {
			anchor = i
			break
		}
	}
	from = anchor - height/2
	if from < 0 {
		from = 0
	}
	if from+height > len(lines) {
		from = len(lines) - height
	}
	return from, from + height
}

// renderSidebar renders exactly height lines: the window above, padded out
// so the vertical rule runs the full height even when there is little to
// show. Rendering a fixed number of lines is what stops a busy host from
// pushing the detail band and the footer off a short terminal.
func renderSidebar(rows []control.Row, live map[sandboxKey]liveState, ports map[sandboxKey][]control.Port, selected, height int) string {
	// A short terminal can hand the layout a negative row budget; render
	// nothing rather than pass a negative capacity to make() and panic.
	if height < 0 {
		height = 0
	}
	lines := sidebarLines(rows, live, ports, selected)
	from, to := sidebarWindow(lines, selected, height)
	out := make([]string, 0, height)
	for _, l := range lines[from:to] {
		out = append(out, l.text)
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
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
