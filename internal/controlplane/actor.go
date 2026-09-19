package controlplane

import (
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// Actor runs the dashboard's side effects. It is declared here — by the
// consumer — and implemented in internal/cli, whose actor delegates to
// internal/control for all of them. Injecting it is what keeps this package
// from importing internal/cli. Panes are a separate seam (PaneHost), because
// opening one produces a live process this package then owns.
//
// Every method returns a tea.Cmd that eventually emits the message Result
// builds. None of them may do I/O before the returned command runs: Update
// calls these on the UI goroutine, and a probe or a lock taken there freezes
// the whole dashboard. Every returned Cmd must eventually yield a message
// built by Result or ResultWarn for the same label — the Label* constants
// below, one per method — because the dashboard blocks further actions until
// it arrives; a nil Cmd means "nothing to do" and must not be returned for an
// action the caller marked in flight.
type Actor interface {
	Down(row control.Row) tea.Cmd
	Send(row control.Row, text string) tea.Cmd
	Interrupt(row control.Row) tea.Cmd
	RestartBrowser(row control.Row) tea.Cmd
	Up(row control.Row) tea.Cmd
}

// The label each action wears: the verb the footer shows while it runs and
// the one every Result carries back. Exported because the Actor is
// implemented in another package and both sides have to agree on the string
// — a mismatch degrades quietly, since actionResultMsg clears the in-flight
// action whatever its label says, leaving only a footer reading the wrong
// verb.
const (
	LabelDown           = "down"
	LabelSend           = "send"
	LabelInterrupt      = "interrupt"
	LabelBrowserRestart = "browser restart"
	LabelUp             = "up"
)

// actionResultMsg reports an Actor command's outcome. label is the short
// verb the footer shows (one of the Label* constants above); err is nil on
// success; warn carries a notice from an action that succeeded but has
// something the person must read.
type actionResultMsg struct {
	label string
	warn  string
	err   error
}

// Result builds the message an Actor returns to report an outcome.
// Exported because the implementation lives in another package.
func Result(label string, err error) tea.Msg { return actionResultMsg{label: label, err: err} }

// ResultWarn reports an action that worked but degraded — the spec's no-tmux
// attach fallback is the one this exists for: the attach succeeds, and the
// person has to be told their session will not outlive the window. It is not
// an error (nothing failed) and it must not fade unread, so the footer keeps
// it in the alert style until the next keypress, exactly like an error.
func ResultWarn(label, warn string) tea.Msg {
	return actionResultMsg{label: label, warn: warn}
}

// ResultLabel, ResultErr and ResultWarnText read a Result back, for
// out-of-package tests.
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

func ResultWarnText(m tea.Msg) string {
	if r, ok := m.(actionResultMsg); ok {
		return r.warn
	}
	return ""
}
