package controlplane

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/elliottregan/cspace/internal/control"
)

type createReadyMsg struct {
	project, name string
	err           error
}

func (m Model) beginCreate(project string) (tea.Model, tea.Cmd) {
	namer, ok := m.actor.(SandboxNamer)
	if !ok {
		m.notice = notice{text: "container naming unavailable", isErr: true}
		return m, nil
	}
	return m.startAction("choose container name", func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		name, err := namer.SuggestName(ctx, project)
		return createReadyMsg{project: project, name: name, err: err}
	})
}
func (m Model) newCreateForm(project, name string) *huh.Form {
	_, width, _ := m.modalFrame()
	input := huh.NewInput().Key("name").Title("Container name").Value(&name).Validate(func(s string) error {
		if namer, ok := m.actor.(SandboxNamer); ok {
			return namer.ValidateName(project, s)
		}
		return fmt.Errorf("container naming unavailable")
	})
	if name == "" {
		input.Description("All planet names are in use. Enter a custom name.")
	}
	return huh.NewForm(huh.NewGroup(input)).WithKeyMap(modalKeymap()).WithWidth(width).WithShowHelp(false)
}
func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.createForm == nil {
		return m, nil
	}
	form, cmd := m.createForm.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.createForm = f
	}
	switch m.createForm.State {
	case huh.StateCompleted:
		row := control.Row{Kind: control.RowSandbox, Project: m.creating.Project, Name: m.createForm.GetString("name")}
		m.creating = row
		m.actionTarget = row
		m.mode = modeNormal
		m.createForm = nil
		return m.startAction(LabelUp, m.actor.Up(row))
	case huh.StateAborted:
		m.mode = modeNormal
		m.createForm = nil
		m.creating = control.Row{}
	}
	return m, cmd
}
