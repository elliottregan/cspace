package controlplane

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// click delivers a left-button press at a zero-based screen cell and
// returns the new model. The release that a real terminal sends after it
// is delivered too, because the model must ignore it — a click that acted
// twice would move the selection twice.
func click(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	mm, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = mm.(Model)
	mm, _ = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return mm.(Model)
}

// clickCmd is click without the release, for the cases that assert on the
// command a click produced.
func clickCmd(t *testing.T, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return mm.(Model), cmd
}

// listLineOf is the list line the given row index is drawn on, read out of
// the geometry rather than guessed.
func listLineOf(t *testing.T, m Model, row int) int {
	t.Helper()
	for y, idx := range m.geom.listRows {
		if idx == row {
			return y
		}
	}
	t.Fatalf("row %d is not on screen; listRows = %v", row, m.geom.listRows)
	return -1
}

func TestViewEnablesCellMotionMouseMode(t *testing.T) {
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, NewKeyMap(nil))
	// Before the first WindowSizeMsg, too: the mode is a property of every
	// view, and the starting screen is a view.
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("starting view MouseMode = %v, want cell motion", got)
	}
	sized := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	if got := sized.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("dashboard view MouseMode = %v, want cell motion", got)
	}
}

func TestClickOnASidebarRowSelectsIt(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain

	// The fixture's second selectable sandbox: rows[3] is issue-42.
	want := 3
	if m.rows[want].Name != "issue-42" {
		t.Fatalf("fixture moved: rows[3] = %q", m.rows[want].Name)
	}
	y := listLineOf(t, m, want)

	got, cmd := clickCmd(t, m, 4, y)
	if got.selected != want {
		t.Errorf("selected = %d (%q), want %d (issue-42)", got.selected, got.rows[got.selected].Name, want)
	}
	if got.focus != focusSidebar {
		t.Error("a click in the sidebar did not point the keyboard at it")
	}
	if cmd == nil {
		t.Error("selecting a row must re-read its events, as j/k do")
	}
}

func TestClickOnAPortLineSelectsItsSandbox(t *testing.T) {
	snap := testSnapshot()
	m := newTestModel(&fakeData{snap: snap}, &recordingActor{})
	// A port line under mercury (rows[1]) belongs to mercury.
	key := sandboxKey{Project: "alpha", Name: "mercury"}
	mm, _ := m.Update(slowMsg{snap: snap, ports: map[sandboxKey][]control.Port{
		key: {{Port: 5173, Label: "web", URL: "http://mercury.alpha.cspace.test:5173/"}},
	}, portsErr: map[sandboxKey]error{}})
	m = mm.(Model)
	// Move off mercury through Update, not by assignment: the geometry is
	// rebuilt after every message, and a selection set behind its back
	// would be hit-tested against a layout that never existed.
	m = step(t, m, "down") // issue-42

	// The line after mercury's own is its port line, and both map to row 1.
	mercury := listLineOf(t, m, 1)
	got := click(t, m, 6, mercury+1)
	if got.selected != 1 {
		t.Errorf("selected = %d, want mercury (1): a port line belongs to its sandbox", got.selected)
	}
}

func TestClickOnANonSelectableRowOnlyMovesFocus(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain
	before := m.selected

	header := listLineOf(t, m, 0) // the project header
	got := click(t, m, 4, header)
	if got.selected != before {
		t.Errorf("selected moved to %d; a project header cannot be selected", got.selected)
	}
	if got.focus != focusSidebar {
		t.Error("the click should still point the keyboard at the sidebar")
	}
}

func TestClickOnATabFocusesIt(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)
	m.focused = 1

	first := m.geom.tabs[0]
	got := click(t, m, first.from+1, m.geom.tabsY)
	if got.focused != 0 {
		t.Errorf("focused = %d, want the clicked tab (0)", got.focused)
	}
	if got.focus != focusMain {
		t.Error("clicking a tab did not point the keyboard at the main area")
	}

	// The span is half-open, so both of its own edges belong to it and the
	// column after it belongs to the next tab. An interior click cannot
	// tell those apart, and lighting the tab next door is exactly what two
	// copies of the elision arithmetic would produce.
	if edge := click(t, m, first.from, m.geom.tabsY); edge.focused != 0 {
		t.Errorf("focused = %d after a click on the span's first column, want 0", edge.focused)
	}
	if edge := click(t, m, first.to-1, m.geom.tabsY); edge.focused != 0 {
		t.Errorf("focused = %d after a click on the span's last column, want 0", edge.focused)
	}
	if len(got.geom.tabs) != 2 {
		t.Fatalf("tab spans = %+v, want one per tab", got.geom.tabs)
	}
	second := got.geom.tabs[1]
	if got.geom.tabs[0].to != second.from {
		t.Fatalf("the spans are not adjacent: %+v", got.geom.tabs)
	}
	if edge := click(t, got, second.from, got.geom.tabsY); edge.focused != 1 {
		t.Errorf("focused = %d after a click on the second span's first column, want 1", edge.focused)
	}
}

func TestClickOnAnElisionMarkerDoesNothing(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)
	// Narrow enough that at least one tab is elided.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 48, Height: 24})
	m = mm.(Model)
	if len(m.geom.tabs) >= 2 {
		t.Skip("both tabs still fit; nothing was elided")
	}
	if !strings.Contains(plain(m.tabsRow(mainWidthFor(m.width))), "+") {
		t.Fatal("expected an elision marker")
	}
	before := m.focused
	// Column 0 of the row is the marker.
	got := click(t, m, sidebarWidth, m.geom.tabsY)
	if got.focused != before {
		t.Errorf("focused moved to %d; a marker is not a tab", got.focused)
	}
}

func TestClickInThePaneAreaFocusesMain(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar

	got := click(t, m, m.geom.main.x+5, m.geom.main.y+3)
	if got.focus != focusMain {
		t.Error("a click in the pane area did not focus it")
	}
}

func TestClickInTheMainAreaWithNoTabsDoesNothing(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	got := click(t, m, m.geom.main.x+5, m.geom.main.y+3)
	if got.focus != focusSidebar {
		t.Error("focus moved to a main area with nothing in it")
	}
}

func TestClickDisarmsTheLeaderAndIsSwallowed(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m = step(t, m, "ctrl+space")
	if !m.leaderArmed {
		t.Fatal("the leader did not arm")
	}
	before := m.focused
	got := click(t, m, m.geom.main.x+2, m.geom.main.y+1)
	if got.leaderArmed {
		t.Error("a click left the leader armed")
	}
	if got.focused != before {
		t.Error("the click that disarmed the leader also acted")
	}
}

func TestClickDismissesTheHelpOverlayAndIsSwallowed(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the overlay")
	}
	before := m.selected
	other := listLineOf(t, m, 3)
	got := click(t, m, 4, other)
	if got.showHelp {
		t.Error("the click did not dismiss the overlay")
	}
	if got.selected != before {
		t.Error("the click that dismissed the overlay also selected a row")
	}
}

func TestClickUnderAModalIsSwallowedWithoutAnsweringIt(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "d") // the teardown confirmation
	if m.mode != modeConfirmDown {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	got := click(t, m, m.geom.main.x+5, m.geom.main.y+2)
	if got.mode != modeConfirmDown {
		t.Errorf("mode = %v; a click is not an answer and must not cancel a prompt", got.mode)
	}
}

func TestANonLeftClickDoesNothing(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain
	before := m.selected
	y := listLineOf(t, m, 3)
	mm, _ := m.Update(tea.MouseClickMsg{X: 4, Y: y, Button: tea.MouseRight})
	got := mm.(Model)
	if got.selected != before || got.focus != focusMain {
		t.Error("a right click acted like a left one")
	}
}

func TestMotionAndReleaseNeverReachTheWidgets(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "m") // the send box
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want the send box", m.mode)
	}
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: 30, Y: 5, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: 30, Y: 5, Button: tea.MouseLeft},
	} {
		mm, cmd := m.Update(msg)
		if cmd != nil {
			t.Errorf("%T produced a command; it must be dropped", msg)
		}
		if mm.(Model).input.Value() != "" {
			t.Errorf("%T reached the send box", msg)
		}
	}
}
