package controlplane

import (
	"context"
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

func navigationLineOf(t *testing.T, m Model, id string) int {
	t.Helper()
	for y, item := range m.geom.navItems {
		if item.id == id {
			return m.geom.list.y + y
		}
	}
	t.Fatalf("navigation item %q is not visible: %+v", id, m.geom.navItems)
	return -1
}

type mouseSessionHost struct {
	*fakeHost
	requests []control.AttachRequest
}

func (h *mouseSessionHost) OpenSession(ctx context.Context, row control.Row, req control.AttachRequest, cols, rows int) (Opened, error) {
	h.requests = append(h.requests, req)
	opened, err := h.Open(ctx, KindClaude, row, cols, rows)
	opened.Session = control.Session{Name: req.Session, ID: req.ExpectedID}
	return opened, err
}

type mouseLinkOpener struct {
	urls []string
	err  error
}

func (o *mouseLinkOpener) OpenURL(_ context.Context, url string) error {
	o.urls = append(o.urls, url)
	return o.err
}

func TestClickOnAContainerCollapsesItsSessions(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	row := m.rows[1]
	session := control.Session{Name: "cspace-claude-2", ID: "generation-2"}
	m.sessions[keyOf(row)] = []control.Session{session}
	m.geom = m.computeGeometry()
	id := containerNavID(row)
	before := len(m.navigation())
	m = click(t, m, 4, navigationLineOf(t, m, id))
	if !m.collapsed[id] || len(m.navigation()) >= before || m.navID != id {
		t.Fatalf("container click did not collapse and select its group: %+v", m.navigation())
	}
	for _, item := range m.navigation() {
		if item.id == sessionNavID(row, session) {
			t.Fatal("collapsed session is still visible")
		}
	}
	m = click(t, m, 4, navigationLineOf(t, m, id))
	if m.collapsed[id] || len(m.navigation()) != before {
		t.Fatal("second click did not expand the container")
	}
}

func TestClickOnAProjectCollapsesItsContainers(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.focus = focusMain
	before := len(m.navigation())
	m = click(t, m, 4, navigationLineOf(t, m, "project:alpha"))
	if !m.collapsed["project:alpha"] || len(m.navigation()) != 1 || m.focus != focusSidebar {
		t.Fatalf("project click did not collapse its children: %+v", m.navigation())
	}
	m = click(t, m, 4, navigationLineOf(t, m, "project:alpha"))
	if m.collapsed["project:alpha"] || len(m.navigation()) != before {
		t.Fatal("second project click did not restore its children")
	}
}

func TestClickOnASessionAttachesAndThenFocusesExistingPane(t *testing.T) {
	h := &mouseSessionHost{fakeHost: &fakeHost{t: t}}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	row := m.rows[1]
	session := control.Session{Name: "cspace-claude-2", ID: "generation-2"}
	m.sessions[keyOf(row)] = []control.Session{session}
	m.geom = m.computeGeometry()
	id := sessionNavID(row, session)
	m, cmd := clickCmd(t, m, 6, navigationLineOf(t, m, id))
	m = pump(t, m, cmd)
	mustTabs(t, m, 1)
	if len(h.requests) != 1 || h.requests[0].Session != session.Name || h.requests[0].ExpectedID != session.ID || h.requests[0].New {
		t.Fatalf("clicked session requested the wrong attach: %+v", h.requests)
	}
	if m.focus != focusMain || m.focusedTab().session.ID != session.ID {
		t.Fatalf("session pane did not receive focus: %+v", m.focusedTab())
	}
	m.focus = focusSidebar
	m, cmd = clickCmd(t, m, 6, navigationLineOf(t, m, id))
	if cmd != nil || len(h.requests) != 1 || m.focus != focusMain {
		t.Fatal("clicking an attached session must focus it without another attach")
	}
}

func TestClickOnASidebarPaneFocusesIt(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	mm, cmd := m.openOrFocus(KindClaude, m.selectedRow())
	m = pump(t, mm.(Model), cmd)
	mm, cmd = m.openOrFocus(KindShell, m.selectedRow())
	m = pump(t, mm.(Model), cmd)
	mustTabs(t, m, 2)
	m.focus = focusSidebar
	m.geom = m.computeGeometry()
	for _, want := range []int{0, 1} {
		id := paneNavID(m.tabs[want].id)
		m, cmd = clickCmd(t, m, 6, navigationLineOf(t, m, id))
		if cmd != nil || m.focused != want || m.focus != focusMain {
			t.Fatalf("clicking pane %s focused %d with command=%v", id, m.focused, cmd != nil)
		}
	}
}

func TestOrdinaryHeaderClickOpensOnlyItsURL(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	k := keyOf(m.selectedRow())
	m.headers[k] = headerSample{status: control.HeaderStatus{Branch: "feature", PR: &control.PullRequestStatus{Number: 42, URL: "https://github.com/owner/repo/pull/42"}}}
	m.services[k] = []control.ServiceStatus{{Port: 3000, Label: "dev", URL: "http://mercury.alpha.cspace.test:3000/", State: control.ServiceRunning}}
	m.geom = m.computeGeometry()
	links := m.geom.headerLinks
	opened := 0
	for _, link := range links {
		if link.details {
			continue
		}
		for _, edge := range []int{0, link.width - 1} {
			opener := &mouseLinkOpener{}
			base := m.WithLinkOpener(opener)
			got, cmd := clickCmd(t, base, base.geom.header.x+link.x+edge, base.geom.header.y+link.y)
			if len(opener.urls) != 0 || cmd == nil {
				t.Fatal("header URL opening must be deferred to a command")
			}
			got = pump(t, got, cmd)
			if len(opener.urls) != 1 || opener.urls[0] != link.url || got.focus != m.focus || got.selected != m.selected {
				t.Fatalf("click opened %v, want only %q without changing selection", opener.urls, link.url)
			}
			_, outside := clickCmd(t, got, got.geom.header.x+link.x+link.width, got.geom.header.y+link.y)
			if outside != nil {
				t.Fatal("link claimed the column outside its right edge")
			}
		}
		opened++
	}
	if opened != 2 {
		t.Fatalf("tested %d web links, want PR and dev", opened)
	}
}

func TestEnvironmentClickOpensDetailsForTheActiveProject(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury"}}
	m.focused, m.focus = 0, focusMain
	m.geom = m.computeGeometry()
	got := click(t, m, 4, m.geom.environment.y+1)
	if got.mode != modeDetails || got.dialog == nil || !got.dialog.environment || got.dialog.project != "alpha" {
		t.Fatalf("environment click opened the wrong dialog: %+v", got.dialog)
	}
	if got.focus != focusMain || got.focused != 0 {
		t.Fatal("opening environment details changed the active pane")
	}
}

func TestDetailsDialogClickRetainsTheNamedContainerAcrossSnapshotChanges(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury"}}
	m.focused, m.focus = 0, focusMain
	m.selected = 3 // Browsing issue-42 must not retarget the pane's Details link.
	m.geom = m.computeGeometry()
	var details headerLink
	for _, link := range m.geom.headerLinks {
		if link.details {
			details = link
		}
	}
	if !details.details {
		t.Fatal("header has no Details target")
	}
	m = click(t, m, m.geom.header.x+details.x, m.geom.header.y+details.y)
	if m.dialog == nil || m.dialog.row.Name != "mercury" {
		t.Fatalf("Details opened for sidebar selection instead of active pane: %+v", m.dialog)
	}
	// A reorder changes all numeric row positions while the dialog is open.
	snap := testSnapshot()
	snap.Rows[1], snap.Rows[3] = snap.Rows[3], snap.Rows[1]
	mm, _ := m.Update(snapshotMsg{snap: snap})
	m = mm.(Model)
	frame, width, height := m.modalFrame()
	lines := m.detailsLines(width)
	actionLine := -1
	for i, line := range lines {
		if line.action != nil && line.action.id == "down" {
			actionLine = i
			break
		}
	}
	if actionLine < 0 {
		t.Fatal("container details has no teardown action")
	}
	m.modalScroll = max(0, actionLine-height+1)
	m, _ = clickCmd(t, m, frame.x+3, frame.y+3+actionLine-m.modalScroll)
	if m.mode != modeConfirmDown || m.pending.Name != "mercury" || m.pending.Project != "alpha" {
		t.Fatalf("dialog action drifted to another row: mode=%v pending=%+v", m.mode, m.pending)
	}
	if len(a.down) != 0 {
		t.Fatal("click bypassed the separate teardown confirmation")
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

func TestClickUnderHelpIsSwallowedUntilEscape(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "?")
	if !m.showHelp {
		t.Fatal("? did not open the overlay")
	}
	before := m.selected
	other := listLineOf(t, m, 3)
	got := click(t, m, 4, other)
	if !got.showHelp {
		t.Error("only Escape should dismiss the help overlay")
	}
	if got.selected != before {
		t.Error("a click under help selected a row")
	}
	if dismissed := step(t, got, "esc"); dismissed.showHelp {
		t.Error("Escape did not dismiss help")
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

func TestClickOnSidebarPaddingAndRuleOnlyMovesFocus(t *testing.T) {
	base := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	g := base.geom
	padding := len(g.navItems)
	if padding >= g.list.h {
		t.Fatal("fixture leaves no empty navigation line")
	}
	for _, point := range []struct{ x, y int }{
		{g.sidebar.w - 1, 1},
		{4, g.list.y + padding},
	} {
		m := base
		m.focus = focusMain
		got, cmd := clickCmd(t, m, point.x, point.y)
		if got.selected != m.selected || got.navID != m.navID || cmd != nil {
			t.Fatalf("empty sidebar cell (%d,%d) acted", point.x, point.y)
		}
		// Padding in the list is inert; the rule explicitly gives focus to
		// the sidebar. Neither can activate a session or open a dialog.
		if point.x == g.sidebar.w-1 && got.focus != focusSidebar {
			t.Fatal("the sidebar rule did not move keyboard focus")
		}
		if got.mode != modeNormal {
			t.Fatal("empty sidebar cell opened a dialog")
		}
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

func TestWheelOverTheSidebarMovesNavigationWithoutChangingPaneFocus(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m.tabs = []*tab{{id: 1, kind: KindClaude, project: "alpha", sandbox: "mercury"}}
	m.focused, m.focus = 0, focusMain
	m.geom = m.computeGeometry()
	items := m.navigation()
	m.selectNavigation(items[0])
	m.geom = m.computeGeometry()
	down := wheel(t, m, 4, 3, false)
	if down.navID != items[1].id || down.focus != focusMain || down.focused != 0 {
		t.Fatalf("wheel navigation changed the pane or picked wrong row: id=%q focus=%v pane=%d", down.navID, down.focus, down.focused)
	}
	up := wheel(t, down, 4, 3, true)
	if up.navID != items[0].id || up.focus != focusMain || up.focused != 0 {
		t.Fatalf("wheel back did not retain pane focus: id=%q focus=%v pane=%d", up.navID, up.focus, up.focused)
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

func TestWheelOverTheSidebarAtTheEndsFiresNoEventsRead(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	items := m.navigation()
	for _, tc := range []struct {
		item   navigationItem
		button tea.MouseButton
	}{{items[0], tea.MouseWheelUp}, {items[len(items)-1], tea.MouseWheelDown}} {
		m.selectNavigation(tc.item)
		m.geom = m.computeGeometry()
		got, cmd := m.Update(tea.MouseWheelMsg{X: 4, Y: 3, Button: tc.button})
		if cmd != nil || got.(Model).navID != tc.item.id {
			t.Fatalf("wheel past %q moved navigation or issued an events read", tc.item.id)
		}
	}
}
