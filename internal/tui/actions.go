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

// Result builds the message an Actor returns to report an outcome. label is a
// short verb; err is nil on success. Exported so out-of-package Actor
// implementations (internal/cli) can construct the model's result message.
func Result(label string, err error) tea.Msg { return actionResultMsg{label: label, err: err} }

// ResultLabel/ResultErr expose an actionResultMsg for out-of-package tests.
func ResultLabel(m tea.Msg) (string, bool) {
	r, ok := m.(actionResultMsg)
	return r.label, ok
}
func ResultErr(m tea.Msg) error {
	if r, ok := m.(actionResultMsg); ok {
		return r.err
	}
	return nil
}
