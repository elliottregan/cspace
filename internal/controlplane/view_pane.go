package controlplane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

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
		lines := []string{
			styleDim.Render("no panes open"),
			"",
		}
		row := m.selectedRow()
		if canBoot(row) {
			lines = append(lines,
				styleDim.Render(row.Name+" is stopped. Boot it before opening a session."),
				styleDim.Render(m.keys.Boot.Help().Key+"   boot the selected container"),
				styleDim.Render("Boot replaces the stopped container; only bind-mounted files are kept."),
			)
		} else {
			lines = append(lines, styleDim.Render(m.keys.Attach.Help().Key+"   activate the selected sidebar entry"))
			if canShell(row) {
				lines = append(lines, styleDim.Render(m.keys.Shell.Help().Key+"       a shell in it"))
			}
		}
		if canSupervisor(row) {
			lines = append(lines, styleDim.Render(m.keys.Supervisor.Help().Key+"       its supervisor's events"))
		}
		lines = append(lines, styleDim.Render(lead+" "+m.keys.NewPane.Help().Key+"   the new-pane picker, host shell included"))
		return fitLines(ansi.Hardwrap(strings.Join(lines, "\n"), width, true), height)
	}
	if t.sup != nil {
		return fitLines(t.sup.view(width, height), height)
	}
	if t.p == nil {
		return fitLines(styleErr.Render("this pane has no process"), height)
	}

	// Scroll mode wins over the exited line, not the other way around. Both
	// branches call ScrollbackView, so the exited pane's history stays
	// readable either way — the choice is only which banner sits above it.
	// handlePaneKey already treats the two pane kinds identically while
	// scrolling (its m.scrolling branch runs before its own exited guard),
	// so a key press here already returns to live rather than acting on the
	// exited pane; the render used to disagree; with the exited line
	// winning, the mode was armed but invisible — no counter, no "any other
	// key returns to live" hint — so the very key that silently left scroll
	// mode looked like it had done nothing at all.
	if m.scrolling {
		return fitLines(strings.Join([]string{
			styleDim.Render(fit(fmt.Sprintf("scroll · %d lines back · %s g or any other key returns to live",
				m.scroll, lead), width)),
			t.p.ScrollbackView(m.scroll, height-1),
		}, "\n"), height)
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
