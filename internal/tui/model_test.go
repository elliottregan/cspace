package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// --- fakes ---

type fakePoller struct{ snap Snapshot }

func (f fakePoller) Poll(context.Context) Snapshot { return f.snap }

type recordingActor struct {
	downCalls      []Row
	interruptCalls []Row
	sendCalls      []struct {
		row  Row
		text string
	}
	browserCalls []Row
	attachCalls  []Row
}

func (a *recordingActor) result(label string) tea.Cmd {
	return func() tea.Msg { return actionResultMsg{label: label} }
}
func (a *recordingActor) Attach(r Row) tea.Cmd {
	a.attachCalls = append(a.attachCalls, r)
	return a.result("attach")
}
func (a *recordingActor) Down(r Row) tea.Cmd {
	a.downCalls = append(a.downCalls, r)
	return a.result("down")
}
func (a *recordingActor) Interrupt(r Row) tea.Cmd {
	a.interruptCalls = append(a.interruptCalls, r)
	return a.result("interrupt")
}
func (a *recordingActor) RestartBrowser(r Row) tea.Cmd {
	a.browserCalls = append(a.browserCalls, r)
	return a.result("browser restart")
}
func (a *recordingActor) Send(r Row, text string) tea.Cmd {
	a.sendCalls = append(a.sendCalls, struct {
		row  Row
		text string
	}{r, text})
	return a.result("send")
}

func twoSandboxSnap() Snapshot {
	return Snapshot{Rows: []Row{
		{Kind: RowProject, Name: "alpha"},
		{Kind: RowSandbox, Project: "alpha", Name: "mercury", Container: "cspace-alpha-mercury",
			State: StateRunning, Selectable: true, Agent: AgentStatus{Reachable: true, State: "working"}},
		{Kind: RowSidecar, Project: "alpha", Name: "convex"},
		{Kind: RowBrowser, Project: "alpha", Name: "browser (shared)", Container: "cspace-alpha-browser", Selectable: true},
	}}
}

func newTestModel(a Actor) Model {
	m := NewModel(fakePoller{snap: twoSandboxSnap()}, a, "/home/x", 2*time.Second,
		func() time.Time { return time.Unix(1_000_000, 0) })
	// seed rows as if a poll landed
	mm, _ := m.Update(snapshotMsg{snap: twoSandboxSnap()})
	return mm.(Model)
}

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestSelectionSkipsNonSelectableRows(t *testing.T) {
	m := newTestModel(&recordingActor{})
	// first selectable row is index 1 (sandbox)
	if got := m.selectedRow().Name; got != "mercury" {
		t.Fatalf("initial selection = %q, want mercury", got)
	}
	// move down: should skip sidecar(2) and land on browser(3)
	mm, _ := m.Update(key('j'))
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "browser (shared)" {
		t.Errorf("after j, selection = %q, want browser (shared)", got)
	}
}

func TestDownRequiresConfirm(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	// 'd' opens confirm, does NOT call Down yet
	mm, _ := m.Update(key('d'))
	m = mm.(Model)
	if len(a.downCalls) != 0 {
		t.Fatal("Down called before confirm")
	}
	// 'y' confirms
	mm, _ = m.Update(key('y'))
	_ = mm.(Model)
	if len(a.downCalls) != 1 || a.downCalls[0].Name != "mercury" {
		t.Errorf("Down calls = %+v, want one for mercury", a.downCalls)
	}
}

func TestDownConfirmCancel(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('d'))
	m = mm.(Model)
	mm, _ = m.Update(key('n'))
	_ = mm.(Model)
	if len(a.downCalls) != 0 {
		t.Errorf("Down should be cancelled, got %+v", a.downCalls)
	}
}

func TestInterruptOnlyWhenWorking(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a) // mercury agent State=="working"
	mm, _ := m.Update(key('i'))
	_ = mm.(Model)
	if len(a.interruptCalls) != 1 {
		t.Errorf("interrupt calls = %d, want 1", len(a.interruptCalls))
	}
}

func TestActionInFlightGatesOtherActions(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	// start an interrupt => action in flight
	mm, _ := m.Update(key('i'))
	m = mm.(Model)
	// a second action key must be ignored while one is running
	mm, _ = m.Update(key('d'))
	m = mm.(Model)
	if m.mode == modeConfirmDown {
		t.Error("down confirm opened while an action was in flight")
	}
	// resolving the action clears the gate
	mm, _ = m.Update(actionResultMsg{label: "interrupt"})
	m = mm.(Model)
	if m.action != "" {
		t.Errorf("action still in flight after result: %q", m.action)
	}
}

func TestSnapshotPreservesSelectionByIdentity(t *testing.T) {
	m := newTestModel(&recordingActor{})
	mm, _ := m.Update(key('j')) // select browser
	m = mm.(Model)
	// a new snapshot with an extra project prepended must keep browser selected
	snap := twoSandboxSnap()
	snap.Rows = append([]Row{
		{Kind: RowProject, Name: "aaa"},
		{Kind: RowSandbox, Project: "aaa", Name: "x", Selectable: true},
	}, snap.Rows...)
	mm, _ = m.Update(snapshotMsg{snap: snap})
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "browser (shared)" {
		t.Errorf("selection after snapshot = %q, want browser (shared)", got)
	}
}

func TestQuitOnCtrlC(t *testing.T) {
	m := newTestModel(&recordingActor{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("quit command produced nil msg")
	} // tea.Quit's msg is tea.QuitMsg{}
}

// --- send / input state machine (mercury at row 1 is a reachable sandbox, so
// canSend is true; the browser row at index 3 is Selectable but RowBrowser) ---

func TestSendOpensInputMode(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('s'))
	m = mm.(Model)
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want modeInput after 's'", m.mode)
	}
	if len(a.sendCalls) != 0 {
		t.Errorf("Send called before Enter: %+v", a.sendCalls)
	}
}

func TestSendEnterCallsActor(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('s'))
	m = mm.(Model)
	m.input.SetValue("do the thing")
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if len(a.sendCalls) != 1 {
		t.Fatalf("send calls = %d, want 1", len(a.sendCalls))
	}
	if a.sendCalls[0].text != "do the thing" || a.sendCalls[0].row.Name != "mercury" {
		t.Errorf("send call = %+v, want {mercury, \"do the thing\"}", a.sendCalls[0])
	}
	if m.action != "send" {
		t.Errorf("action = %q, want \"send\"", m.action)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after send", m.mode)
	}
}

func TestSendEmptyInputSendsNothing(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('s'))
	m = mm.(Model)
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mm.(Model)
	if len(a.sendCalls) != 0 {
		t.Errorf("empty Enter sent %+v, want none", a.sendCalls)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal", m.mode)
	}
	if m.action != "" {
		t.Errorf("action = %q, want empty", m.action)
	}
}

func TestSendEscCancels(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('s'))
	m = mm.(Model)
	m.input.SetValue("discarded")
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(Model)
	if len(a.sendCalls) != 0 {
		t.Errorf("Esc sent %+v, want none", a.sendCalls)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal after Esc", m.mode)
	}
}

func TestSendGatedOnReachableSandbox(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('j')) // move to browser row (not a sandbox)
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "browser (shared)" {
		t.Fatalf("selection = %q, want browser (shared)", got)
	}
	mm, _ = m.Update(key('s'))
	m = mm.(Model)
	if m.mode == modeInput {
		t.Error("'s' opened input on a non-sandbox row")
	}
	if len(a.sendCalls) != 0 {
		t.Errorf("Send reachable-gate bypassed: %+v", a.sendCalls)
	}
}

func TestErrorNoticeClearsOnKeypress(t *testing.T) {
	m := newTestModel(&recordingActor{})
	mm, _ := m.Update(actionResultMsg{label: "down", err: errors.New("boom")})
	m = mm.(Model)
	if !m.notice.isErr || m.notice.text == "" {
		t.Fatal("error notice should be set after a failed action")
	}
	mm, _ = m.Update(key('j')) // any keypress
	m = mm.(Model)
	if m.notice.text != "" {
		t.Errorf("error notice should clear on the next keypress, got %q", m.notice.text)
	}
}

func TestSnapshotErrorKeepsLastKnownRows(t *testing.T) {
	m := newTestModel(&recordingActor{}) // seeded with a good snapshot
	before := len(m.rows)
	if before == 0 {
		t.Fatal("expected seeded rows")
	}
	// A failed `container ls` poll must NOT blank the rows.
	mm, _ := m.Update(snapshotMsg{snap: Snapshot{Rows: nil, Err: errors.New("apiserver down")}})
	m = mm.(Model)
	if len(m.rows) != before {
		t.Errorf("rows = %d after failed poll, want last-known %d", len(m.rows), before)
	}
	if m.snapErr == nil {
		t.Error("snapErr should be set so the view can mark rows stale")
	}
}
