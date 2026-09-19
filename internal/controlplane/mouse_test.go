package controlplane

import (
	"errors"
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
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, nopPaneHost{}, nopClipboard{}, NewKeyMap(nil))
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

// TestClickOnAnElisionMarkerDoesNothing pins that the "+N" head of an
// elided tabs row belongs to no tab.
//
// The setup is built so a hit test that DID claim the marker's columns
// would be visible twice over: three tabs narrowed until exactly one is
// dropped, so the span nearest the marker is a tab that is not the focused
// one (a claim would move `focused`), and the keyboard pointed at the
// sidebar (a claim would also move `focus`). Asserting on `focused` alone
// against a row where the only surviving span IS the focused tab — which
// is what two tabs narrowed to one gives — cannot tell a no-op from a
// wrong hit, which is how the first version of this test passed either way.
func TestClickOnAnElisionMarkerDoesNothing(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	m.focus = focusSidebar
	m = stepPump(t, m, "a")
	mustTabs(t, m, 3)
	// Set before the resize, so the geometry the click is tested against is
	// the one this state renders.
	m.focus = focusSidebar
	// Narrow enough to drop one tab and keep two.
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = mm.(Model)

	if len(m.geom.tabs) != 2 {
		t.Fatalf("tab spans = %+v, want two: one tab elided and two kept", m.geom.tabs)
	}
	if m.geom.tabs[0].index == m.focused {
		t.Fatalf("the span next to the marker is the focused tab (%d); this setup cannot tell a wrong hit from a no-op",
			m.focused)
	}
	if !strings.Contains(plain(m.tabsRow(mainWidthFor(m.width))), "+") {
		t.Fatal("expected an elision marker")
	}
	// The marker sits at the head of the row, in the columns before the
	// first surviving span. The row starts where the sidebar ends.
	marker := sidebarWidth
	if marker >= m.geom.tabs[0].from {
		t.Fatalf("no columns before the first span for a marker: %+v", m.geom.tabs)
	}

	beforeFocused, beforeFocus := m.focused, m.focus
	got := click(t, m, marker, m.geom.tabsY)
	if got.focused != beforeFocused {
		t.Errorf("focused moved to %d; a marker is not a tab", got.focused)
	}
	if got.focus != beforeFocus {
		t.Error("clicking a marker pointed the keyboard at the main area; a marker is not a tab")
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

// TestClickUnderAModalIsSwallowedWithoutAnsweringIt pins the
// `m.mode != modeNormal` swallow.
//
// The click has to land somewhere the same click WOULD act in modeNormal,
// or the test proves nothing: the first version clicked the empty main
// area of a model with no tabs, which the main arm refuses anyway, so it
// passed with the swallow deleted. A selectable sidebar row that is not
// the current selection is the dangerous case — with the swallow gone it
// moves the selection, and the detail band, out from under a prompt that
// names a different sandbox.
func TestClickUnderAModalIsSwallowedWithoutAnsweringIt(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "d") // the teardown confirmation
	if m.mode != modeConfirmDown {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	beforeSelected, beforeFocus := m.selected, m.focus
	if m.rows[3].Name != "issue-42" || !m.rows[3].Selectable || beforeSelected == 3 {
		t.Fatalf("fixture moved: rows[3] must be a selectable row other than the selection (%d)", beforeSelected)
	}
	y := listLineOf(t, m, 3)

	got, cmd := clickCmd(t, m, 4, y)
	if got.mode != modeConfirmDown {
		t.Errorf("mode = %v; a click is not an answer and must not cancel a prompt", got.mode)
	}
	if got.selected != beforeSelected {
		t.Errorf("selected moved to %d under an open prompt; the detail band belongs to the row the prompt named",
			got.selected)
	}
	if got.focus != beforeFocus {
		t.Error("the keyboard moved under an open prompt")
	}
	if cmd != nil {
		t.Error("a swallowed click must not start anything")
	}
	if got.pending.Name != m.pending.Name {
		t.Errorf("pending = %q, want the row the prompt was opened against (%q)", got.pending.Name, m.pending.Name)
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

// TestMotionAndReleaseNeverReachTheWidgets pins the
// `case tea.MouseReleaseMsg, tea.MouseMotionMsg:` arm, which consumes both
// above Update's fall-through to whichever widget owns the keyboard.
//
// Neither bubbles' textinput nor huh reads a mouse message today — huh
// v2.0.3 contains no reference to Mouse at all — so "the send box is
// unchanged" and "no command came back" are both true whether or not the
// arm exists. That is how the first version of this test passed with the
// arm deleted.
//
// What does tell the two paths apart: textinput.Update re-runs
// handleOverflow on EVERY message, whatever its type. So a send box whose
// scroll window has gone stale — a value wider than the box, then a
// narrower window — renders differently the moment anything at all passes
// through the widget. The test asserts that discrimination first, so if a
// future bubbles stops recomputing, this fails loudly instead of quietly
// going vacuous again.
func TestMotionAndReleaseNeverReachTheWidgets(t *testing.T) {
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{X: 30, Y: 5, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: 30, Y: 5, Button: tea.MouseLeft},
	} {
		m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
		m = step(t, m, "m") // the send box
		if m.mode != modeInput {
			t.Fatalf("mode = %v, want the send box", m.mode)
		}
		// Through the message path, not by poking the field: the paste
		// falls through to the textinput exactly as a real one does.
		mm, _ := m.Update(tea.PasteMsg{Content: strings.Repeat("0123456789", 12)})
		m = mm.(Model)
		mm, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
		m = mm.(Model)
		before := m.input.View()

		if probe, _ := m.input.Update(msg); probe.View() == before {
			t.Fatalf("%T no longer changes a stale send box; this test can no longer tell "+
				"a dropped message from one handed to the widget", msg)
		}

		got, cmd := m.Update(msg)
		g := got.(Model)
		if cmd != nil {
			t.Errorf("%T produced a command; it must be dropped", msg)
		}
		if g.input.View() != before {
			t.Errorf("%T reached the send box", msg)
		}
		if g.input.Value() != m.input.Value() {
			t.Errorf("%T changed the send box's text", msg)
		}
		if g.mode != modeInput {
			t.Errorf("%T left the send box: mode = %v", msg, g.mode)
		}
	}
}

// TestClickOnTheSidebarBandAndRule covers the arm that catches everything
// in the sidebar that is not a row: the vertical rule, the detail band
// below it, and a list line that belongs to no row. All three point the
// keyboard at the sidebar — the person did ask for that by clicking in it
// — and none of them moves the selection or re-reads any events.
func TestClickOnTheSidebarBandAndRule(t *testing.T) {
	base := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	g := base.geom

	// A list line inside the row list that maps to no row: the fixture has
	// five rows and a taller column than that.
	padding := -1
	for y, idx := range g.listRows {
		if idx < 0 {
			padding = y
			break
		}
	}
	if padding < 0 {
		t.Fatalf("the fixture fills the list; no empty line to click: %v", g.listRows)
	}

	cases := []struct {
		name string
		x, y int
	}{
		{"the detail band", 4, g.list.y + g.list.h + 2},
		{"the vertical rule", sidebarWidth - 1, 1},
		{"a list line belonging to no row", 4, g.list.y + padding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !g.sidebar.contains(tc.x, tc.y) {
				t.Fatalf("(%d,%d) is not in the sidebar %+v", tc.x, tc.y, g.sidebar)
			}
			m := base
			m.focus = focusMain
			before := m.selected

			got, cmd := clickCmd(t, m, tc.x, tc.y)
			if got.focus != focusSidebar {
				t.Error("a click in the sidebar did not point the keyboard at it")
			}
			if got.selected != before {
				t.Errorf("selected moved to %d; there is no row under the pointer", got.selected)
			}
			if cmd != nil {
				t.Error("nothing was selected, so there is nothing to re-read")
			}
		})
	}
}

// TestAnyClickDismissesAnErrorNotice pins that a click is input for the
// purpose of the footer whatever button made it, which is the rule
// handleKey applies to every key.
func TestAnyClickDismissesAnErrorNotice(t *testing.T) {
	base := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	for _, button := range []tea.MouseButton{tea.MouseLeft, tea.MouseMiddle, tea.MouseRight} {
		mm, _ := base.Update(actionResultMsg{label: LabelSend, err: errors.New("boom")})
		m := mm.(Model)
		if !m.notice.isErr {
			t.Fatalf("setup: the footer holds no error notice (%+v)", m.notice)
		}
		mm, _ = m.Update(tea.MouseClickMsg{X: 4, Y: 1, Button: button})
		if got := mm.(Model).notice; got.text != "" {
			t.Errorf("a %v click left %q in the footer; any key would have dismissed it", button, got.text)
		}
	}
}

// wheel delivers one notch. up is +1 in the model's own direction — toward
// older output, toward the row above.
func wheel(t *testing.T, m Model, x, y int, up bool) Model {
	t.Helper()
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	mm, _ := m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: button})
	return mm.(Model)
}

func TestWheelOverTheSidebarMovesTheSelection(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.selected = 1 // mercury
	m.focus = focusMain

	down := wheel(t, m, 4, 3, false)
	if down.selected != 3 {
		t.Errorf("selected = %d, want the next selectable row (3)", down.selected)
	}
	if down.focus != focusMain {
		t.Error("the wheel moved the focus; it is a look, not a commitment")
	}
	up := wheel(t, down, 4, 3, true)
	if up.selected != 1 {
		t.Errorf("selected = %d, want back at mercury (1)", up.selected)
	}
}

func TestWheelOverATmuxBackedPaneRefusesLikeLeaderBracket(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h) // `sleep 30`: nothing on the alternate screen, and no scrollback

	byKey := leader(t, m, "[")
	byWheel := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)

	if byWheel.scrolling {
		t.Error("the wheel armed scroll mode on a pane with no scrollback")
	}
	if byWheel.notice.text != byKey.notice.text {
		t.Errorf("wheel says %q, leader [ says %q — they must say the same thing",
			byWheel.notice.text, byKey.notice.text)
	}
	if !byWheel.notice.isErr {
		t.Error("the refusal should stay until the next keypress")
	}
	if !strings.Contains(byWheel.notice.text, "PgUp") {
		t.Errorf("the refusal must say where the history is: %q", byWheel.notice.text)
	}
}

// A pane whose child prints to the NORMAL screen, which the fake host's
// `history` child does and a real tmux-backed pane never does. openOne
// presses enter, so this is a Claude-kind tab — but the fake runs /bin/sh
// for every kind, so it stands in for a host shell. The distinction the
// refusal test above turns on is tmux and the alternate screen, not the
// tab's kind.
func TestWheelOnAPaneWithScrollbackEntersScrollModeAndScrolls(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 2*wheelLines)

	up := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if !up.scrolling {
		t.Fatal("the wheel did not enter scroll mode on a pane with scrollback")
	}
	if up.scroll != wheelLines {
		t.Errorf("scroll = %d, want one notch (%d)", up.scroll, wheelLines)
	}
	further := wheel(t, up, up.geom.main.x+4, up.geom.main.y+4, true)
	if further.scroll != 2*wheelLines {
		t.Errorf("scroll = %d, want two notches", further.scroll)
	}
	back := wheel(t, further, further.geom.main.x+4, further.geom.main.y+4, false)
	if back.scroll != wheelLines {
		t.Errorf("scroll = %d after a notch down, want one notch", back.scroll)
	}
}

func TestWheelDownOnALivePaneDoesNotArmScrollMode(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 1)

	got := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, false)
	if got.scrolling {
		t.Error("wheeling down from the live screen armed scroll mode; there is nothing below it")
	}
}

func TestWheelOverThePaneWithTheSidebarFocusedDoesNotArmScrollMode(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 2*wheelLines)
	m.focus = focusSidebar

	got := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if got.scrolling {
		t.Error("the wheel armed scroll mode while the keyboard was on the sidebar: " +
			"only handlePaneKey leaves that mode, and the sidebar's keys never reach it, " +
			"so the pane would sit under a banner promising that any key returns to live")
	}
	if got.focus != focusSidebar {
		t.Error("the wheel moved the focus")
	}
}

func TestWheelOverASupervisorTabScrollsItsViewport(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "a")
	mustTabs(t, m, 1)
	sup := m.tabs[0].sup
	if sup == nil {
		t.Fatal("the supervisor tab has no view")
	}
	sup.vp.SetContent(strings.Repeat("line\n", 200))
	sup.vp.GotoBottom()
	before := sup.vp.YOffset()
	if before == 0 {
		t.Fatal("the viewport did not scroll to the bottom")
	}

	wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if got := before - sup.vp.YOffset(); got != wheelLines {
		t.Errorf("one notch up moved %d lines, want %d (wheelLines)", got, wheelLines)
	}

	wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, false)
	if got := sup.vp.YOffset(); got != before {
		t.Errorf("one notch down after one up left YOffset at %d, want back at %d", got, before)
	}
}

func TestWheelNeverReachesTheChild(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	m := openOne(t, h)
	for i := 0; i < 5; i++ {
		m = wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
		m = wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, false)
	}
	// A key the child WILL echo, sent after all ten notches, is the
	// barrier: once "z" is on the screen the pty has delivered everything
	// queued before it, so an absent escape sequence is absent rather than
	// merely late. A sleep would only prove the test was patient.
	m = step(t, m, "z")
	waitForPaneScreen(t, m.tabs[0], "z")
	// `cat -v` prints ESC as ^[ , so a forwarded SGR report would read
	// "^[[<64;...".
	if screen := plain(m.tabs[0].p.Render()); strings.Contains(screen, "^[[<") {
		t.Errorf("a mouse sequence reached the child: %q", screen)
	}
}

func TestWheelIsInertUnderAModalAndTheHelpOverlay(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	before := m.selected

	help := step(t, m, "?")
	if got := wheel(t, help, 4, 3, false); got.selected != before {
		t.Error("the wheel moved the selection behind the help overlay")
	}
	box := step(t, m, "m")
	if got := wheel(t, box, 4, 3, false); got.selected != before {
		t.Error("the wheel moved the selection behind the send box")
	}
}

// TestWheelDisarmsTheLeaderAndIsSwallowed pins review-task3.md's Important 1:
// a half-typed leader chord left armed by a wheel notch outlives the notch,
// and the very next ordinary key is then dispatched as the chord's second
// key instead of going where a person watching scroll mode's own "any other
// key returns to live" banner would expect it to. Matches handleClick's
// existing rule for exactly the same hazard.
func TestWheelDisarmsTheLeaderAndIsSwallowed(t *testing.T) {
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], wheelLines)
	m = step(t, m, "ctrl+space")
	if !m.leaderArmed {
		t.Fatal("ctrl+space did not arm the leader")
	}

	got := wheel(t, m, m.geom.main.x+4, m.geom.main.y+4, true)
	if got.leaderArmed {
		t.Error("a wheel notch left the leader armed")
	}
	if got.scrolling {
		t.Error("the notch that disarmed the leader also acted, arming scroll mode")
	}

	// The leader outliving the notch is what let 't' — a leader chord's own
	// second key — open the new-pane picker over a pane still (invisibly)
	// promising that any key returns to live.
	next := stepPump(t, got, "t")
	if next.mode == modePicker {
		t.Error("the leader survived the wheel notch: 't' opened the new-pane picker")
	}
}

// TestWheelOverTheSidebarAtTheEndsFiresNoEventsRead pins review-task3.md's
// Minor 3: the click path already returns nil when the selection could not
// move (selectListRow, mouse.go); the wheel must match it. A trackpad emits
// notches an order of magnitude faster than key repeat, so an unconditional
// events read at a boundary is wasted work the click path already avoids.
func TestWheelOverTheSidebarAtTheEndsFiresNoEventsRead(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})

	// mercury (1) is the first selectable row.
	m.selected = 1
	if got := m.rows[m.selected].Name; got != "mercury" {
		t.Fatalf("fixture moved: rows[%d] = %q, want mercury", m.selected, got)
	}
	if _, cmd := m.Update(tea.MouseWheelMsg{X: 4, Y: 3, Button: tea.MouseWheelUp}); cmd != nil {
		t.Error("wheeling up past the first selectable row fired an events read")
	}

	// "browser (shared)" (4) is the last selectable row.
	m.selected = 4
	if got := m.rows[m.selected].Name; got != "browser (shared)" {
		t.Fatalf("fixture moved: rows[%d] = %q, want browser (shared)", m.selected, got)
	}
	if _, cmd := m.Update(tea.MouseWheelMsg{X: 4, Y: 3, Button: tea.MouseWheelDown}); cmd != nil {
		t.Error("wheeling down past the last selectable row fired an events read")
	}
}
