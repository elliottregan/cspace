package controlplane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderTabs draws the tabs row: one tab per open pane, titled
// "<project>/<sandbox> · <kind>".
//
// active says whether the keyboard is pointed at the main area. It is the
// third cue that typing will reach the child, beside the footer switching to
// the leader's keys and the cursor appearing in the pane — and the only one
// that is visible without reading anything.
//
// When the tabs do not fit, the row keeps the focused tab and drops from the
// LEFT, then says how many it dropped — the design's open question 1, whose
// default is exactly that. Dropping from the left rather than the right
// keeps the focused tab and its neighbours, which are the ones n/p reaches
// next; a bare truncation would hide the tab a person just opened.
func renderTabs(tabs []*tab, focused, width int, active bool) string {
	if len(tabs) == 0 {
		return ""
	}
	focusedStyle := styleTabFocused
	if active {
		focusedStyle = styleTabActive
	}
	rendered := make([]string, len(tabs))
	for i, t := range tabs {
		style := styleTabIdle
		if i == focused {
			style = focusedStyle
		}
		rendered[i] = style.Render(t.title())
	}

	from := 0
	for {
		row := strings.Join(rendered[from:], "")
		hidden := from
		suffix := ""
		if hidden > 0 {
			suffix = styleDim.Render(fmt.Sprintf(" +%d", hidden))
		}
		if ansi.StringWidth(row)+ansi.StringWidth(suffix) <= width {
			return suffix + row
		}
		if from >= focused || from == len(tabs)-1 {
			// Even the focused tab alone does not fit: truncate its text
			// rather than the rendered string, so no escape sequence is cut
			// in half and no closing reset is lost.
			//
			// The budget is the width less the two things that are added
			// around the title and are not part of it: the "+N " prefix,
			// measured rather than assumed (a two-digit count is four
			// cells, not three), and the two columns of Padding(0, 1) that
			// every tab style carries. A hard-coded 4 here is one cell too
			// many and the row comes out at width+1.
			prefix := fmt.Sprintf("+%d ", from)
			budget := width - ansi.StringWidth(prefix) - 2
			if budget < 1 {
				budget = 1
			}
			return styleDim.Render(prefix) +
				focusedStyle.Render(fit(tabs[focused].title(), budget))
		}
		from++
	}
}

// paneArea is the main area's content: the focused pane's screen, the
// supervisor view, or — with no tabs at all — the keys that open one.
//
// Render, Cursor and ScrollbackView are called from here and nowhere else,
// which is what keeps the "UI goroutine only" rule checkable: View is the
// only caller of paneArea.
func (m Model) paneArea(width, height int) string {
	t := m.focusedTab()
	if t == nil {
		return fitLines(strings.Join([]string{
			styleDim.Render("no panes open"),
			"",
			styleDim.Render("enter   a claude session in the selected sandbox"),
			styleDim.Render("s       a shell in it"),
			styleDim.Render("a       its supervisor's events"),
			styleDim.Render("⌃Space t   the new-pane picker, host shell included"),
		}, "\n"), height)
	}
	if t.sup != nil {
		return fitLines(t.sup.view(width, height), height)
	}
	if t.p == nil {
		return fitLines(styleErr.Render("this pane has no process"), height)
	}

	if code, err, exited := t.p.Exited(); exited {
		reason := fmt.Sprintf("exited with status %d", code)
		if err != nil {
			reason = "exited: " + err.Error()
		}
		// The last screen, dimmed, under the reason — a dead pane still
		// shows what it was doing when it died. By the time this renders
		// the pane has also been reaped (model.go's paneOutputMsg arm), and
		// that is fine: Pane.Close ends the child, the pty and the tmux
		// client, but the emulator keeps its screen and its scrollback —
		// only its input pipe is closed — so Render and ScrollbackView
		// still answer.
		body := styleDim.Render(fitLines(t.p.ScrollbackView(m.scroll, height-2), height-2))
		return fitLines(strings.Join([]string{
			styleErr.Render(fit(reason+" — ⌃Space x closes this tab", width)),
			"",
			body,
		}, "\n"), height)
	}

	if m.scrolling {
		return fitLines(strings.Join([]string{
			styleDim.Render(fit(fmt.Sprintf("scroll · %d lines back · ⌃Space g or any other key returns to live",
				m.scroll), width)),
			t.p.ScrollbackView(m.scroll, height-1),
		}, "\n"), height)
	}
	return fitLines(t.p.Render(), height)
}

// fitLines pads or truncates s to exactly n lines, so a block always
// occupies the height the layout gave it. Lines are not touched: their width
// is already the caller's problem, and cutting a styled line would risk
// losing its closing reset.
func fitLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// sidebarColumn is the left column: the row list, a rule, and the detail
// band for the selection.
//
// The band is 24 columns wide here, which is the design's placement once
// panes take the main area over. A URL does not fit — but the sidebar's own
// port lines carry the URL as an OSC 8 hyperlink, so the address is still
// one click away, and what the band adds at this width is the state, the
// uptime, the memory and the agent's last event.
//
// On a short window the band is what gives: below twelve rows there is not
// enough left for both, and the row list is the thing you cannot navigate
// without.
func (m Model) sidebarColumn(height int) string {
	if height <= 0 {
		return ""
	}
	band := height / 3
	switch {
	case height < 12:
		band = 0
	case band < 6:
		band = 6
	case band > 14:
		band = 14
	}
	list := height
	if band > 0 {
		list = height - band - 1 // the rule
	}
	parts := []string{renderSidebar(m.rows, m.live, m.ports, m.selected, list)}
	if band > 0 {
		row := m.selectedRow()
		k := keyOf(row)
		parts = append(parts,
			styleDim.Render(strings.Repeat("─", sidebarInner)),
			fitLines(renderDetail(row, m.live[k], m.ports[k], m.portsErr[k],
				m.events, m.eventsErr, m.memory[row.Container], sidebarInner), band))
	}
	return strings.Join(parts, "\n")
}
