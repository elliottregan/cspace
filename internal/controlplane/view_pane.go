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
// When the tabs do not fit, the row keeps the focused tab and shrinks a
// window around it until the rest fits, counting what it dropped on each
// side: "+L" before the row, "+R" after it. That is the design's open
// question 1, whose default is to drop from the left and say how many — the
// right-hand count is what makes the answer honest when the focused tab is
// near the start, which is also the case where dropping from the left alone
// can drop nothing at all.
//
// Which side gives way is decided by distance from the focused tab, so its
// neighbours — the ones n/p reaches next — are the last to go, and a tie
// goes to the left, which is the documented default.
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

	from, to := 0, len(tabs)
	for {
		left, right := tabsElided(from, true), tabsElided(len(tabs)-to, false)
		row := strings.Join(rendered[from:to], "")
		if ansi.StringWidth(left)+ansi.StringWidth(row)+ansi.StringWidth(right) <= width {
			return left + row + right
		}
		if to-from <= 1 {
			break
		}
		// Whichever side is farther from the focused tab gives way; a tie
		// drops from the left.
		if focused-from >= to-1-focused {
			from++
		} else {
			to--
		}
	}

	// Even the focused tab alone does not fit: truncate its text rather
	// than the rendered string, so no escape sequence is cut in half and no
	// closing reset is lost.
	//
	// The budget is the width less the three things that are added around
	// the title and are not part of it: the two elision counts, measured
	// rather than assumed (a two-digit count is four cells, not three), and
	// the two columns of Padding(0, 1) that every tab style carries. A
	// hard-coded 4 here is one cell too many and the row comes out at
	// width+1.
	left, right := tabsElided(focused, true), tabsElided(len(tabs)-focused-1, false)
	budget := width - ansi.StringWidth(left) - ansi.StringWidth(right) - 2
	if budget < 1 {
		budget = 1
	}
	return left + focusedStyle.Render(fit(tabs[focused].title(), budget)) + right
}

// tabsElided is one side's "+N" marker, and nothing at all when that side
// dropped nothing. Emitting it unconditionally is what used to print a
// literal "+0 " in front of a row with every tab to the right of the
// focused one hidden and none to its left.
func tabsElided(n int, left bool) string {
	if n <= 0 {
		return ""
	}
	if left {
		return styleDim.Render(fmt.Sprintf("+%d ", n))
	}
	return styleDim.Render(fmt.Sprintf(" +%d", n))
}

// paneArea is the main area's content: the focused pane's screen, the
// supervisor view, or — with no tabs at all — the keys that open one.
//
// Render, Cursor and ScrollbackView are called from here and nowhere else,
// which is what keeps the "UI goroutine only" rule checkable: View is the
// only caller of paneArea.
func (m Model) paneArea(width, height int) string {
	// Every key this area names is a leader chord, and the leader is
	// configurable — so the prefix is read from the binding, exactly as the
	// footer reads it.
	lead := m.leaderLabel()
	t := m.focusedTab()
	if t == nil {
		return fitLines(strings.Join([]string{
			styleDim.Render("no panes open"),
			"",
			styleDim.Render("enter   a claude session in the selected sandbox"),
			styleDim.Render("s       a shell in it"),
			styleDim.Render("a       its supervisor's events"),
			styleDim.Render(lead + " t   the new-pane picker, host shell included"),
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
			styleErr.Render(fit(reason+" — "+lead+" x closes this tab", width)),
			"",
			body,
		}, "\n"), height)
	}

	if m.scrolling {
		return fitLines(strings.Join([]string{
			styleDim.Render(fit(fmt.Sprintf("scroll · %d lines back · %s g or any other key returns to live",
				m.scroll, lead), width)),
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
