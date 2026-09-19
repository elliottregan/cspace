package controlplane

import (
	"context"
	"errors"
	"fmt"
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

	calls []string
	// bounded records whether the context Image was handed carries a
	// deadline, which is the Global Constraint on every seam that shells
	// out.
	bounded bool
}

func (c *fakeClipboard) Image(ctx context.Context, project, sandbox string) (string, error) {
	_, c.bounded = ctx.Deadline()
	c.calls = append(c.calls, fmt.Sprintf("image:%s/%s", project, sandbox))
	if c.imgErr != nil {
		return "", c.imgErr
	}
	return c.path, nil
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
func pasteV(t *testing.T, m Model) Model {
	t.Helper()
	m = step(t, m, "ctrl+space")
	mm, cmd := m.Update(press("v"))
	return pump(t, mm.(Model), cmd)
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

	mm, _ := m.Update(pasteMsg{id: m.tabs[0].id, text: "/sessions/paste/x.png"})
	m = mm.(Model)
	if !m.notice.isErr || !strings.Contains(m.notice.text, "exited") {
		t.Errorf("notice = %q, want it to say the pane exited", m.notice.text)
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

	mm, _ = m.Update(pasteMsg{id: first.id, text: "wrong-tab-sentinel"})
	if got := mm.(Model); got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
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
	// No tab with this id has ever existed.
	mm, _ := m.Update(pasteMsg{id: 4242, text: "/sessions/paste/x.png"})
	if got := mm.(Model); got.notice.isErr {
		t.Errorf("notice = %q, want silence for a tab that is gone", got.notice.text)
	}
}
