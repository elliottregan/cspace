package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/pane"
)

// fakeHost opens real panes running a harmless command, so the tab
// bookkeeping is exercised against the real engine without a container.
//
// t is not decoration: every pane it opens is a real child on a real pty
// with four goroutines behind it, and most of these tests never close the
// tab they opened. Registering the close as a cleanup is what internal/pane's
// own openTestPane does (pane_test.go:33), and without it one `go test` run
// of this package leaves a dozen `sleep 30` children and their pty masters
// alive until the test binary exits — under `make test-race`, with a race
// detector watching all of them.
type fakeHost struct {
	t       *testing.T
	opens   []Kind
	rows    []control.Row
	sweeps  int
	openErr error
	warn    string
	// sweep and sweepErr are what Sweep reports; both zero value by default,
	// matching a sweep that found and broke on nothing.
	sweep    SweepOutcome
	sweepErr error
	// history makes the child print enough lines to fill a scrollback, for
	// the tests that scroll.
	history bool
	// echo makes the child turn the tty's own echo off and print back what
	// it reads, in caret notation. It is how Task 4 proves a keystroke
	// actually reached the child: with a 512-slot queue being drained,
	// Dropped() is zero whether or not a byte was ever sent.
	echo bool
	// exits makes the child die the moment it starts, for the tests that
	// watch a pane end on its own rather than by the operator's key.
	exits bool
	// nilPane makes Open report success with no Pane and no error — the
	// shape a PaneHost must not be trusted to avoid on its own. It still
	// hands back a detacher, because the real host books the attach before
	// it opens the pane: that is the thing nothing else can release.
	nilPane bool
	// noPaneDetach is the detacher a nilPane open handed out, kept so a
	// test can assert the model closed it.
	noPaneDetach *fakeDetacher
}

func (h *fakeHost) Open(_ context.Context, kind Kind, row control.Row, cols, rows int) (Opened, error) {
	h.opens = append(h.opens, kind)
	h.rows = append(h.rows, row)
	if h.openErr != nil {
		return Opened{}, h.openErr
	}
	if h.nilPane {
		h.noPaneDetach = &fakeDetacher{}
		return Opened{Detach: h.noPaneDetach}, nil
	}
	script := "sleep 30"
	switch {
	case h.history:
		script = "i=0; while [ $i -lt 200 ]; do echo line$i; i=$((i+1)); done; sleep 30"
	case h.echo:
		// raw so a single byte is delivered without waiting for a newline,
		// -echo so what lands on the screen is the child's doing and not
		// the line discipline's, and `cat -v` so a control byte is visible
		// (NUL prints as ^@).
		script = "stty raw -echo; cat -v"
	case h.exits:
		script = "exit 0"
	}
	p, err := pane.Open(pane.Command{Path: "/bin/sh", Args: []string{"sh", "-c", script}}, cols, rows)
	if err != nil {
		return Opened{}, err
	}
	// Close is idempotent (sync.Once), so a tab the test closed itself, or
	// one the dashboard reaped when its child exited, costs nothing here.
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return Opened{Pane: p, Detach: &fakeDetacher{}, Warning: h.warn}, nil
}

func (h *fakeHost) Sweep(context.Context) (SweepOutcome, error) {
	h.sweeps++
	return h.sweep, h.sweepErr
}

type fakeDetacher struct{ closed int }

func (d *fakeDetacher) Close(context.Context) error { d.closed++; return nil }

// stepPump delivers a key and runs the command it produced. step() throws
// the command away, which is fine for the keys that only change state and
// useless for the ones whose whole effect is in a command — opening a pane,
// closing one, quitting.
func stepPump(t *testing.T, m Model, k string) Model {
	t.Helper()
	mm, cmd := m.Update(press(k))
	return pump(t, mm.(Model), cmd)
}

// pump runs one Cmd and feeds the messages it produced back into the model.
//
// It goes exactly one level deep, and that is deliberate: the follow-up
// command an open produces is awaitOutput, which blocks until the child's
// next frame — a child running `sleep 30` never has one, and a recursive
// pump would hang on it rather than fail. The three-second ceiling covers
// the same hazard for the command it does run.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	done := make(chan []tea.Msg, 1)
	go func() { done <- drain(cmd) }()
	select {
	case msgs := <-done:
		for _, msg := range msgs {
			if msg == nil {
				continue
			}
			mm, _ := m.Update(msg)
			m = mm.(Model)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a command never produced a message")
	}
	return m
}

func TestEnterOpensAClaudePaneAndSecondEnterFocusesIt(t *testing.T) {
	h := &fakeHost{t: t}
	a := &recordingActor{}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, a, h)

	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	if len(h.opens) != 1 || h.opens[0] != KindClaude {
		t.Fatalf("opens = %v, want one KindClaude", h.opens)
	}
	if m.focus != focusMain {
		t.Error("opening a pane did not move focus to the main area")
	}
	// Enter goes to the PaneHost now, never to the Actor: Actor no longer
	// declares Attach at all, so the old step-3 dispatch this once guarded
	// against cannot exist any more, even by accident.

	// A second Enter on the same row focuses the tab it already has rather
	// than starting a second Claude against one workspace.
	//
	// The focus has to go back to the sidebar first, and not as a
	// convenience: from Task 4 on, a key pressed while the main area has
	// focus goes to the child, so an Enter left pointed at the pane would
	// never reach openOrFocus and both assertions below would hold for the
	// wrong reason. TestShellAndSupervisorOpenTheirOwnTabs does the same.
	m.focus = focusSidebar
	m2 := step(t, m, "enter")
	if len(h.opens) != 1 {
		t.Errorf("opens = %v, want the existing tab focused", h.opens)
	}
	if m2.focused != 0 {
		t.Errorf("focused = %d, want the existing tab", m2.focused)
	}
}

func TestShellAndSupervisorOpenTheirOwnTabs(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "s")
	mustTabs(t, m, 1)
	m.focus = focusSidebar
	m = stepPump(t, m, "a")
	mustTabs(t, m, 2)

	if got := []Kind{m.tabs[0].kind, m.tabs[1].kind}; got[0] != KindShell || got[1] != KindSupervisor {
		t.Errorf("kinds = %v, want shell then supervisor", got)
	}
	// The supervisor view is not a pane: it runs no process.
	if m.tabs[1].p != nil {
		t.Error("the supervisor tab opened a pty")
	}
	if len(h.opens) != 1 {
		t.Errorf("host opened %d panes, want only the shell", len(h.opens))
	}
}

func TestAFailedOpenBecomesAFooterErrorAndNoTab(t *testing.T) {
	h := &fakeHost{t: t, openErr: errors.New("no container yet")}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none after a failed open", len(m.tabs))
	}
	if !m.notice.isErr {
		t.Error("a failed open left no error notice")
	}
	// Pins the footer's contract, not just that it errored: every caller of
	// LabelOpenPane's failure text builds it as "open pane failed: <cause>",
	// and a rename of that prefix should fail a test, not just look wrong.
	if !strings.Contains(m.notice.text, "open pane failed: ") {
		t.Errorf("notice text = %q, want it to start with %q", m.notice.text, "open pane failed: ")
	}
	if m.action != "" {
		t.Error("the action gate is still held after a failed open")
	}
}

// TestAnOpenWithNoPaneBecomesAFooterErrorAndNoTab is the PaneHost contract a
// type system cannot enforce: Opened{} with a nil Pane and a nil error is a
// success with nothing behind it. Every other code path treats a nil p as
// "no process" (a KindSupervisor tab), so this would not crash — it would
// become a tab that can never draw, resize, redraw or close: a zombie the
// operator can neither use nor get rid of. Treat it as the open failure it
// is instead.
func TestAnOpenWithNoPaneBecomesAFooterErrorAndNoTab(t *testing.T) {
	h := &fakeHost{t: t, nilPane: true}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	mm, cmd := m.Update(press("enter"))
	m = mm.(Model)
	opened, ok := runCmd(t, cmd).(paneOpenedMsg)
	if !ok {
		t.Fatal("enter did not open a pane")
	}
	mm, cmd = m.Update(opened)
	m = mm.(Model)
	drain(cmd) // the release below is a command, not an inline Close

	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none after an open with no pane", len(m.tabs))
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "open pane failed: ") {
		t.Errorf("notice = %+v, want an open pane failure", m.notice)
	}
	if m.action != "" {
		t.Error("the action gate is still held after an open with no pane")
	}
	// The host booked the attach before it failed to hand back a pane, and
	// no tab exists for leader x or quit to reach that booking through —
	// so the only place it can be released is here.
	if h.noPaneDetach.closed != 1 {
		t.Errorf("the stranded attachment was closed %d times, want 1: its lock and "+
			"client record are held for the life of the process otherwise",
			h.noPaneDetach.closed)
	}
}

func TestCloseTabDetachesAndTearsDown(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	d := m.tabs[0].detach.(*fakeDetacher)

	m = pump(t, m, m.closeTab(m.tabs[0].id))
	if len(m.tabs) != 0 {
		t.Errorf("tabs = %d, want none", len(m.tabs))
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want 1", d.closed)
	}
	if m.focus != focusSidebar {
		t.Error("closing the last tab did not return focus to the sidebar")
	}
}

// TestAnExitedPaneReapsItself is the close nobody presses a key for, and it
// is the commonest one: the child ends on its own. 4a closes Dirty only
// inside Pane.Close, so the waiter's final markDirty is the last signal an
// exited-but-unclosed pane will ever emit — miss it and the tmux client
// stays attached inside the sandbox, its record file stays on the host, and
// the pty master and x/vt's parser buffer stay live until the operator
// happens to press leader x.
func TestAnExitedPaneReapsItself(t *testing.T) {
	h := &fakeHost{t: t, exits: true}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "enter")
	mustTabs(t, m, 1)
	tb := m.tabs[0]
	d := tb.detach.(*fakeDetacher)

	// The redraw wait the open armed, run by hand: stepPump discards it.
	out, ok := runCmd(t, awaitOutput(tb)).(paneOutputMsg)
	if !ok {
		t.Fatal("the pane never signalled")
	}
	mm, cmd := m.Update(out)
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("an exited pane produced no teardown; it would leak until leader x")
	}
	if !tb.closing {
		t.Error("the reap was not marked in flight, so a second signal would start another")
	}
	reaped, ok := runCmd(t, cmd).(paneReapedMsg)
	if !ok {
		t.Fatal("the teardown did not report back")
	}
	if reaped.err != nil {
		t.Errorf("reap error = %v", reaped.err)
	}
	mm, _ = m.Update(reaped)
	m = mm.(Model)

	if d.closed != 1 {
		t.Errorf("detacher closed %d times, want exactly 1 — and nobody pressed a key", d.closed)
	}
	// The tab stays, so the last screen is still readable; leader x is what
	// removes it.
	mustTabs(t, m, 1)
	// And nothing re-arms on it. A closed pane's Dirty() returns
	// immediately and forever, so a re-armed wait would spin at the tick
	// rate rather than park.
	if _, again := m.Update(paneOutputMsg{id: tb.id}); again != nil {
		t.Error("an exited pane re-armed its output wait")
	}
}

// A Claude pane takes seconds to appear, so a window resize landing while
// the open is in flight is an ordinary event — and the WindowSizeMsg arm
// can only resize the tabs that exist. The pane that arrives afterwards
// carries the geometry it was opened at until something else resizes it,
// which is a pane drawing at the old width inside a box at the new one.
func TestAPaneOpenedDuringAResizeGetsTheCurrentLayout(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	mm, cmd := m.Update(press("enter"))
	m = mm.(Model)

	// The window changes while the host is still opening.
	mm, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = mm.(Model)
	wantCols, wantRows := m.paneSize()

	opened, ok := runCmd(t, cmd).(paneOpenedMsg)
	if !ok {
		t.Fatal("enter did not open a pane")
	}
	mm, _ = m.Update(opened)
	m = mm.(Model)
	mustTabs(t, m, 1)

	// The emulator's own geometry, read the only way it is observable from
	// outside: Render draws the whole screen, so its line count is the row
	// count and each line is exactly as wide as the screen.
	lines := strings.Split(plain(m.tabs[0].p.Render()), "\n")
	if len(lines) != wantRows {
		t.Errorf("the new pane renders %d rows, want the current layout's %d",
			len(lines), wantRows)
	}
	if got := ansi.StringWidth(lines[0]); got != wantCols {
		t.Errorf("the new pane renders %d columns, want the current layout's %d",
			got, wantCols)
	}
}

// TestAnOpenThatWarnsGoesThroughResultWarn pins the mechanism, not the text:
// an open that worked but degraded — the no-tmux fallback — reports through
// ResultWarn, the same path every other "it worked, now read this" takes, so
// there is one place that decides such a notice is sticky.
func TestAnOpenThatWarnsGoesThroughResultWarn(t *testing.T) {
	// exits, so the redraw wait batched with the warning comes back at once:
	// a child that prints nothing and never dies leaves awaitOutput parked
	// on Dirty, and drain would wait on it.
	h := &fakeHost{t: t, exits: true,
		warn: "mercury has no tmux: this session will not survive the window"}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)

	mm, cmd := m.Update(press("enter"))
	m = mm.(Model)
	opened, ok := runCmd(t, cmd).(paneOpenedMsg)
	if !ok {
		t.Fatal("enter did not open a pane")
	}
	mm, cmd = m.Update(opened)
	m = mm.(Model)

	warn := ""
	for _, msg := range drain(cmd) {
		if got := ResultWarnText(msg); got != "" {
			warn = got
			mm, _ = m.Update(msg)
			m = mm.(Model)
		}
	}
	if !strings.Contains(warn, "no tmux") {
		t.Fatalf("the open emitted no warning result; got %q", warn)
	}
	if !m.notice.isErr || !strings.Contains(m.notice.text, "no tmux") {
		t.Errorf("notice = %+v, want the warning in the alert style", m.notice)
	}
}

// runCmd runs one command and hands back the message it produced. The
// ceiling is the same hazard pump documents: a command that never answers
// should fail the test rather than hang it.
//
// startAction (pre-dating this task) batches every action's own command
// with m.spinner.Tick, so the Cmd Update hands back for an action — opening
// a pane included — is a tea.BatchMsg of [the real command, the spinner's
// first nudge], not the real message on its own; a live tea.Program handles
// that fan-out itself, which is what pump's use of drain simulates. runCmd
// does the same unwrapping and discards the spinner's own tick message,
// which no caller here is ever asserting on.
//
// Everything else that survives the filter is collected rather than
// returned on first sight: the paneOpenedMsg arm batches its redraw wait
// with a warning's actionResultMsg (see TestAnOpenThatWarnsGoesThroughResultWarn),
// and silently handing back only the first of those would make a caller
// that only wanted one of them look like it worked while quietly dropping
// the other. More than one non-spinner message is therefore a test bug —
// this cmd was assumed to yield one thing — and it fails loudly, naming
// what it found, rather than picking a survivor.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	done := make(chan []tea.Msg, 1)
	go func() {
		var msgs []tea.Msg
		for _, msg := range drain(cmd) {
			if _, isTick := msg.(spinner.TickMsg); isTick {
				continue
			}
			msgs = append(msgs, msg)
		}
		done <- msgs
	}()
	select {
	case msgs := <-done:
		switch len(msgs) {
		case 0:
			return nil
		case 1:
			return msgs[0]
		default:
			types := make([]string, len(msgs))
			for i, m := range msgs {
				types[i] = fmt.Sprintf("%T", m)
			}
			t.Fatalf("runCmd got %d messages, want at most 1: %v", len(msgs), types)
			return nil
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a command never produced a message")
		return nil
	}
}

// mustTabs is a fatal check, not a wait, and deliberately so: stepPump has
// already run the open's command and fed paneOpenedMsg back into the model
// before this is called, so the tab either exists by now or never will.
// Polling could not help anyway — Model is a value with a value-receiver
// Update, and nothing else holds a reference to this one to mutate.
// (waitForHistory in leader_test.go is different: it polls a live *pane.Pane
// that a goroutine really is filling in.)
func mustTabs(t *testing.T, m Model, n int) {
	t.Helper()
	if len(m.tabs) != n {
		t.Fatalf("tabs = %d, want %d", len(m.tabs), n)
	}
}
