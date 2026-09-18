package controlplane

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
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
		return m.startAction("attach", m.actor.Attach(row))
	case key.Matches(msg, keys.Teardown):
		m.mode = modeConfirmDown
		// The form renders into the main area, so it is built at that
		// width: mainArea's width less styleMain's one column of padding
		// on each side. huh ignores a non-positive width, which is what a
		// model that has not been sized yet would pass.
		m.confirm = newDownConfirm(row.Name, m.width-sidebarWidth-2)
		return m, m.confirm.Init()
	case key.Matches(msg, keys.Send):
		m.mode = modeInput
		m.input.SetValue("")
		return m, m.input.Focus()
	case key.Matches(msg, keys.Interrupt):
		return m.startAction("interrupt", m.actor.Interrupt(row))
	case key.Matches(msg, keys.BrowserRestart):
		return m.startAction("browser restart", m.actor.RestartBrowser(row))
	case key.Matches(msg, keys.Boot):
		return m.startAction("up", m.actor.Up(row))
	}
	return m, nil
}

// startAction marks an action in flight and starts the spinner beside it.
// The Actor's command does the work off the UI goroutine; nothing here
// blocks.
func (m Model) startAction(label string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	m.action = label
	return m, tea.Batch(cmd, m.spinner.Tick)
}

// handleInputKey drives the send box. While it is open every other binding
// is inert — the keys are text.
func (m Model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := m.input.Value()
		m.mode = modeNormal
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		return m.startAction("send", m.actor.Send(m.selectedRow(), text))
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
