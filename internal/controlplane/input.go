package controlplane

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// handleKey routes a keypress to whatever owns the keyboard: a modal, the
// leader, the focused pane, or the sidebar.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// An error notice stays until the next keypress; any key dismisses it.
	// Success notices fade on their own timer, so leave those alone.
	if m.notice.isErr {
		m.notice = notice{}
	}
	if m.mode == modeDetails || m.showHelp {
		return m.handleDetailsKey(msg)
	}
	if m.hasModal() && (msg.String() == "pgup" || msg.String() == "pgdown") {
		_, _, height := m.modalFrame()
		if msg.String() == "pgup" {
			height = -height
		}
		m.scrollModal(height)
		return m, nil
	}
	switch m.mode {
	case modeConfirmDown:
		return m.updateConfirm(msg)
	case modeInput:
		return m.handleInputKey(msg)
	case modePicker:
		return m.updatePicker(msg)
	case modeCreate:
		return m.updateCreate(msg)
	}
	if m.leaderArmed {
		m.leaderArmed = false
		return m.handleLeaderKey(msg)
	}
	if key.Matches(msg, m.keys.Leader) {
		m.leaderArmed = true
		return m, nil
	}
	if m.focus == focusMain && m.focusedTab() != nil {
		// Every key but the leader belongs to the child. That is the point
		// of a pane, and it is why the leader must not be ctrl+b.
		return m.handlePaneKey(msg)
	}
	return m.handleNormalKey(msg)
}

// handleNormalKey is the sidebar's own keyboard. The keys that never depend
// on the selection come first; the rest go through forRow, whose disabled
// bindings key.Matches skips — so a key the selection cannot use does
// nothing rather than failing on press, and the footer has already stopped
// offering it.
func (m Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Details):
		row := m.selectedRow()
		return m.openDetails(row, row.Kind != control.RowSandbox)
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, m.quitCmd()
	case key.Matches(msg, m.keys.MoveUp):
		m.moveNavigation(-1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.MoveDown):
		m.moveNavigation(1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.Refresh):
		var cmds []tea.Cmd
		if !m.pollingMedium {
			m.pollingMedium = true
			cmds = append(cmds, m.snapshotCmd())
		}
		if m.readingHeader {
			m.refreshHeader = true
		} else {
			if hc := m.headerCmd(true); hc != nil {
				m.readingHeader = true
				cmds = append(cmds, hc)
			}
		}
		if !m.pollingSlow {
			m.pollingSlow = true
			cmds = append(cmds, m.slowCmd())
		}
		return m, tea.Batch(cmds...)
	case key.Matches(msg, m.keys.FocusMain):
		if len(m.tabs) == 0 {
			return m, nil
		}
		m.focus = focusMain
		return m, nil
	}

	// One action at a time: the footer reports one outcome, so a second
	// action started under the first would have the pair of them racing
	// for that line and the first result to land clearing the gate for
	// both.
	if m.action != "" {
		return m, nil
	}

	if key.Matches(msg, m.keys.Attach) {
		if n, ok := m.selectedNavigation(); ok {
			// Enter on a container preserves the original default-attach
			// shortcut. Left/right and a heading click expand its children.
			if n.kind == navContainer {
				if canAttach(n.row) {
					return m.openOrFocus(KindClaude, n.row)
				}
				m.notice = notice{
					text:  fmt.Sprintf("%s is stopped; press %s to boot before opening a session", n.row.Name, m.keys.Boot.Help().Key),
					isErr: true,
				}
				return m, nil
			}
			return m.activateNavigation(n)
		}
	}
	if msg.String() == "left" || msg.String() == "right" {
		if n, ok := m.selectedNavigation(); ok && (n.kind == navContainer || n.kind == navProject || n.kind == navHost) {
			want := msg.String() == "left"
			if m.collapsed[n.id] != want {
				m.toggleNavigation(n.id)
			}
			return m, nil
		}
	}
	row := m.selectedRow()
	keys := m.keys.forRow(row, m.live[keyOf(row)])
	switch {
	case key.Matches(msg, keys.Shell):
		return m.openOrFocus(KindShell, row)
	case key.Matches(msg, keys.Supervisor):
		return m.openOrFocus(KindSupervisor, row)
	case key.Matches(msg, keys.Teardown):
		m.mode = modeConfirmDown
		// pending pins the row the prompt was opened against: a poll can
		// land and move the selection while the confirmation is still on
		// screen, and the teardown has to act on what it named, not on
		// whatever ends up selected by the time it is answered.
		m.pending = row
		// The form renders into the main area, so it is built at that
		// width: mainWidthFor's floor, less styleMain's one column of
		// padding on each side (the same width View() itself uses). huh
		// ignores a non-positive width, which is what a model that has not
		// been sized yet would pass.
		_, width, _ := m.modalFrame()
		m.confirm = newDownConfirm(row.Name, width)
		return m, m.confirm.Init()
	case key.Matches(msg, keys.Send):
		m.mode = modeInput
		m.pending = row // see the Teardown case above
		m.input.SetValue("")
		m.input.SetWidth(sendInputWidth(row.Name, m.width))
		return m, m.input.Focus()
	case key.Matches(msg, keys.Interrupt):
		return m.startAction(LabelInterrupt, m.actor.Interrupt(row))
	case key.Matches(msg, keys.BrowserRestart):
		return m.startAction(LabelBrowserRestart, m.actor.RestartBrowser(row))
	case key.Matches(msg, keys.Boot):
		m.actionTarget = row
		return m.startAction(LabelUp, m.actor.Up(row))
	}
	return m, nil
}

// startAction marks an action in flight and starts the spinner beside it.
// The Actor's command does the work off the UI goroutine; nothing here
// blocks.
//
// A nil cmd means the caller has nothing to do — see the Actor contract in
// actor.go — so it is left untouched rather than marked in flight: an action
// with nothing running behind it would never get an actionResultMsg to clear
// it, and the spinner would spin forever.
func (m Model) startAction(label string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if cmd == nil {
		return m, nil
	}
	m.action = label
	// A new action gets a clean line: whatever the last one was asked
	// while it was busy has nothing to do with this one.
	m.actionNote = ""
	return m, tea.Batch(cmd, m.spinner.Tick)
}

// handleInputKey drives the send box. While it is open every other binding
// is inert — the keys are text.
func (m Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		target := m.pending
		m.mode = modeNormal
		m.pending = control.Row{}
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		return m.startAction(LabelSend, m.actor.Send(target, text))
	case "esc":
		m.mode = modeNormal
		m.pending = control.Row{}
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
