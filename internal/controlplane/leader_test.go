package controlplane

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/pane"
)

// leader arms the leader and delivers its second key.
func leader(t *testing.T, m Model, second string) Model {
	t.Helper()
	m = step(t, m, "ctrl+space")
	if !m.leaderArmed {
		t.Fatal("ctrl+space did not arm the leader")
	}
	return step(t, m, second)
}

// openOne opens a Claude pane on the selected row and returns the settled
// model.
func openOne(t *testing.T, h *fakeHost) Model {
	t.Helper()
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	return m
}

func TestPaneKeyConversionMatchesBubbleteasModifiers(t *testing.T) {
	// The cast in paneKey is only safe if the two bitmasks agree, which is
	// what this locks. A drift here would silently send the wrong modifier.
	if pane.KeyMod(tea.ModCtrl) != pane.ModCtrl ||
		pane.KeyMod(tea.ModShift) != pane.ModShift ||
		pane.KeyMod(tea.ModAlt) != pane.ModAlt ||
		pane.KeyMod(tea.ModMeta) != pane.ModMeta {
		t.Fatal("pane.KeyMod and tea.KeyMod have drifted apart")
	}
	got := paneKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl, Text: "c"})
	if got.Code != 'c' || got.Mod != pane.ModCtrl || got.Text != "c" {
		t.Errorf("paneKey = %+v", got)
	}
}

func TestKeysReachTheFocusedPaneAndTheLeaderDoesNot(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)

	// `q` quits from the sidebar. With a pane focused it is a letter.
	m2 := step(t, m, "q")
	if m2.quitting {
		t.Error("q quit the program while a pane had focus")
	}
	// `d` likewise: no teardown confirmation may open behind a focused pane.
	if got := step(t, m, "d"); got.mode != modeNormal {
		t.Errorf("d opened %v while a pane had focus", got.mode)
	}

	// The leader gets through.
	if !step(t, m, "ctrl+space").leaderArmed {
		t.Error("the leader did not arm")
	}
}

func TestLeaderDispatch(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)

	if got := leader(t, m, "h"); got.focus != focusSidebar {
		t.Error("leader h did not focus the sidebar")
	}
	if got := leader(t, m, "?"); !got.showHelp {
		t.Error("leader ? did not open the help overlay")
	}
	if got := leader(t, m, "["); !got.scrolling {
		t.Error("leader [ did not enter scroll mode")
	}
	if got := leader(t, m, "t"); got.mode != modePicker || got.picker == nil {
		t.Error("leader t did not open the new-pane picker")
	}
	// v is bound so the config shape is stable, and deliberately does
	// nothing until rollout step 5.
	if got := leader(t, m, "v"); got.mode != modeNormal || got.notice.text != "" {
		t.Error("leader v did something; image paste is step 5")
	}
}

// settlePicker delivers a key to the open new-pane picker and pumps the
// commands it produces back in until the picker leaves modePicker, then
// hands back the command the settled model produced.
//
// It is the test-side equivalent of model.go's modePicker fall-through,
// exactly as answer() is for the teardown confirmation, and it exists for
// the same reason: huh resolves a Select over two asynchronous round trips
// — the field returns huh.NextField as a *command*, the group turns that
// into nextGroup, and only nextGroupMsg makes the form report
// StateCompleted. A single Update leaves the picker open.
func settlePicker(t *testing.T, m Model, k string) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(press(k))
	m = mm.(Model)
	for i := 0; i < 8 && cmd != nil && m.mode == modePicker; i++ {
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
			if m.mode != modePicker {
				break
			}
		}
		cmd = next
	}
	if m.mode == modePicker {
		t.Fatalf("the picker never settled after %q", k)
	}
	return m, cmd
}

// TestThePickerOpensThePaneItPicks is the round trip the fall-through in
// model.go exists for: without it the picker can be opened and never
// answered, and no unit test that only asserts `mode == modePicker` would
// notice.
func TestThePickerOpensThePaneItPicks(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h) // one Claude pane on mercury, focus in the main area
	m = leader(t, m, "t")
	if m.mode != modePicker || m.picker == nil {
		t.Fatal("leader t did not open the new-pane picker")
	}

	// "Shell in the sandbox" is the second option.
	m = step(t, m, "down")
	m, cmd := settlePicker(t, m, "enter")
	if m.picker != nil {
		t.Error("the picker survived its own answer")
	}
	m = pump(t, m, cmd)

	mustTabs(t, m, 2)
	if len(h.opens) != 2 || h.opens[1] != KindShell {
		t.Fatalf("opens = %v, want the picker's shell as the second", h.opens)
	}
}

// TestThePickerRefusesAStoppedSandbox is the gate the picker needs and the
// sidebar keys get for free from forRow. A stopped sandbox keeps its row
// and its container name (4a Task 6), so an ungated open would reach the
// tmux probe and fail with a transport-shaped error a person cannot act on.
func TestThePickerRefusesAStoppedSandbox(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = step(t, m, "j") // issue-42, which testSnapshot has as StateStopped
	if got := m.selectedRow().Name; got != "issue-42" {
		t.Fatalf("selection = %q, want issue-42", got)
	}
	m = leader(t, m, "t")
	m, cmd := settlePicker(t, m, "enter") // "Claude session", the default
	m = pump(t, m, cmd)

	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none: the pick was refused", len(m.tabs))
	}
	if len(h.opens) != 0 {
		t.Errorf("opens = %v, want none on a stopped sandbox", h.opens)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "issue-42") {
		t.Errorf("notice = %+v, want an error naming the sandbox", m.notice)
	}
}

// TestTwoHostShellsAreTwoTabs is the exemption openOrFocus makes for the one
// pane kind that belongs to no sandbox. The existing-tab match is
// (kind, project, sandbox), and a host shell has neither of the last two —
// so without the exemption the second one opened from the same row would
// silently refocus the first, and one opened from a different row would sit
// beside it wearing that row's identity under an identical title.
func TestTwoHostShellsAreTwoTabs(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	m = openHostShell(t, m)
	m.focus = focusSidebar
	m = step(t, m, "j") // a different row: issue-42
	m = openHostShell(t, m)

	mustTabs(t, m, 2)
	for i, tb := range m.tabs {
		if tb.kind != KindHostShell {
			t.Fatalf("tab %d is a %v, want a host shell", i, tb.kind)
		}
		if tb.project != "" || tb.sandbox != "" {
			t.Errorf("tab %d carries %q/%q; a host shell belongs to no sandbox",
				i, tb.project, tb.sandbox)
		}
		if got := tb.title(); got != "host · shell" {
			t.Errorf("tab %d title = %q", i, got)
		}
	}
}

// openHostShell drives leader `t` → "Host shell", the fourth option, which
// is the only way to open one: it has no sidebar key, because there is no
// row to press one on.
func openHostShell(t *testing.T, m Model) Model {
	t.Helper()
	m = leader(t, m, "t")
	if m.mode != modePicker || m.picker == nil {
		t.Fatal("leader t did not open the new-pane picker")
	}
	for i := 0; i < 3; i++ {
		m = step(t, m, "down") // Claude, Shell, Supervisor, Host shell
	}
	m, cmd := settlePicker(t, m, "enter")
	return pump(t, m, cmd)
}

func TestLeaderTwiceSendsTheLeaderToTheChild(t *testing.T) {
	// An echoing child, because the interesting claim is that the byte
	// arrived. Asserting Dropped() did not change cannot fail for the
	// reason it names: a 512-slot queue being drained drops nothing whether
	// or not anything was ever queued.
	h := &fakeHost{t: t, echo: true}
	m := openOne(t, h)
	m = leader(t, m, "ctrl+space")
	if m.leaderArmed {
		t.Error("the leader stayed armed after being passed through")
	}
	// Ctrl+Space is NUL, which `cat -v` prints as ^@.
	waitForPaneScreen(t, m.tabs[0], "^@")
}

// waitForPaneScreen polls a pane's rendered screen until want appears. The
// child is a real process on a real pty, so the round trip — key, queue,
// pty, child, pty, emulator — is asynchronous with the Update that started
// it.
func waitForPaneScreen(t *testing.T, tb *tab, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(plain(tb.p.Render()), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the pane never showed %q:\n%s", want, plain(tb.p.Render()))
}

func TestTabNavigationWraps(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	m.focus = focusSidebar
	m = stepPump(t, m, "s")
	mustTabs(t, m, 2)

	if got := leader(t, m, "n"); got.focused != 0 {
		t.Errorf("next tab from the last = %d, want 0 (wrapped)", got.focused)
	}
	m.focused = 0
	if got := leader(t, m, "p"); got.focused != 1 {
		t.Errorf("previous tab from the first = %d, want 1 (wrapped)", got.focused)
	}
}

func TestScrollModeMovesAndAnyOtherKeyReturnsToLive(t *testing.T) {
	// The offset is clamped to the history that exists, so this pane has to
	// have some: a pane whose child printed nothing cannot scroll, and
	// asserting that it does would be asserting a bug.
	h := &fakeHost{t: t, history: true}
	m := openOne(t, h)
	waitForHistory(t, m.tabs[0], 10)
	m = leader(t, m, "[")

	m = step(t, m, "up")
	if m.scroll != 1 {
		t.Errorf("scroll after up = %d, want 1", m.scroll)
	}
	m = step(t, m, "down")
	if m.scroll != 0 {
		t.Errorf("scroll after down = %d, want 0", m.scroll)
	}

	m = step(t, m, "x") // any other key
	if m.scrolling {
		t.Error("an ordinary key did not leave scroll mode")
	}
	// ...and it did not reach the child either: leaving scroll mode is what
	// that key did.
	if m.scroll != 0 {
		t.Errorf("scroll = %d after leaving", m.scroll)
	}
}

// waitForHistory polls until a pane's scrollback holds at least n lines.
func waitForHistory(t *testing.T, tb *tab, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tb.p.ScrollbackLen() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scrollback holds %d lines, want %d", tb.p.ScrollbackLen(), n)
}

func TestLeaderXClosesTheFocusedPane(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	d := m.tabs[0].detach.(*fakeDetacher)
	m, cmd := leaderCmd(t, m, "x")
	m = pump(t, m, cmd)
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none", len(m.tabs))
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want 1", d.closed)
	}
}

// leaderCmd is leader() keeping the command, for the second keys that do
// their work in one.
func leaderCmd(t *testing.T, m Model, second string) (Model, tea.Cmd) {
	t.Helper()
	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press(second))
	return mm.(Model), cmd
}

func TestCtrlCGoesToTheChildWhenAPaneHasFocus(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	if got := step(t, m, "ctrl+c"); got.quitting {
		t.Error("ctrl+c quit the dashboard instead of interrupting the child")
	}
	m.focus = focusSidebar
	if got := step(t, m, "ctrl+c"); !got.quitting {
		t.Error("ctrl+c did not quit from the sidebar")
	}
}

func TestHelpMentionsTheLeader(t *testing.T) {
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, &fakeHost{t: t})
	m.showHelp = true
	if got := plain(m.helpView(80)); !strings.Contains(got, "leader") {
		t.Errorf("help %q never mentions the leader", got)
	}
}

func TestAKeyDismissesTheHelpOverlayWithAPaneFocused(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	m = leader(t, m, "?")
	if !m.showHelp {
		t.Fatal("leader ? did not open the help overlay")
	}
	// `x` with a pane focused would otherwise go to the child, leaving the
	// overlay up and the keyboard pointed at something the overlay covers.
	m = step(t, m, "x")
	if m.showHelp {
		t.Error("an ordinary key did not dismiss the overlay")
	}
	// ...and it was consumed doing so: the leader is not armed, no mode
	// opened, and the tab is still there.
	if m.leaderArmed || m.mode != modeNormal || len(m.tabs) != 1 {
		t.Errorf("the dismissing key did something else: armed=%v mode=%v tabs=%d",
			m.leaderArmed, m.mode, len(m.tabs))
	}
}
