package tui

import tea "github.com/charmbracelet/bubbletea"

// Actor executes the side-effecting commands the dashboard offers. It is
// consumer-defined here and implemented in internal/cli (where teardownSandbox,
// attachArgs and the control-port HTTP client live), then injected — so this
// package never imports internal/cli. Each method returns a tea.Cmd that
// eventually emits an actionResultMsg (or, for Attach, resumes the program via
// tea.ExecProcess before emitting one).
type Actor interface {
	Attach(row Row) tea.Cmd
	Down(row Row) tea.Cmd
	Send(row Row, text string) tea.Cmd
	Interrupt(row Row) tea.Cmd
	RestartBrowser(row Row) tea.Cmd
}

// actionResultMsg reports the outcome of an Actor command. label is a short
// verb ("attach", "down", "send", "interrupt", "browser restart") used in the
// footer; err is nil on success.
type actionResultMsg struct {
	label string
	err   error
}
