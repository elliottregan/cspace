package controlplane

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// noScrollbackNotice is the refusal a pane with no history gives, shared by
// leader [ and the wheel so the two can never drift into saying different
// things about the same pane.
//
// tmux is the dominant reason: it switches the terminal to the ALTERNATE
// screen the moment it starts (measured against the image's tmux 3.3a: its
// first bytes are ESC[?1049h), and nothing written to the alternate screen
// ever enters scrollback — the permanent state of every Claude and shell
// pane, not a rare one. See the
// scroll-mode-never-reaches-a-tmux-backed-panes-history finding. But it is
// not the only reason a pane has none: an exited pane has no child at all
// to hold history for, and a freshly opened host shell (no tmux) simply
// has not filled a screen yet. The wording leads with the one thing true of
// all three — there is nothing here to scroll — and keeps the tmux
// explanation as a trailing, general aside rather than a claim about this
// particular pane.
//
// Kept to <= 78 cells on purpose: the footer's fit() elides at 120 columns
// and, worse, silently drops trailing clauses at 80 — and the trailing
// clause here is "PgUp/PgDn go there", the only actionable part of the
// whole sentence. A longer wording that reads fine at 120 becomes a
// refusal with no way forward at 80. Measured with lipgloss.Width.
func noScrollbackNotice() notice {
	return notice{
		text: "nothing to scroll: a tmux pane keeps its history inside — " +
			"PgUp/PgDn go there",
		isErr: true,
	}
}

// handleLeaderKey dispatches the key after the leader.
//
// The leader is already disarmed by the caller, so every path here is a
// single-shot: there is no mode to get stuck in, which is the property that
// makes a prefix key safe in a window whose other keys all belong to a
// child.
func (m Model) handleLeaderKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The leader twice sends the leader itself to the child — the standard
	// escape hatch, and the only way to type Ctrl+Space into a program. A
	// pane whose child has already exited has nothing to type into: the
	// same guard handlePaneKey applies to every ordinary key belongs here
	// too, or the byte sits in a queue nothing will ever drain.
	if key.Matches(msg, m.keys.Leader) {
		if t := m.focusedTab(); t != nil && t.p != nil {
			if _, _, exited := t.p.Exited(); !exited {
				t.p.SendKey(paneKey(msg))
			}
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.FocusSidebar):
		m.focus = focusSidebar
		m.scrolling, m.scroll = false, 0
		return m, nil

	case key.Matches(msg, m.keys.NextTab):
		return m.moveTab(1), nil
	case key.Matches(msg, m.keys.PrevTab):
		return m.moveTab(-1), nil

	case key.Matches(msg, m.keys.NewPane):
		// The one-action gate: an open already in flight must not be
		// overwritten by a second one before it reports back.
		if m.action != "" {
			return m, nil
		}
		m.mode = modePicker
		m.pending = m.selectedRow()
		m.picker = newPanePicker(mainWidthFor(m.width) - 2)
		return m, m.picker.Init()

	case key.Matches(msg, m.keys.ClosePane):
		// The one-action gate, as above: a close already in flight must not
		// be overwritten by another.
		if m.action != "" {
			return m, nil
		}
		t := m.focusedTab()
		if t == nil {
			return m, nil
		}
		return m.startAction(LabelClosePane, m.closeTab(t.id))

	case key.Matches(msg, m.keys.Scroll):
		t := m.focusedTab()
		if t == nil || t.p == nil {
			return m, nil
		}
		if t.p.ScrollbackLen() == 0 {
			// Refused rather than armed: there is nothing to walk back
			// through, and the mode is not inert — it swallows the next
			// keypress as the one that returns to live, so arming it here
			// costs a keystroke and shows a counter that can only ever say
			// "0 lines back". The wheel gives the same refusal.
			m.notice = noScrollbackNotice()
			return m, nil
		}
		m.scrolling = true
		return m, nil

	case key.Matches(msg, m.keys.Live):
		m.scrolling, m.scroll = false, 0
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil

	case key.Matches(msg, m.keys.Quit):
		// No confirmation: tmux holds every session. The panes are still
		// closed properly on the way out, so no tmux client is left attached
		// inside a sandbox and no client record is stranded for the next
		// start's sweep to find.
		m.quitting = true
		return m, m.quitCmd()

	case key.Matches(msg, m.keys.PasteImage):
		t := m.focusedTab()
		if t == nil || t.p == nil {
			// No tabs at all, or a supervisor tab, which runs no process.
			// Either way there is nowhere for a path to be typed.
			m.notice = notice{text: LabelPasteImage + ": no pane", isErr: true}
			return m, nil
		}
		if _, _, exited := t.p.Exited(); exited {
			m.notice = pasteExitedNotice()
			return m, nil
		}
		if m.action != "" {
			// The one-action gate, as the other leader keys apply it: two
			// concurrent osascript runs would race for one footer line.
			//
			// Unlike t and x this one says so. osascript is the slowest
			// thing behind any of these gates — a ten-second budget, and a
			// pasteboard conversion that can use it — so v is the key a
			// person is most likely to press twice, and a second silent
			// no-op reads as a broken binding rather than a busy one. The
			// answer names the action that is actually holding the gate,
			// which is not always this one.
			//
			// Through actionNote rather than m.notice: footer() ranks the
			// spinner above the notice, so a notice set here would be
			// invisible for exactly as long as it was true, and would then
			// surface in red once the action it describes had finished.
			m.actionNote = m.action + " is still in progress"
			return m, nil
		}
		return m.startAction(LabelPasteImage, m.pasteImageCmd(t))
	}
	return m, nil
}

// moveTab steps the focus through the tabs, wrapping. The step is the only
// thing it decides; focusTab does the rest, so the leader's n/p and a click
// on a tab cannot end up leaving the model in different states.
func (m Model) moveTab(dir int) Model {
	if len(m.tabs) == 0 {
		return m
	}
	return m.focusTab((m.focused + dir + len(m.tabs)) % len(m.tabs))
}

// handlePaneKey is what a focused pane's keyboard does: in scroll mode, move
// through the history; otherwise every key goes to the child.
func (m Model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.focusedTab()
	if t == nil {
		return m, nil
	}
	if t.sup != nil {
		return m.handleSupervisorKey(t, msg)
	}
	if t.p == nil {
		return m, nil
	}

	if m.scrolling {
		_, rows := m.paneSize()
		switch msg.String() {
		case "up":
			m.scroll = clampScroll(m.scroll+1, t.p.ScrollbackLen())
		case "down":
			m.scroll = clampScroll(m.scroll-1, t.p.ScrollbackLen())
		case "pgup":
			m.scroll = clampScroll(m.scroll+rows, t.p.ScrollbackLen())
		case "pgdown":
			m.scroll = clampScroll(m.scroll-rows, t.p.ScrollbackLen())
		case "home":
			m.scroll = t.p.ScrollbackLen()
		case "end":
			m.scroll = 0
		default:
			// Any other key returns to live, and is consumed doing so —
			// which is what the design says, and what stops a stray letter
			// landing in a child a person thought they were only reading.
			m.scrolling, m.scroll = false, 0
		}
		return m, nil
	}

	if _, _, exited := t.p.Exited(); exited {
		// There is nothing to type into: the child is gone and the pane has
		// already been reaped, so a key here would be queued for a writer
		// that has exited. Leader x is what the tab is still on screen for.
		return m, nil
	}
	t.p.SendKey(paneKey(msg))
	return m, nil
}

func clampScroll(n, max int) int {
	if n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

// paneKey converts a Bubble Tea keypress into the engine's own event.
//
// The modifier is a cast, not a table: pane.KeyMod's constants are declared
// to be ultraviolet's bit for bit, and bubbletea v2's tea.KeyMod *is*
// ultraviolet's. TestPaneKeyConversionMatchesBubbleteasModifiers is what
// keeps that true.
func paneKey(msg tea.KeyPressMsg) pane.KeyEvent {
	return pane.KeyEvent{
		Code: msg.Code,
		Mod:  pane.KeyMod(msg.Mod),
		Text: msg.Text,
	}
}

// pickerField is the form key the new-pane picker's answer comes back under.
const pickerField = "kind"

// newPanePicker is the leader's `t`: the four things a tab can be.
//
// The host shell is in the list and has no sidebar key, because it belongs
// to no sandbox — there is no row to press a key on. That is the whole
// reason the picker exists rather than a fourth sidebar binding.
func newPanePicker(width int) *huh.Form {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[Kind]().
				Key(pickerField).
				Title("New pane").
				Options(
					huh.NewOption("Claude session", KindClaude),
					huh.NewOption("Shell in the sandbox", KindShell),
					huh.NewOption("Supervisor events", KindSupervisor),
					huh.NewOption("Host shell", KindHostShell),
				),
		),
	).WithKeyMap(km).WithShowHelp(false).WithShowErrors(false).WithWidth(width)
}

// `huh.NewSelect[T comparable]() *Select[T]` and
// `huh.NewOption[T comparable](key string, value T) Option[T]` are both
// generic in v2.0.3, so Kind — an int — is a legal type argument.

// updatePicker feeds the picker and acts on its answer. Like the teardown
// confirmation it resolves m.pending — the row the picker was opened
// against — so a poll landing while it is open cannot retarget it.
func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.picker == nil {
		m.mode = modeNormal
		return m, nil
	}
	form, cmd := m.picker.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.picker = f
	}
	switch m.picker.State {
	case huh.StateCompleted:
		// huh/v2 has no typed getter beyond GetString/GetInt/GetBool, so
		// the Select's value comes back through Get(key) any. A failed
		// assertion yields KindClaude, which is the option a person pressing
		// Enter on the default would have got anyway.
		kind, _ := m.picker.Get(pickerField).(Kind)
		target := m.pending
		m.mode, m.picker = modeNormal, nil
		m.pending = control.Row{}
		// The picker is the one opener that does not come through forRow,
		// so the gate forRow applies to `enter`/`s`/`a` is applied here
		// instead. It matters because 4a Task 6 keeps a stopped sandbox on
		// screen WITH its container name, so an ungated open would sail
		// past this and die at the tmux probe with a transport-shaped
		// error. The host shell belongs to no sandbox and is always
		// allowed. (A supervisor view on a stopped sandbox is still
		// reachable — from the sidebar's `a`, which canSupervisor permits
		// because the event log lives on the host.)
		if kind != KindHostShell && !canAttach(target) {
			m.notice = notice{
				text:  "cannot open a " + kind.String() + " pane: " + target.Name + " is not running",
				isErr: true,
			}
			return m, nil
		}
		return m.openOrFocus(kind, target)
	case huh.StateAborted:
		m.mode, m.picker = modeNormal, nil
		m.pending = control.Row{}
		return m, nil
	}
	return m, cmd
}
