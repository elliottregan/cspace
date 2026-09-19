package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeClipboard records what it was asked for and answers with what the
// test set.
type fakeClipboard struct {
	path    string
	imgErr  error
	text    string
	textErr error
	// file is the host path Image reports beside path — the PNG as this
	// process can reach it. Tests that care about the cleanup point it at
	// a real file under t.TempDir().
	file string

	calls []string
	// bounded records whether the context Image was handed carries a
	// deadline, which is the Global Constraint on every seam that shells
	// out.
	bounded bool
}

func (c *fakeClipboard) Image(ctx context.Context, project, sandbox string) (string, string, error) {
	_, c.bounded = ctx.Deadline()
	c.calls = append(c.calls, fmt.Sprintf("image:%s/%s", project, sandbox))
	if c.imgErr != nil {
		return "", "", c.imgErr
	}
	return c.path, c.file, nil
}

func (c *fakeClipboard) Text(context.Context) (string, error) {
	c.calls = append(c.calls, "text")
	return c.text, c.textErr
}

// newTestModelWithClipboard is newTestModelWithHost plus a clipboard.
func newTestModelWithClipboard(h PaneHost, c Clipboard) Model {
	d := &fakeData{snap: testSnapshot()}
	m := New(d, &recordingActor{}, h, c, NewKeyMap(nil))
	m.now = func() time.Time { return time.Unix(1_000_060, 0) }
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = mm.(Model)
	mm, _ = m.Update(snapshotMsg{snap: d.snap})
	return mm.(Model)
}

// pasteV arms the leader, presses v, runs the command it produced, and
// feeds the result back.
//
// Note what it does NOT do: pump throws away the command the pasteMsg arm
// returns, which is the cleanup. A test about the PNG has to run that one
// itself — see runDiscard.
func pasteV(t *testing.T, m Model) Model {
	t.Helper()
	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	return pump(t, mm.(Model), cmd)
}

// runDiscard runs the cleanup command a pasteMsg arm handed back. It is a
// plain unlink, so there is nothing to wait for — and nothing to feed back,
// which it also asserts.
func runDiscard(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("the cleanup command produced %T, want nothing", msg)
	}
}

// pasteTempPNG stands in for the file osascript would have left on the
// host, so a test can watch what becomes of it.
func pasteTempPNG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "20260919-143001.123.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatalf("write the stand-in PNG: %v", err)
	}
	return path
}

// gone reports whether the PNG is no longer on disk.
func gone(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return err != nil
}

func TestLeaderVPastesTheImagePathIntoAClaudePane(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	const path = "/sessions/paste/20260919-143001.123.png"
	c := &fakeClipboard{path: path}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	m = pasteV(t, m)

	if len(c.calls) != 1 || c.calls[0] != "image:alpha/mercury" {
		t.Fatalf("clipboard calls = %v, want one Image for the pane's own sandbox", c.calls)
	}
	if m.notice.isErr {
		t.Errorf("notice = %q, want no error", m.notice.text)
	}
	// The echo child prints back what it read, in caret notation.
	waitForPaneScreen(t, m.tabs[0], path)
	// No newline: the path is typed, not sent. `cat -v` would print a CR
	// as ^M.
	if screen := plain(m.tabs[0].p.Render()); strings.Contains(screen, "^M") {
		t.Errorf("the paste carried a carriage return: %q", screen)
	}
	// The ^M check alone proves nothing: it is blind to a trailing "\n"
	// (cat -v passes LF through, and in raw mode OPOST is off, so it just
	// moves the cursor down and prints nothing at all), and it is blind to
	// a "\r" that arrives before the child's own `stty raw` has run, which
	// ICRNL turns into a newline on the way in. A mutant appending either
	// one survives it — measured, both of them.
	//
	// A sentinel keystroke sent AFTER the paste is what makes "nothing
	// followed the path" observable. The pane's write queue is FIFO and
	// the child reads in order, so once the sentinel is on screen the
	// paste has been through in full; anything the paste appended would
	// then sit between the path and the sentinel — or push the sentinel
	// onto another row.
	m = step(t, m, "Z")
	waitForPaneScreen(t, m.tabs[0], "Z")
	if screen := plain(m.tabs[0].p.Render()); !strings.Contains(screen, path+"Z") {
		t.Errorf("the next keystroke did not land immediately after the path — "+
			"something was typed in between:\n%s", screen)
	}
}

// TestPressingVDoesNotReadTheClipboardFromUpdate is the Global Constraint
// as a test: a Clipboard method shells out to osascript, and one run on the
// UI goroutine is a frozen window. The press must only hand back a command.
func TestPressingVDoesNotReadTheClipboardFromUpdate(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	m = mm.(Model)
	if len(c.calls) != 0 {
		t.Fatalf("Update itself read the clipboard: %v", c.calls)
	}
	if cmd == nil {
		t.Fatal("leader v produced no command; the read has to happen somewhere")
	}
	if m.action != LabelPasteImage {
		t.Errorf("action = %q, want the paste marked in flight", m.action)
	}
	if m = pump(t, m, cmd); len(c.calls) != 1 {
		t.Errorf("clipboard calls after running the command = %v, want one", c.calls)
	}
	// And the read it does is bounded. osascript is a host binary that can
	// hang; an unbounded one would leave the footer spinning on an action
	// that never reports, with the one-action gate shut behind it.
	if !c.bounded {
		t.Error("the clipboard was read with a context that has no deadline")
	}
}

// TestLeaderVOnAnExitedPaneRefusesWithoutReadingTheClipboard: a pane whose
// child is gone has nothing to type into, and the refusal comes before the
// clipboard is touched — there is no point writing a PNG nobody can ask
// for.
func TestLeaderVOnAnExitedPaneRefusesWithoutReadingTheClipboard(t *testing.T) {
	h := &fakeHost{t: t, exits: true}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	waitForExit(t, m.tabs[0])

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read for an exited pane: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "exited") {
		t.Errorf("notice = %q, want it to say the pane exited", m.notice.text)
	}
	// The exact text, against the one shared definition rather than a copy
	// of its wording — the same lock TestScrollRefusesAPaneWithNoScrollback
	// puts on noScrollbackNotice, so the keypress and the result that comes
	// back for a dead pane can never drift apart.
	if m.notice.text != pasteExitedNotice().text {
		t.Errorf("notice = %q, want the shared refusal %q", m.notice.text, pasteExitedNotice().text)
	}
	if m.action != "" {
		t.Errorf("action = %q, want nothing in flight", m.action)
	}
}

// TestAPasteForAPaneThatExitedIsRefused is the other end of the same race:
// the pane was alive when v was pressed and its child died while osascript
// ran. The path is real and on disk, so the person is told rather than left
// watching a paste vanish.
func TestAPasteForAPaneThatExitedIsRefused(t *testing.T) {
	h := &fakeHost{t: t, exits: true}
	m := newTestModelWithClipboard(h, &fakeClipboard{})
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	waitForExit(t, m.tabs[0])

	file := pasteTempPNG(t)
	mm, cleanup := m.Update(pasteMsg{id: m.tabs[0].id, text: "/sessions/paste/x.png", file: file})
	m = mm.(Model)
	runDiscard(t, cleanup)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "exited") {
		t.Errorf("notice = %q, want it to say the pane exited", m.notice.text)
	}
	if m.notice.text != pasteExitedNotice().text {
		t.Errorf("notice = %q, want the shared refusal %q", m.notice.text, pasteExitedNotice().text)
	}
	if !gone(t, file) {
		t.Error("the PNG survived a paste the pane could not receive")
	}
}

// TestAPasteFindsItsTabByIdentityNotIndex: closing a tab shifts every index
// after it, so a result that came back by position would type a path — a
// path for another sandbox's /sessions mount — into whichever tab slid into
// the closed one's slot.
func TestAPasteFindsItsTabByIdentityNotIndex(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	m := newTestModelWithClipboard(h, &fakeClipboard{})
	m = stepPump(t, m, "enter") // tab 0: claude
	m.focus = focusSidebar
	m = stepPump(t, m, "s") // tab 1: shell
	mustTabs(t, m, 2)
	first, second := m.tabs[0], m.tabs[1]

	// The first tab goes; the second slides into index 0, and the id the
	// in-flight paste is carrying is the one that no longer exists.
	mm, _ := m.Update(paneClosedMsg{id: first.id})
	m = mm.(Model)
	mustTabs(t, m, 1)

	m.action = LabelPasteImage // the paste this result belongs to
	mm, _ = m.Update(pasteMsg{id: first.id, text: "wrong-tab-sentinel"})
	if got := mm.(Model); got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
	} else if got.action != "" {
		t.Errorf("action = %q, want the one-action gate released", got.action)
	}
	// Give a wrongly-routed paste time to round-trip through the pty and
	// the echoing child before checking for its absence.
	time.Sleep(250 * time.Millisecond)
	if strings.Contains(plain(second.p.Render()), "wrong-tab-sentinel") {
		t.Error("the paste landed in the tab that took the closed one's index")
	}
}

// TestLeaderVWhileAnotherActionIsInFlightIsIgnored: the one-action gate the
// other leader keys apply. Two osascript runs would race for the one footer
// line, and the first result back would clear the marker for both.
func TestLeaderVWhileAnotherActionIsInFlightIsIgnored(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	m.action = LabelClosePane

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read under another action: %v", c.calls)
	}
	if m.action != LabelClosePane {
		t.Errorf("action = %q, want the in-flight one left alone", m.action)
	}
	// The notice names what is actually holding the gate, which here is
	// not the paste.
	if !m.notice.isErr || !strings.Contains(m.notice.text, LabelClosePane) {
		t.Errorf("notice = %q, want it to name the close that is still running", m.notice.text)
	}
}

func TestLeaderVOnAHostShellAsksForTheHostPath(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/Users/x/.cspace/paste/20260919-143001.000.png"}
	m := newTestModelWithClipboard(h, c)
	// The picker is the only way to a host shell: it belongs to no row.
	// openHostShell (leader_test.go) already drives it.
	m = openHostShell(t, m)
	mustTabs(t, m, 1)

	pasteV(t, m)
	if len(c.calls) != 1 || c.calls[0] != "image:/" {
		t.Fatalf("clipboard calls = %v, want an Image with no sandbox", c.calls)
	}
}

func TestLeaderVOnASupervisorTabSaysThereIsNoPane(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "a")
	mustTabs(t, m, 1)

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read for a tab with no pane: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no pane") {
		t.Errorf("notice = %q, want a 'no pane' error", m.notice.text)
	}
}

func TestLeaderVWithNoTabsSaysThereIsNoPane(t *testing.T) {
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(&fakeHost{t: t}, c)

	m = pasteV(t, m)
	if len(c.calls) != 0 {
		t.Errorf("clipboard was read with no tab open: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no pane") {
		t.Errorf("notice = %q, want a 'no pane' error", m.notice.text)
	}
}

func TestATextOnlyClipboardFallsBackToATextPaste(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: ErrNoImage, text: "func main() {}"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if len(c.calls) != 2 || c.calls[1] != "text" {
		t.Fatalf("clipboard calls = %v, want the image probe then the text", c.calls)
	}
	waitForPaneScreen(t, m.tabs[0], "func main()")
}

func TestAWrappedErrNoImageStillFallsBack(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: fmt.Errorf("probe: %w", ErrNoImage), text: "wrapped"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	pasteV(t, m)
	if len(c.calls) != 2 {
		t.Fatalf("clipboard calls = %v; errors.Is must see through the wrap", c.calls)
	}
}

func TestAnEmptyClipboardSaysSoAndTypesNothing(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{imgErr: ErrNoImage, text: ""}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "empty") {
		t.Errorf("notice = %q, want it to say the clipboard is empty", m.notice.text)
	}
}

func TestAMissingOsascriptIsAFooterErrorNotACrash(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{imgErr: errors.New("osascript not found: image paste needs macOS")}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")

	m = pasteV(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "osascript") {
		t.Errorf("notice = %q, want the osascript failure in the footer", m.notice.text)
	}
	if m.action != "" {
		t.Errorf("action = %q, want the in-flight marker cleared", m.action)
	}
}

func TestAPasteForATabThatClosedIsDropped(t *testing.T) {
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(&fakeHost{t: t}, c)
	// A result only ever arrives with its own paste in flight.
	m.action = LabelPasteImage
	// No tab with this id has ever existed.
	mm, _ := m.Update(pasteMsg{id: 4242, text: "/sessions/paste/x.png"})
	got := mm.(Model)
	if got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
	}
	// Silent is not the same as doing nothing: the gate still has to open,
	// and this is the one branch with no notice to notice its absence by.
	if got.action != "" {
		t.Errorf("action = %q, want the one-action gate released", got.action)
	}
}

// --- fix round 1: the focused tab, the gate, the file, the missing seam ---

// openTwoPanes leaves a Claude pane on alpha/mercury at index 0 and a host
// shell — which belongs to NO sandbox — focused at index 1. The two
// identities differ, so the clipboard call itself names which tab the key
// acted on.
func openTwoPanes(t *testing.T, m Model) Model {
	t.Helper()
	m = stepPump(t, m, "enter")
	m = openHostShell(t, m)
	mustTabs(t, m, 2)
	if m.focused != 1 {
		t.Fatalf("focused = %d after opening a second pane, want 1", m.focused)
	}
	return m
}

// Leader v acts on the tab the main area is SHOWING. Nothing pinned this:
// with one tab open, `m.tabs[0]` and `m.focusedTab()` are the same pointer,
// so a version that read the first tab shipped green — and pasted a
// screenshot into whichever sandbox happened to be opened first.
func TestLeaderVPastesIntoTheFocusedTabNotTheFirst(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{path: "/Users/x/.cspace/paste/focused.png"}
	m := openTwoPanes(t, newTestModelWithClipboard(h, c))

	m = pasteV(t, m)

	if len(c.calls) != 1 || c.calls[0] != "image:/" {
		t.Fatalf("clipboard calls = %v, want the FOCUSED tab's identity — "+
			"a host shell's, which is empty — not the first tab's alpha/mercury", c.calls)
	}
	waitForPaneScreen(t, m.tabs[1], "focused.png")
	if screen := plain(m.tabs[0].p.Render()); strings.Contains(screen, "focused.png") {
		t.Errorf("the path was typed into the unfocused first tab:\n%s", screen)
	}
}

// The keyboard being on the sidebar does not change which pane a paste
// goes to: handleKey dispatches the leader before the focus split, and
// focusedTab reads m.focused (which tab is on screen), not m.focus (who
// owns the keyboard). That is the supported route — ⌃Space h, then ⌃Space
// v — and it had no coverage at all.
func TestLeaderVFromTheSidebarPastesIntoThePaneOnScreen(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	c := &fakeClipboard{path: "/Users/x/.cspace/paste/sidebar.png"}
	m := openTwoPanes(t, newTestModelWithClipboard(h, c))
	m.focus = focusSidebar

	m = pasteV(t, m)

	if len(c.calls) != 1 || c.calls[0] != "image:/" {
		t.Fatalf("clipboard calls = %v, want the tab on screen, not the selected row", c.calls)
	}
	waitForPaneScreen(t, m.tabs[1], "sidebar.png")
	if m.focus != focusSidebar {
		t.Error("leader v moved the keyboard focus off the sidebar")
	}
}

// TestADroppedPasteReleasesTheOneActionGate is the cost of the one branch
// that returns without a notice. m.action is the gate leader t, leader x,
// v itself, every sidebar key and both supervisor arms consult: a
// dropped-paste path that forgot to clear it would take the whole window
// out — no new pane, no close, no boot, no send — for the rest of the
// session, behind a spinner that never stops.
func TestADroppedPasteReleasesTheOneActionGate(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	id := m.tabs[0].id

	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	m = mm.(Model)
	if m.action != LabelPasteImage {
		t.Fatalf("action = %q, want the paste in flight", m.action)
	}

	// The tab goes while osascript runs. dropTab by hand rather than a
	// paneClosedMsg on purpose: that arm clears m.action itself, which is
	// the very thing this test is trying to observe.
	m = m.dropTab(id)
	mustTabs(t, m, 0)

	mm, cleanup := m.Update(runCmd(t, cmd))
	m = mm.(Model)
	runDiscard(t, cleanup)
	if m.action != "" {
		t.Fatalf("action = %q after a paste nobody could receive; the gate is stuck", m.action)
	}
	// And the gate is open in the way that matters: a sidebar key it would
	// have swallowed opens a pane again.
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
}

// A PNG nobody can be told about is an orphan: the paste/ directory is a
// place a sandbox agent globs, and ~/.cspace/paste (the host shell's) is
// never swept at all.
func TestAPasteNoTabCanReceiveTakesItsPNGBackOut(t *testing.T) {
	file := pasteTempPNG(t)
	m := newTestModelWithClipboard(&fakeHost{t: t}, &fakeClipboard{})

	mm, cleanup := m.Update(pasteMsg{id: 4242, text: "/sessions/paste/x.png", file: file})
	runDiscard(t, cleanup)

	if got := mm.(Model); got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
	}
	if !gone(t, file) {
		t.Error("the PNG survived a paste no pane could receive")
	}
}

// The mirror image, and the one branch that must NOT delete: a pane was
// told to read this path, so the file has to be there when it does.
func TestADeliveredPasteKeepsItsPNG(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	file := pasteTempPNG(t)
	const typed = "/sessions/paste/20260919-143001.123.png"
	c := &fakeClipboard{path: typed, file: file}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	m = mm.(Model)

	mm, cleanup := m.Update(runCmd(t, cmd))
	m = mm.(Model)
	if cleanup != nil {
		t.Error("a delivered paste handed back a cleanup command")
		runDiscard(t, cleanup)
	}
	waitForPaneScreen(t, m.tabs[0], typed)
	if gone(t, file) {
		t.Error("the PNG the pane was just told to read has been deleted")
	}
}

// A Model built without a clipboard fails closed and says which seam is
// missing, rather than dereferencing nil or typing nothing and reporting
// success.
func TestAModelWithNoClipboardSaysSoAndTypesNothing(t *testing.T) {
	h := &fakeHost{t: t, echo: true}
	m := newTestModelWithClipboard(h, nil)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	m = pasteV(t, m)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no clipboard configured") {
		t.Errorf("notice = %q, want the missing seam named", m.notice.text)
	}
	// Give a paste that should never have happened time to round-trip
	// through the pty and the echoing child before checking for it.
	time.Sleep(250 * time.Millisecond)
	if screen := strings.TrimSpace(plain(m.tabs[0].p.Render())); screen != "" {
		t.Errorf("something was typed into the pane: %q", screen)
	}

	// And the stand-in must not report ErrNoImage: that is the fallback
	// branch, so the dispatcher would go on to Text and diagnose a seam
	// that was never wired up as a clipboard problem.
	if _, _, err := (nopClipboard{}).Image(context.Background(), "alpha", "mercury"); errors.Is(err, ErrNoImage) {
		t.Error("nopClipboard.Image reports ErrNoImage; the dispatcher would fall back to text")
	}
	if text, err := (nopClipboard{}).Text(context.Background()); err == nil {
		t.Errorf("nopClipboard.Text returned %q and no error; that is indistinguishable "+
			"from an empty clipboard", text)
	}
}

// A second v during a slow osascript starts nothing — and, unlike t and x,
// says why. Ten seconds is long enough that a silent key reads as broken.
func TestASecondVWhileAPasteIsInFlightSaysSo(t *testing.T) {
	h := &fakeHost{t: t}
	c := &fakeClipboard{path: "/sessions/paste/x.png"}
	m := newTestModelWithClipboard(h, c)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)

	// The first paste, whose command is deliberately never run: it is
	// still "in flight" for as long as the test wants.
	m = step(t, m, "ctrl+space")
	mm, _ := m.Update(press("v"))
	m = mm.(Model)

	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	m = mm.(Model)
	if cmd != nil {
		t.Error("a second v started a second clipboard read")
	}
	if len(c.calls) != 0 {
		t.Errorf("the clipboard was read from Update: %v", c.calls)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, LabelPasteImage) ||
		!strings.Contains(m.notice.text, "in progress") {
		t.Errorf("notice = %q, want it to name the paste that is still running", m.notice.text)
	}
}
