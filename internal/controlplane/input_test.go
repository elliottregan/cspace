package controlplane

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// press builds the KeyPressMsg a terminal would deliver. bubbletea v2 keys
// are structs: printable keys carry Text, named keys carry a Code.
func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// step delivers a key and returns the new model, discarding the command.
func step(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, _ := m.Update(press(k))
	return mm.(Model)
}

// answer delivers a key to an open teardown confirmation and pumps the
// commands it produces back into the model until the confirmation closes.
//
// huh answers a Confirm over two asynchronous round trips, not one: the
// field sets the value and returns huh.NextField (a *command*), the group
// turns that message into nextGroup (another command), and only when the
// form receives nextGroupMsg does it report StateCompleted. A single Update
// therefore leaves the form open, which is why step() is not enough here.
// Production does this for free — Task 6's fall-through routes the
// unconsumed messages straight back into updateConfirm — so this helper is
// the test-side equivalent of that loop and nothing more.
//
// It stops as soon as the mode leaves modeConfirmDown, so the action's own
// command (which carries the spinner tick) is never run here.
func answer(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, cmd := m.Update(press(k))
	m = mm.(Model)
	for i := 0; i < 8 && cmd != nil && m.mode == modeConfirmDown; i++ {
		var next tea.Cmd
		for _, msg := range drain(cmd) {
			if msg == nil {
				continue
			}
			mm, c := m.Update(msg)
			m = mm.(Model)
			if c != nil {
				next = c
			}
			if m.mode != modeConfirmDown {
				break
			}
		}
		cmd = next
	}
	if m.mode == modeConfirmDown {
		t.Fatalf("the confirmation never settled after %q", k)
	}
	return m
}

func TestMoveKeysChangeTheSelection(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "j")
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("after j, selection = %q, want issue-42", got)
	}
	m = step(t, m, "k")
	if got := m.selectedRow().Name; got != "mercury" {
		t.Errorf("after k, selection = %q, want mercury", got)
	}
	m = step(t, m, "down")
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Errorf("after ↓, selection = %q, want issue-42", got)
	}
}

func TestInterruptDispatch(t *testing.T) {
	a := &recordingActor{}
	d := &fakeData{snap: testSnapshot()}
	m := newTestModel(d, a)
	// mercury's snapshot agent is idle, so interrupt is gated off; the fast
	// ticker reporting it working is what enables the key.
	mm, _ := m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Agent: control.AgentStatus{Reachable: true, State: "working"}},
	}})
	m = mm.(Model)

	got := step(t, m, "i")
	if len(a.interrupt) != 1 {
		t.Errorf("interrupt calls = %d, want 1 (model %v)", len(a.interrupt), got.action)
	}
	if got.action != LabelInterrupt {
		t.Errorf("action = %q, want %q in flight", got.action, LabelInterrupt)
	}
}

func TestInterruptIsGatedOnAWorkingAgent(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a) // mercury is idle
	step(t, m, "i")
	if len(a.interrupt) != 0 {
		t.Errorf("interrupt fired on an idle agent: %+v", a.interrupt)
	}
}

// Spec, Error handling: an unreachable supervisor disables send and
// interrupt on that row rather than failing on press.
func TestSendAndInterruptAreOffForADegradedSandbox(t *testing.T) {
	snap := testSnapshot()
	snap.Rows[1].State = control.StateDegraded
	snap.Rows[1].Agent = control.AgentStatus{}
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: snap}, a)

	m = step(t, m, "m")
	if m.mode == modeInput {
		t.Error("the send box opened for an unreachable supervisor")
	}
	step(t, m, "i")
	if len(a.interrupt) != 0 {
		t.Errorf("interrupt fired for an unreachable supervisor: %+v", a.interrupt)
	}
	// The footer must not advertise them either.
	if out := plain(m.View().Content); strings.Contains(out, "send a turn") {
		t.Errorf("footer offered send for a degraded sandbox:\n%s", out)
	}
}

func TestBootOnlyOffersAStoppedSandbox(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	step(t, m, "u") // mercury is running
	if len(a.up) != 0 {
		t.Errorf("boot fired on a running sandbox: %+v", a.up)
	}
	m = step(t, m, "j") // issue-42 is stopped
	step(t, m, "u")
	if len(a.up) != 1 || a.up[0].Name != "issue-42" {
		t.Errorf("boot calls = %+v, want one for issue-42", a.up)
	}
}

func TestBrowserRestartDispatches(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	step(t, m, "b")
	if len(a.browser) != 1 || a.browser[0].Project != "alpha" {
		t.Errorf("browser restart calls = %+v, want one for alpha", a.browser)
	}
}

func TestOneActionAtATime(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	m = step(t, m, "enter") // attach in flight
	m = step(t, m, "d")     // must not open the confirmation
	if m.mode == modeConfirmDown {
		t.Error("a second action started while one was in flight")
	}
	mm, _ := m.Update(actionResultMsg{label: "attach"})
	m = mm.(Model)
	if m.action != "" {
		t.Errorf("action = %q after its result landed, want cleared", m.action)
	}
	m = step(t, m, "d")
	if m.mode != modeConfirmDown {
		t.Error("the confirmation should open once the gate cleared")
	}
}

func TestSendBox(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "m")
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want modeInput", m.mode)
	}
	if len(a.sends) != 0 {
		t.Fatal("Send called before the text was entered")
	}
	if !strings.Contains(plain(m.View().Content), "send to mercury") {
		t.Errorf("the footer should show the send box:\n%s", plain(m.View().Content))
	}

	// Ordinary keys type rather than dispatch: "q" must not quit here.
	m = step(t, m, "q")
	if m.quitting {
		t.Fatal("q quit the dashboard from inside the send box")
	}

	m.input.SetValue("do the thing")
	m = step(t, m, "enter")
	if len(a.sends) != 1 || a.sends[0].text != "do the thing" || a.sends[0].row.Name != "mercury" {
		t.Errorf("send calls = %+v, want one for mercury", a.sends)
	}
	if m.mode != modeNormal || m.action != "send" {
		t.Errorf("mode = %v, action = %q after sending", m.mode, m.action)
	}
}

func TestSendBoxEmptyAndCancel(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "m")
	m = step(t, m, "enter") // empty
	if len(a.sends) != 0 || m.action != "" {
		t.Errorf("an empty send should do nothing: sends=%+v action=%q", a.sends, m.action)
	}

	// Whitespace-only is empty too, once trimmed: nothing worth sending.
	m = step(t, m, "m")
	m.input.SetValue("   ")
	m = step(t, m, "enter")
	if len(a.sends) != 0 || m.mode != modeNormal || m.action != "" {
		t.Errorf("a whitespace-only send should do nothing: sends=%+v mode=%v action=%q",
			a.sends, m.mode, m.action)
	}

	m = step(t, m, "m")
	m.input.SetValue("discarded")
	m = step(t, m, "esc")
	if len(a.sends) != 0 || m.mode != modeNormal {
		t.Errorf("esc should cancel the send box: sends=%+v mode=%v", a.sends, m.mode)
	}
}

// The send box's footer label has to name the same sandbox Send will
// actually act on. Like TestTeardownActsOnTheRowThePromptNamed, a poll can
// land and move the selection while the box is open (applySnapshot runs
// unconditionally in the snapshotMsg case; only the *tick* handlers that
// start a new poll respect paused()) — the footer must keep naming mercury,
// not whatever ends up selected.
func TestSendBoxFooterNamesThePendingRow(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})

	m = step(t, m, "m") // opens the send box on mercury, the initial selection
	if m.mode != modeInput || m.pending.Name != "mercury" {
		t.Fatalf("test setup: mode = %v, pending = %+v, want the send box open on mercury",
			m.mode, m.pending)
	}

	// Drop the mercury-convex sidecar along with mercury itself: left in
	// place it would still read "mercury" in the sidebar (sidecar names are
	// trimmed by their *preceding* sandbox row, venus here, which is not
	// mercury's prefix), which would pass this assertion for the wrong
	// reason. Checking the footer directly rather than the whole view sidesteps that too.
	moved := testSnapshot()
	moved.Rows = append([]control.Row{moved.Rows[0]}, moved.Rows[2:]...)
	moved.Rows[1] = control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "venus",
		Container: "cspace-alpha-venus", State: control.StateRunning, Selectable: true}
	mm, _ := m.Update(snapshotMsg{snap: moved})
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "venus" {
		t.Fatalf("test setup: selection = %q after the snapshot, want venus", got)
	}

	footer := plain(m.footer())
	if !strings.Contains(footer, "mercury") {
		t.Errorf("the footer should still name mercury, the row the send box was opened against: %q", footer)
	}
	if strings.Contains(footer, "venus") {
		t.Errorf("the footer should not have followed the selection to venus: %q", footer)
	}
}

func TestTeardownConfirms(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "d")
	if m.mode != modeConfirmDown || m.confirm == nil {
		t.Fatalf("mode = %v, confirm = %v, want the confirmation open", m.mode, m.confirm)
	}
	if len(a.down) != 0 {
		t.Fatal("Down called before the confirmation was answered")
	}
	if out := plain(m.View().Content); !strings.Contains(out, "Tear down mercury") {
		t.Errorf("the confirmation should name the sandbox:\n%s", out)
	}
	if out := plain(m.View().Content); !strings.Contains(out, "Keep it") {
		t.Errorf("confirm buttons missing from view:\n%s", out)
	}

	m = answer(t, m, "y")
	if len(a.down) != 1 || a.down[0].Name != "mercury" {
		t.Errorf("down calls = %+v, want one for mercury", a.down)
	}
	if m.mode != modeNormal || m.confirm != nil {
		t.Errorf("the confirmation should close after answering: mode=%v", m.mode)
	}
}

func TestTeardownCancels(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)
	m = step(t, m, "d")
	m = step(t, m, "esc")
	if len(a.down) != 0 {
		t.Errorf("esc should cancel the teardown: %+v", a.down)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after cancelling, want modeNormal", m.mode)
	}

	// Answering "no" is a cancel too: huh's Reject binding sets the value
	// and advances the form, which completes it with false — over the same
	// two command round trips "y" takes, hence answer() rather than step().
	m = step(t, m, "d")
	m = answer(t, m, "n")
	if len(a.down) != 0 {
		t.Errorf("answering no should not tear down: %+v", a.down)
	}
	if m.mode != modeNormal || m.confirm != nil {
		t.Errorf("answering no should close the confirmation: mode=%v", m.mode)
	}
}

// A poll can land and move the selection while the teardown confirmation is
// still open (restoreSelection runs unconditionally in applySnapshot; only
// the *tick* handlers that start a new poll respect paused()). The
// confirmation must act on the sandbox it was opened against, not on
// whatever ends up selected by the time it is answered.
func TestTeardownActsOnTheRowThePromptNamed(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "d") // opens the confirm on mercury, the initial selection
	if m.mode != modeConfirmDown || m.pending.Name != "mercury" {
		t.Fatalf("test setup: mode = %v, pending = %+v, want the confirm open on mercury",
			m.mode, m.pending)
	}

	// A snapshot lands while the prompt is open. mercury's old slot (index 1)
	// is now a different sandbox, venus, and restoreSelection lands there
	// since mercury's identity is gone from the row set.
	moved := testSnapshot()
	moved.Rows[1] = control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "venus",
		Container: "cspace-alpha-venus", State: control.StateRunning, Selectable: true}
	mm, _ := m.Update(snapshotMsg{snap: moved})
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "venus" {
		t.Fatalf("test setup: selection = %q after the snapshot, want venus", got)
	}

	m = answer(t, m, "y")
	if len(a.down) != 1 || a.down[0].Name != "mercury" {
		t.Errorf("down calls = %+v, want one for mercury (the row the prompt named), not the row later selected", a.down)
	}
}

func TestHelpOverlayToggles(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "?")
	out := plain(m.View().Content)
	if !strings.Contains(out, "restart browser") || !strings.Contains(out, "tear down") {
		t.Errorf("the help overlay should list every binding:\n%s", out)
	}
	if !strings.Contains(out, "~/.cspace/config.json") {
		t.Errorf("the help overlay should say where bindings come from:\n%s", out)
	}
	if !strings.Contains(out, "shell pane") {
		t.Errorf("the help overlay should list the pane bindings too:\n%s", out)
	}
	if !strings.Contains(out, "mercury") {
		t.Error("the sidebar stays visible behind the help overlay")
	}
	// m.help is sized to the whole 100-column window; the overlay renders
	// into the narrower main area, so every line must still fit inside the
	// window it is actually drawn in.
	for i, l := range strings.Split(out, "\n") {
		if w := len([]rune(l)); w > 100 {
			t.Errorf("help overlay line %d is wider than the window (%d): %q", i, w, l)
		}
	}
	m = step(t, m, "?")
	if strings.Contains(plain(m.View().Content), "~/.cspace/config.json") {
		t.Error("the help overlay should toggle off")
	}
}

// The overlay must close on any key, not just `?` — otherwise a key that
// also dispatches an action (d, for the teardown confirm) would both close
// help and fire that action against a main area the overlay had been
// covering, with no confirmation ever drawn.
func TestAnyKeyClosesTheHelpOverlayWithoutDispatching(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(&fakeData{snap: testSnapshot()}, a)

	m = step(t, m, "?")
	if !m.showHelp {
		t.Fatal("test setup: ? should open the help overlay")
	}

	m = step(t, m, "d")
	if m.showHelp {
		t.Error("d should have closed the help overlay")
	}
	if m.mode != modeNormal || m.confirm != nil {
		t.Errorf("d should not have been dispatched: mode=%v confirm=%v", m.mode, m.confirm)
	}
	if len(a.down) != 0 {
		t.Errorf("d should not have been dispatched: down calls = %+v", a.down)
	}

	m = step(t, m, "?")
	if !m.showHelp {
		t.Error("? should still open the help overlay from normal mode")
	}
}

func TestQuitKeyAndErrorNoticeDismissal(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, _ := m.Update(actionResultMsg{label: "down", err: errors.New("boom")})
	m = mm.(Model)

	m = step(t, m, "j") // any key dismisses an error notice
	if m.notice.text != "" {
		t.Errorf("error notice = %q, want it dismissed on the next keypress", m.notice.text)
	}

	_, cmd := m.Update(press("q"))
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q produced %T, want tea.QuitMsg", cmd())
	}
}

// The Actor contract (actor.go) says a nil Cmd means "nothing to do" and
// must not be returned for an action the caller marked in flight.
// startAction enforces the model's side of that: given one anyway, it must
// not mark an action in flight that will never get an actionResultMsg to
// clear it — that would spin the footer's spinner forever.
func TestStartActionIgnoresANilCommand(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, cmd := m.startAction("attach", nil)
	m = mm.(Model)
	if m.action != "" {
		t.Errorf("action = %q, want it left unset for a nil command", m.action)
	}
	if cmd != nil {
		t.Errorf("startAction with a nil command returned %v, want nil", cmd)
	}
}

// mainWidthFor is what both View and the teardown confirmation's width
// computation go through, so they can never disagree; this locks its floor
// at a window too narrow for the sidebar to leave 20 columns on its own.
//
// The confirmation's own width isn't asserted here beyond this: huh.Form (and
// its Group/Confirm field) keep their width unexported with no getter, so
// what the form actually received cannot be read back without reaching into
// huh's internals — which would couple this test to huh's layout rather than
// to cspace's own arithmetic. mainWidthFor(30)-2 == 18 is exactly the "width
// handed to the form at a 30-column window" the review asked to assert; this
// is that assertion at the boundary where it is actually observable.
func TestMainWidthForFloorsANarrowWindow(t *testing.T) {
	if got := mainWidthFor(30); got != 20 {
		t.Errorf("mainWidthFor(30) = %d, want the 20-column floor", got)
	}
	if got := mainWidthFor(30) - 2; got != 18 {
		t.Errorf("the width the teardown confirm would be built with at 30 columns = %d, want 18", got)
	}
	if got := mainWidthFor(100); got != 76 {
		t.Errorf("mainWidthFor(100) = %d, want 76", got)
	}
}

// TestSendBoxShowsItsWholePlaceholder — bubbles/v2's placeholderView builds a
// rune slice of Width()+1, so an unsized textinput renders exactly one
// character of its placeholder. "message" showed as "m", which on a box opened
// with the "m" key reads as the keystroke having leaked into the input.
func TestSendBoxShowsItsWholePlaceholder(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	m = step(t, m, "m")
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want modeInput", m.mode)
	}
	if got := m.input.Width(); got <= 0 {
		t.Fatalf("input width = %d, want the box sized to the footer", got)
	}
	footer := plain(m.footer())
	if !strings.Contains(footer, "message") {
		t.Errorf("send box should show its whole placeholder; got %q", footer)
	}
}

// The box must fit the window: a long sandbox name cannot push the input off
// the footer line.
func TestSendInputWidthLeavesRoomForTheLabel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window int
	}{
		{"mercury", 120},
		{"a-very-long-descriptive-sandbox-name", 100},
		{"mercury", 10}, // narrower than the label itself
	} {
		w := sendInputWidth(tc.name, tc.window)
		if w < 8 {
			t.Errorf("sendInputWidth(%q, %d) = %d, want at least the 8-column floor", tc.name, tc.window, w)
		}
		if tc.window > 60 && w+lipgloss.Width(sendBoxPrefix(tc.name))+3 > tc.window {
			t.Errorf("sendInputWidth(%q, %d) = %d overflows the line", tc.name, tc.window, w)
		}
	}
}

// The whole send box has to fit the footer line: fit() would otherwise
// truncate the input the person is typing into and show an ellipsis.
func TestSendBoxFitsTheFooterLine(t *testing.T) {
	m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(Model)
	m = step(t, m, "m")
	if got := lipgloss.Width(plain(m.footer())); got > 120 {
		t.Errorf("send box footer is %d columns wide, want at most 120:\n%q", got, plain(m.footer()))
	}
	m.input.SetValue(strings.Repeat("x", 300))
	if got := lipgloss.Width(plain(m.footer())); got > 120 {
		t.Errorf("send box footer with a long turn is %d columns wide, want at most 120", got)
	}
}

// The label is plain text and the input's view is already styled ANSI, so a
// narrow window shortens the label — it never fits the composed line. fit()
// counts display cells while cutting runes, so fitting a styled string can
// cut an escape sequence in half or drop the closing reset and bleed the
// style into whatever the terminal draws next.
//
// 30 columns with an ordinary sandbox name is where sendInputWidth's
// eight-column floor starts to bite; 16 is past it, where the label gives all
// it can and the line is allowed to overflow rather than cut the input.
func TestSendBoxShortensItsLabelRatherThanTheStyledInput(t *testing.T) {
	for _, window := range []int{80, 30, 16} {
		m := newTestModel(&fakeData{snap: testSnapshot()}, &recordingActor{})
		mm, _ := m.Update(tea.WindowSizeMsg{Width: window, Height: 24})
		m = mm.(Model)
		m = step(t, m, "m")
		if m.mode != modeInput {
			t.Fatalf("window %d: the send box did not open", window)
		}

		footer := m.footer()
		if !strings.HasSuffix(footer, m.input.View()) {
			t.Errorf("window %d: the styled input was not rendered verbatim:\n%q", window, footer)
		}
		if visible := plain(footer); strings.ContainsRune(visible, '\x1b') {
			t.Errorf("window %d: a cut escape sequence survived stripping: %q", window, visible)
		}
		if window >= 30 {
			if got := lipgloss.Width(plain(footer)); got > window {
				t.Errorf("window %d: footer is %d columns wide: %q", window, got, plain(footer))
			}
		}
		if !strings.HasPrefix(plain(footer), "send") {
			t.Errorf("window %d: the label should still read as one: %q", window, plain(footer))
		}
	}
}
