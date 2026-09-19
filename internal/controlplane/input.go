package controlplane

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// handleKey routes a keypress to whatever owns the keyboard. Ctrl+C never
// arrives here — Update takes it first, so no mode can swallow the way out.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// An error notice stays until the next keypress; any key dismisses it.
	// Success notices fade on their own timer, so leave those alone.
	if m.notice.isErr {
		m.notice = notice{}
	}
	switch m.mode {
	case modeConfirmDown:
		return m.updateConfirm(msg)
	case modeInput:
		return m.handleInputKey(msg)
	}
	return m.handleNormalKey(msg)
}

// handleNormalKey is the sidebar's own keyboard. The keys that never depend
// on the selection come first; the rest go through forRow, whose disabled
// bindings key.Matches skips — so a key the selection cannot use does
// nothing rather than failing on press, and the footer has already stopped
// offering it.
func (m Model) handleNormalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The help overlay swallows the very next key regardless of what it is:
	// it closes on anything, not just `?`. Without this, a key that also
	// dispatches an action (d, for instance) would both close help and fire
	// that action against a main area the overlay had been covering.
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit
	case key.Matches(msg, m.keys.MoveUp):
		m.moveSelection(-1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.MoveDown):
		m.moveSelection(1)
		return m, m.eventsCmd()
	case key.Matches(msg, m.keys.Refresh):
		if m.pollingMedium {
			return m, nil
		}
		m.pollingMedium = true
		return m, m.snapshotCmd()
	case key.Matches(msg, m.keys.FocusMain):
		if len(m.tabs) == 0 {
			return m, nil
		}
		m.focus = focusMain
		return m, nil
	}

	// One action at a time: the footer reports one outcome, and attach hands
	// the terminal away entirely while it runs.
	if m.action != "" {
		return m, nil
	}

	row := m.selectedRow()
	keys := m.keys.forRow(row, m.live[keyOf(row)])
	switch {
	case key.Matches(msg, keys.Attach):
		return m.openOrFocus(KindClaude, row)
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
		m.confirm = newDownConfirm(row.Name, mainWidthFor(m.width)-2)
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
