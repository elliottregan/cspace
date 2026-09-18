package controlplane

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// confirmField is the form key the teardown answer is read back under.
const confirmField = "confirm"

// newDownConfirm builds the teardown confirmation: one huh field, defaulting
// to "Keep it", so tearing a sandbox down takes an explicit y (or a move to
// the affirmative button and Enter). The wording names what goes with it,
// because control.Down wipes the clone, the sessions and the volumes.
//
// width is the main area's inner width. It has to be passed: huh.NewGroup
// hardcodes 80 columns and the model never forwards its tea.WindowSizeMsg to
// the form, so without this the prompt would wrap at 80 inside an area that
// is 74 wide on a 100-column terminal and be re-wrapped by the outer style.
// Form.WithWidth ignores a non-positive width, so an unsized model is safe.
func newDownConfirm(sandbox string, width int) *huh.Form {
	km := huh.NewDefaultKeyMap()
	// huh's default quit binding is ctrl+c alone, which the dashboard takes
	// before the form ever sees it — so esc is what cancels here.
	km.Quit = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	return huh.NewForm(
		huh.NewGroup(
			// Not Inline: at the main area's width (74 columns on a
			// 100-column terminal) the inline form truncates before either
			// button is visible.
			huh.NewConfirm().
				Key(confirmField).
				Title(fmt.Sprintf("Tear down %s? Its clone, sessions and volumes go with it.", sandbox)).
				Affirmative("Down it").
				Negative("Keep it"),
		),
	).WithKeyMap(km).WithShowHelp(false).WithShowErrors(false).WithWidth(width)
}

// updateConfirm feeds the form and acts on its outcome. huh reports both
// answers through State: Completed carries the value (which may be "no"),
// Aborted is esc.
//
// The Down call resolves m.pending — the row the confirmation was opened
// against, set by handleNormalKey's Teardown case — never m.selectedRow():
// a poll can land and move the selection while the form is still open, and
// the teardown has to act on the sandbox it named, not on whatever is
// selected by the time it is answered.
func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.confirm == nil { // defensive: no form, no mode
		m.mode = modeNormal
		m.pending = control.Row{}
		return m, nil
	}
	form, cmd := m.confirm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.confirm = f
	}
	switch m.confirm.State {
	case huh.StateCompleted:
		down := m.confirm.GetBool(confirmField)
		target := m.pending
		m.mode, m.confirm = modeNormal, nil
		m.pending = control.Row{}
		if !down {
			return m, nil
		}
		return m.startAction("down", m.actor.Down(target))
	case huh.StateAborted:
		m.mode, m.confirm = modeNormal, nil
		m.pending = control.Row{}
		return m, nil
	}
	return m, cmd
}
