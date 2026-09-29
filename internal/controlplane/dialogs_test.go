package controlplane

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/elliottregan/cspace/internal/control"
)

func TestErrorFooterOpensFullWrappedError(t *testing.T) {
	message := "container ls --all --format json: exit status 1\n" +
		strings.Repeat("diagnostic context ", 20) + "\nXPC connection error: Connection invalid"
	for _, width := range []int{80, 100, 140} {
		for _, source := range []string{"snapshot", "action"} {
			t.Run(fmt.Sprintf("%s/%d", source, width), func(t *testing.T) {
				m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
				mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				m = mm.(Model)
				if source == "snapshot" {
					m.snapErr = errors.New(message)
				} else {
					m.notice = notice{text: message, isErr: true}
				}
				footer := plain(m.footer())
				if strings.Contains(footer, "\n") || ansi.StringWidth(footer) > width || !strings.Contains(footer, "click for details") {
					t.Fatalf("error footer should fit one line and expose details: %q", footer)
				}
				focus := m.focus
				m = click(t, m, width/2, m.height-1)
				if m.mode != modeDetails || m.dialog == nil || !m.dialog.environment {
					t.Fatal("error footer did not open Environment Details")
				}
				_, bodyWidth, _ := m.modalFrame()
				text := strings.ReplaceAll(detailsText(m, bodyWidth), "\n", "")
				if !strings.Contains(text, strings.ReplaceAll(message, "\n", "")) {
					t.Fatalf("dialog lost error context: %q", text)
				}
				m = step(t, m, "esc")
				if m.hasModal() || m.focus != focus {
					t.Fatal("closing error details did not restore focus")
				}
			})
		}
	}
}

func TestErrorFooterCannotReplaceOpenModal(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.snapErr = errors.New("list failed")
	m = step(t, m, "o")
	dialog := m.dialog
	m = click(t, m, 5, m.height-1)
	if m.dialog != dialog || m.dialog.environment {
		t.Fatal("footer click replaced the existing container dialog")
	}
}

func detailsText(m Model, width int) string {
	var text []string
	for _, line := range m.detailsLines(width) {
		text = append(text, line.text)
	}
	return strings.Join(text, "\n")
}

func TestDialogsWrapAndScrollWithinTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {100, 24}, {140, 40}, {80, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
			mm, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = mm.(Model)
			m.headers[keyOf(m.selectedRow())] = headerSample{status: control.HeaderStatus{Branch: strings.Repeat("long-branch/", 24)}}
			mm, _ = m.openDetails(m.selectedRow(), false)
			m = mm.(Model)
			frame, width, height := m.modalFrame()
			if frame.x < 0 || frame.y < 0 || frame.x+frame.w > size.width || frame.y+frame.h > size.height {
				t.Fatalf("modal frame %+v exceeds terminal", frame)
			}
			lines := m.detailsLines(width)
			if len(lines) <= height {
				t.Fatal("fixture needs enough content to require scrolling")
			}
			var wrappedBranch string
			collect := false
			for _, line := range lines {
				if ansi.StringWidth(line.text) > width {
					t.Errorf("dialog line exceeds %d cells: %q", width, line.text)
				}
				if strings.HasPrefix(line.text, "Branch:") {
					collect = true
				}
				if collect && line.text == "" {
					break
				}
				if collect {
					wrappedBranch += line.text
				}
			}
			if !strings.Contains(wrappedBranch, m.headers[keyOf(m.selectedRow())].status.Branch) {
				t.Fatal("long branch was truncated instead of wrapping across dialog lines")
			}
			box, renderedFrame := m.modalView()
			if renderedFrame != frame || len(strings.Split(box, "\n")) > frame.h {
				t.Fatalf("rendered modal disagrees with geometry: frame=%+v lines=%d", frame, len(strings.Split(box, "\n")))
			}
			for _, line := range strings.Split(box, "\n") {
				if ansi.StringWidth(line) > frame.w {
					t.Fatalf("modal box exceeds frame width %d", frame.w)
				}
			}
			beforeFocus, beforeSelection := m.focus, m.navID
			mm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			m = mm.(Model)
			if m.modalScroll != len(lines)-height || m.focus != beforeFocus || m.navID != beforeSelection {
				t.Fatalf("End did not scroll only the dialog: scroll=%d want=%d", m.modalScroll, len(lines)-height)
			}
			bottom := m.modalScroll
			m = wheel(t, m, frame.x+2, frame.y+3, true)
			if m.modalScroll != max(0, bottom-wheelLines) {
				t.Fatal("mouse wheel did not scroll the open dialog")
			}
			mm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
			m = mm.(Model)
			if m.modalScroll != 0 {
				t.Fatal("Home did not return the dialog to its start")
			}
		})
	}
}

func TestDetailsDialogTabRevealsOffscreenActions(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = mm.(Model)
	mm, _ = m.openDetails(m.selectedRow(), false)
	m = mm.(Model)
	_, width, height := m.modalFrame()
	var actions []string
	for _, line := range m.detailsLines(width) {
		if line.action != nil {
			actions = append(actions, line.action.id)
		}
	}
	if len(actions) < 2 {
		t.Fatal("fixture needs several actions")
	}
	for _, action := range actions {
		m = step(t, m, "tab")
		if m.dialog.selected != action {
			t.Fatalf("Tab selected %q, want %q", m.dialog.selected, action)
		}
		visible := false
		for i, line := range m.detailsLines(width) {
			if line.action != nil && line.action.id == action {
				visible = i >= m.modalScroll && i < m.modalScroll+height
			}
		}
		if !visible {
			t.Fatalf("selected action %q is outside the dialog viewport", action)
		}
	}
	m = step(t, m, "tab")
	if m.dialog.selected != actions[0] {
		t.Fatal("Tab did not wrap to the first action")
	}
}

func TestHelpAndFormDialogsFitAndScrollAtSmallSizes(t *testing.T) {
	for _, kind := range []string{"help", "picker", "confirm", "create"} {
		for _, size := range []struct{ width, height int }{{80, 24}, {100, 24}, {140, 40}, {80, 8}} {
			t.Run(fmt.Sprintf("%s/%dx%d", kind, size.width, size.height), func(t *testing.T) {
				actor := &createTestActor{suggestedName: "venus"}
				m := newTestModel(&fakeData{snap: testSnapshot()}, actor)
				mm, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				m = mm.(Model)
				_, width, height := m.modalFrame()
				switch kind {
				case "help":
					m = step(t, m, "?")
				case "picker":
					m = leader(t, m, "t")
				case "confirm":
					m = step(t, m, "d")
				case "create":
					mm, cmd := m.beginCreate("alpha")
					m = pump(t, mm.(Model), cmd)
				}
				if !m.hasModal() {
					t.Fatalf("%s did not open", kind)
				}
				_, lines := m.modalContent(width)
				for _, line := range lines {
					if ansi.StringWidth(line.text) > width {
						t.Fatalf("modal content exceeds width %d: %q", width, line.text)
					}
				}
				box, frame := m.modalView()
				if frame.x < 0 || frame.y < 0 || frame.x+frame.w > size.width || frame.y+frame.h > size.height || len(strings.Split(box, "\n")) > frame.h {
					t.Fatalf("modal %+v does not fit terminal %dx%d", frame, size.width, size.height)
				}
				for _, line := range strings.Split(box, "\n") {
					if ansi.StringWidth(line) > frame.w {
						t.Fatalf("modal rendering exceeds frame width %d", frame.w)
					}
				}
				beforeFocus, beforeSelection := m.focus, m.navID
				mm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				m = mm.(Model)
				want := min(height, max(0, len(lines)-height))
				if m.modalScroll != want || m.focus != beforeFocus || m.navID != beforeSelection {
					t.Fatalf("PageDown did not scroll only modal: got %d want %d", m.modalScroll, want)
				}
				if size.height == 8 && want == 0 {
					t.Fatal("short-terminal fixture must have hidden content")
				}
				m = wheel(t, m, frame.x+2, frame.y+3, false)
				if m.modalScroll != min(max(0, len(lines)-height), want+wheelLines) {
					t.Fatal("wheel did not scroll the open modal")
				}
				before := m.modalScroll
				mm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
				m = mm.(Model)
				if m.modalScroll != max(0, before-height) {
					t.Fatal("PageUp did not scroll modal back")
				}
				m = step(t, m, "esc")
				if m.hasModal() || m.focus != beforeFocus || m.navID != beforeSelection || len(actor.up) != 0 || len(actor.down) != 0 {
					t.Fatal("Escape changed focus, selection, or performed an action")
				}
			})
		}
	}
}

func TestDetailsEscapeRestoresPaneFocusAndIsolatesKeysAndPaste(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	m := openOne(t, h)
	beforeID, beforeFocus := m.focusedTab().id, m.focus
	m = leader(t, m, "o")
	if m.mode != modeDetails || m.dialog == nil {
		t.Fatal("leader o did not open details above the pane")
	}
	if m.View().Cursor != nil {
		t.Fatal("pane cursor remained visible through the dialog")
	}
	for _, key := range []string{"x", "q", "d"} {
		m = step(t, m, key)
	}
	mm, _ := m.Update(tea.PasteMsg{Content: "MODAL_PASTE_MUST_NOT_REACH_PANE"})
	m = mm.(Model)
	if m.mode != modeDetails || m.quitting || len(m.tabs) != 1 || m.focusedTab().id != beforeID {
		t.Fatal("dialog input changed the pane, closed the dialog, or quit")
	}
	m = step(t, m, "esc")
	if m.mode != modeNormal || m.dialog != nil || m.focus != beforeFocus || m.focusedTab().id != beforeID {
		t.Fatal("Escape did not restore the same pane and focus")
	}
	// A real echoed byte is a barrier for anything incorrectly queued earlier.
	m = step(t, m, "z")
	waitForPaneScreen(t, m.tabs[0], "z")
	if text := plain(m.tabs[0].p.Render()); strings.Contains(text, "MODAL_PASTE") || strings.Contains(text, "xqd") {
		t.Fatalf("dialog input leaked into the underlying child: %q", text)
	}
}

func TestDetailsRefreshStaysPinnedAndDropsForeignEvents(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	row := m.selectedRow()
	mm, _ := m.openDetails(row, false)
	m = mm.(Model)
	snap := testSnapshot()
	snap.Rows[1].MemoryB = 8 << 30
	snap.Rows[1].IP = "10.0.0.55"
	snap.Rows[1], snap.Rows[3] = snap.Rows[3], snap.Rows[1]
	mm, _ = m.Update(snapshotMsg{snap: snap})
	m = mm.(Model)
	mm, _ = m.Update(eventsMsg{key: keyOf(row), lines: []control.EventLine{{Kind: "user-turn", Text: "pinned event"}}})
	m = mm.(Model)
	mm, _ = m.Update(eventsMsg{key: sandboxKey{Project: "alpha", Name: "issue-42"}, lines: []control.EventLine{{Kind: "foreign-event"}}})
	m = mm.(Model)
	if m.dialog == nil || keyOf(m.dialog.row) != keyOf(row) || len(m.events) != 1 || m.events[0].Kind != "user-turn" {
		t.Fatalf("dialog or events drifted during refresh: dialog=%+v events=%+v", m.dialog, m.events)
	}
	text := detailsText(m, 84)
	if !strings.Contains(text, "10.0.0.55") || !strings.Contains(text, "mercury") || strings.Contains(text, "foreign-event") {
		t.Fatalf("pinned dialog did not refresh its own data: %q", text)
	}
	// A removed target stays named but loses destructive/lifecycle actions.
	var remaining []control.Row
	for _, candidate := range snap.Rows {
		if keyOf(candidate) != keyOf(row) {
			remaining = append(remaining, candidate)
		}
	}
	snap.Rows = remaining
	mm, _ = m.Update(snapshotMsg{snap: snap})
	m = mm.(Model)
	if !strings.Contains(detailsText(m, 84), "no longer") {
		t.Fatal("removed container looks live in its open dialog")
	}
	for _, line := range m.detailsLines(84) {
		if line.action != nil && line.action.id == "down" {
			t.Fatal("removed container still has a teardown action")
		}
	}
}

func TestProjectDetailsKeyTargetsSelectedProjectsEnvironment(t *testing.T) {
	snap := testSnapshot()
	snap.Rows = append(snap.Rows, control.Row{Kind: control.RowProject, Project: "beta", Name: "beta"}, control.Row{Kind: control.RowBrowser, Project: "beta", Name: "browser (shared)", Container: "cspace-beta-browser"})
	m := newTestModelWithHost(&fakeData{snap: snap}, &recordingActor{}, &fakeHost{t: t})
	m = stepPump(t, m, "enter")
	if m.focusedTab() == nil || m.focusedTab().project != "alpha" {
		t.Fatal("fixture needs an active pane in a different project")
	}
	m = leader(t, m, "h")
	for _, item := range m.navigation() {
		if item.id == "project:beta" {
			m.selectNavigation(item)
		}
	}
	m = step(t, m, "o")
	if m.mode != modeDetails || m.dialog == nil || !m.dialog.environment || m.dialog.project != "beta" {
		t.Fatalf("project Details targets the wrong environment: %+v", m.dialog)
	}
	text := detailsText(m, 84)
	if !strings.Contains(text, "cspace-beta-browser") || strings.Contains(text, "cspace-alpha-browser") {
		t.Fatalf("project environment mixed browser details: %q", text)
	}
}
