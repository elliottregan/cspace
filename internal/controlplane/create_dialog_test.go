package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type createTestActor struct {
	recordingActor
	suggestedName string
	suggestErr    error
	projects      []string
	validated     []string
}

func (a *createTestActor) SuggestName(_ context.Context, project string) (string, error) {
	a.projects = append(a.projects, project)
	return a.suggestedName, a.suggestErr
}

func (a *createTestActor) ValidateName(project, name string) error {
	a.validated = append(a.validated, project+"/"+name)
	if name == "" || strings.ContainsAny(name, " /!") {
		return errors.New("invalid sandbox name")
	}
	return nil
}

func openCreateDialog(t *testing.T, actor *createTestActor, host PaneHost) Model {
	t.Helper()
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, actor, host)
	y := navigationLineOf(t, m, "project:alpha/new")
	m, cmd := clickCmd(t, m, 4, y)
	if len(actor.projects) != 0 {
		t.Fatal("suggesting a name performed I/O in the input handler")
	}
	m = pump(t, m, cmd)
	if m.mode != modeCreate || m.createForm == nil || m.creating.Project != "alpha" || m.creating.Name != actor.suggestedName {
		t.Fatalf("new container did not open its named project form: mode=%v creating=%+v", m.mode, m.creating)
	}
	return m
}

// Complete huh's asynchronous field/group transitions, retaining the boot
// command instead of executing it so tests can inspect the pending target.
func submitCreate(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(press("enter"))
	m = mm.(Model)
	for range 8 {
		if m.mode != modeCreate || cmd == nil {
			return m, cmd
		}
		var next tea.Cmd
		for _, msg := range drain(cmd) {
			if msg == nil {
				continue
			}
			mm, next = m.Update(msg)
			m = mm.(Model)
			if m.mode != modeCreate {
				return m, next
			}
		}
		cmd = next
	}
	t.Fatal("new-container form did not settle")
	return m, nil
}

func setCreateName(t *testing.T, m Model, name string) {
	t.Helper()
	input, ok := m.createForm.GetFocusedField().(*huh.Input)
	if !ok {
		t.Fatal("new container form has no name input")
	}
	input.Value(&name)
}

func TestNewContainerDialogSuggestsPlanetAndEscapeCancels(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	m := openCreateDialog(t, a, nopPaneHost{})
	if len(a.projects) != 1 || a.projects[0] != "alpha" || !strings.Contains(plain(m.createForm.View()), "venus") {
		t.Fatal("suggested name or owning project was lost")
	}
	m = step(t, m, "esc")
	if m.mode != modeNormal || m.createForm != nil || m.creating.Name != "" || len(a.up) != 0 || m.action != "" {
		t.Fatalf("cancel left a pending form or started a boot: mode=%v creating=%+v up=%+v", m.mode, m.creating, a.up)
	}
}

func TestEnterOnNewContainerOpensAndSubmitsTheDialog(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	for range len(m.navigation()) {
		if item, ok := m.selectedNavigation(); ok && item.id == "project:alpha/new" {
			break
		}
		m = step(t, m, "down")
	}
	if item, ok := m.selectedNavigation(); !ok || item.kind != navNewContainer || item.row.Project != "alpha" {
		t.Fatal("keyboard navigation did not reach alpha's New container row")
	}
	// Rendering contextual help must not disable the row's Enter action.
	_ = m.View()
	mm, cmd := m.Update(press("enter"))
	if len(a.projects) != 0 {
		t.Fatal("Enter performed name lookup inside the input handler")
	}
	m = pump(t, mm.(Model), cmd)
	if m.mode != modeCreate || m.createForm == nil || len(a.projects) != 1 || a.projects[0] != "alpha" {
		t.Fatalf("Enter did not open the selected project's form: mode=%v projects=%v", m.mode, a.projects)
	}
	m, cmd = submitCreate(t, m)
	if m.mode != modeNormal || m.action != LabelUp || cmd == nil || len(a.up) != 1 || a.up[0].Project != "alpha" || a.up[0].Name != "venus" {
		t.Fatalf("form Enter did not start one boot for alpha/venus: mode=%v action=%q up=%+v", m.mode, m.action, a.up)
	}
}

func TestNewContainerRejectsInvalidNameBeforeStartingBoot(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	m := openCreateDialog(t, a, nopPaneHost{})
	setCreateName(t, m, "invalid name!")
	m, _ = submitCreate(t, m)
	if m.mode != modeCreate || len(a.up) != 0 || len(a.validated) == 0 || !strings.Contains(plain(m.createForm.View()), "invalid sandbox name") {
		t.Fatalf("invalid name escaped validation: mode=%v validated=%v up=%v", m.mode, a.validated, a.up)
	}
	setCreateName(t, m, "feature-ui")
	m, cmd := submitCreate(t, m)
	if m.mode != modeNormal || m.action != LabelUp || cmd == nil || len(a.up) != 1 || a.up[0].Project != "alpha" || a.up[0].Name != "feature-ui" {
		t.Fatalf("valid corrected name did not start exactly one boot: action=%q up=%+v", m.action, a.up)
	}
}

func TestNewContainerSuccessfulBootOpensDefaultClaude(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	h := &fakeHost{t: t}
	m := openCreateDialog(t, a, h)
	m, _ = submitCreate(t, m)
	if len(a.up) != 1 || a.up[0].Name != "venus" || m.actionTarget.Name != "venus" {
		t.Fatalf("boot target is not the proposed container: %+v", a.up)
	}
	mm, cmd := m.Update(actionResultMsg{label: LabelUp})
	m = pump(t, mm.(Model), cmd)
	if len(h.opens) != 1 || h.opens[0] != KindClaude || len(h.rows) != 1 || h.rows[0].Name != "venus" || h.rows[0].Project != "alpha" {
		t.Fatalf("successful boot attached wrong pane: opens=%v rows=%+v", h.opens, h.rows)
	}
	if m.focusedTab() == nil || m.focusedTab().sandbox != "venus" || m.focus != focusMain || m.creating.Name != "" {
		t.Fatal("new Claude session did not become the active pane")
	}
}

func TestNewContainerFailedBootDoesNotAttach(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	h := &fakeHost{t: t}
	m := openCreateDialog(t, a, h)
	m, _ = submitCreate(t, m)
	mm, cmd := m.Update(actionResultMsg{label: LabelUp, err: errors.New("name already in use")})
	m = pump(t, mm.(Model), cmd)
	if len(h.opens) != 0 || m.action != "" || !m.notice.isErr || !strings.Contains(m.notice.text, "name already in use") || m.creating.Name != "" {
		t.Fatalf("failed boot silently attached or lost its error: opens=%v notice=%+v", h.opens, m.notice)
	}
}

func TestNewContainerNameFailureIsVisibleWithoutOpeningForm(t *testing.T) {
	a := &createTestActor{suggestErr: errors.New("registry unavailable")}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	mm, cmd := m.beginCreate("alpha")
	m = pump(t, mm.(Model), cmd)
	if m.mode != modeNormal || m.createForm != nil || m.action != "" || !m.notice.isErr || !strings.Contains(m.notice.text, "registry unavailable") {
		t.Fatalf("name lookup failure left an unusable form: mode=%v action=%q notice=%+v", m.mode, m.action, m.notice)
	}
}

func TestQuittingDuringNewContainerBootNamesUnfinishedTarget(t *testing.T) {
	a := &createTestActor{suggestedName: "venus"}
	m := openCreateDialog(t, a, nopPaneHost{})
	m, _ = submitCreate(t, m)
	// Sidebar browsing during the boot must not change the warning's subject.
	for _, item := range m.navigation() {
		if item.row.Name == "issue-42" {
			m.selectNavigation(item)
			break
		}
	}
	m = step(t, m, "q")
	if !m.quitting || !strings.Contains(m.QuitWarning(), "alpha/venus") || !strings.Contains(m.QuitWarning(), "cspace down --keep-state venus") || strings.Contains(m.QuitWarning(), "issue-42") {
		t.Fatalf("unfinished boot warning is missing or drifted: %q", m.QuitWarning())
	}
}
