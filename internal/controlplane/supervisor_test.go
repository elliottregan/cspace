package controlplane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
)

func supervisorLines() []control.EventLine {
	return []control.EventLine{
		{Ts: "2026-09-18T10:00:00Z", Kind: "sdk-event", Type: "assistant",
			Text:  "I will read **main.go** first.",
			Tools: []string{"Read(/workspace/main.go)"}},
		{Ts: "2026-09-18T10:00:02Z", Kind: "sdk-event", Type: "result", Subtype: "success"},
	}
}

func TestSupervisorViewRendersTextAndToolSummaries(t *testing.T) {
	s := newSupervisor(60)
	s.resize(60, 16)
	s.setEvents(supervisorLines())
	got := plain(s.view(60, 16))

	if !strings.Contains(got, "main.go") {
		t.Errorf("view %q lost the assistant's text", got)
	}
	// Markdown is rendered, so the asterisks are gone even though the text
	// carried them.
	if strings.Contains(got, "**main.go**") {
		t.Errorf("view %q shows raw markdown", got)
	}
	if !strings.Contains(got, "Read(/workspace/main.go)") {
		t.Errorf("view %q has no tool summary", got)
	}
	if !strings.Contains(got, "result") {
		t.Errorf("view %q dropped the result line", got)
	}
	// At most 16: fitLines guarantees the ceiling, and the floor is not
	// bubbles' to promise. textarea.View() renders a prompt column and a
	// cursor column of its own, so "exactly SetHeight(3) lines at exactly
	// SetWidth(w) cells" is an assumption about a widget's internals — the
	// same one sendInputWidth (view.go:189) exists to work around.
	if lines := strings.Count(s.view(60, 16), "\n") + 1; lines > 16 {
		t.Errorf("view is %d lines, want at most 16", lines)
	}
}

func TestSupervisorViewSurvivesAResize(t *testing.T) {
	s := newSupervisor(60)
	s.resize(60, 16) // view is read-only; geometry arrives through resize
	s.setEvents(supervisorLines())
	_ = s.view(60, 16)
	// glamour bakes its wrap width at construction, so a resize has to
	// rebuild the renderer rather than set a field.
	s.resize(30, 10)
	got := s.view(30, 10)
	for _, line := range strings.Split(plain(got), "\n") {
		// At most 30 cells: the claim is that the feed was re-wrapped by a
		// renderer built at the new width, not that every widget in the
		// stack stops at exactly Width(). The send box does not — bubbles
		// draws its prompt and its cursor past that, which is what
		// sendInputWidth subtracts for the footer's own input.
		if ansi.StringWidth(line) > 30 {
			t.Errorf("line %q is wider than the resized view", line)
		}
	}
}

func TestSupervisorEnterSendsAndEscInterrupts(t *testing.T) {
	a := &recordingActor{}
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, a, h)
	// mercury's snapshot agent is idle, and interrupt is gated on a working
	// one exactly as the sidebar's `i` is — the fast ticker reporting it
	// working is what enables the key here too.
	mm, _ := m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Agent: control.AgentStatus{Reachable: true, State: "working"}},
	}})
	m = mm.(Model)
	m = stepPump(t, m, "a") // open the supervisor tab on mercury
	if m.focusedTab() == nil || m.focusedTab().sup == nil {
		t.Fatal("no supervisor tab")
	}

	for _, r := range "hello" {
		m = step(t, m, string(r))
	}
	m = step(t, m, "enter")
	if len(a.sends) != 1 || a.sends[0].text != "hello" {
		t.Fatalf("sends = %+v, want one hello", a.sends)
	}
	if got := m.focusedTab().sup.input.Value(); got != "" {
		t.Errorf("the box kept %q after sending", got)
	}
	if m.action != LabelSend {
		t.Fatalf("action = %q, want the send in flight", m.action)
	}

	// Esc while the send is still out is refused: the supervisor tab is
	// inside the same one-action-at-a-time gate the sidebar's keys pass
	// through. Two actions in flight means two results, the first of which
	// clears the gate for the second and leaves the footer naming the wrong
	// verb.
	if got := step(t, m, "esc"); len(a.interrupt) != 0 {
		t.Errorf("interrupts = %d while the send was in flight, want 0 (action %q)",
			len(a.interrupt), got.action)
	}

	// Once the send's result lands the gate opens and esc interrupts.
	mm, _ = m.Update(Result(LabelSend, nil))
	m = mm.(Model)
	m = step(t, m, "esc")
	if len(a.interrupt) != 1 {
		t.Errorf("interrupts = %d, want 1", len(a.interrupt))
	}
}

// unreachableSnapshot is testSnapshot with mercury's supervisor down — the
// state the spec's error handling names, which the ordinary snapshot (a
// reachable, idle agent) cannot express.
func unreachableSnapshot() control.Snapshot {
	snap := testSnapshot()
	rows := append([]control.Row(nil), snap.Rows...)
	for i := range rows {
		if rows[i].Kind == control.RowSandbox && rows[i].Name == "mercury" {
			rows[i].Agent = control.AgentStatus{}
		}
	}
	snap.Rows = rows
	return snap
}

// Spec, Error handling: "Supervisor unreachable: send and interrupt are
// disabled on that row rather than failing on press". The sidebar gets that
// by disabling the binding; this route is a switch on the key string, so it
// checks the same predicates by hand — and it must not throw the typed text
// away doing it.
func TestSupervisorSendAndInterruptAreGatedOnReachability(t *testing.T) {
	a := &recordingActor{}
	m := newTestModelWithHost(&fakeData{snap: unreachableSnapshot()}, a, &fakeHost{t: t})
	m = stepPump(t, m, "a")
	if m.focusedTab() == nil || m.focusedTab().sup == nil {
		t.Fatal("no supervisor tab")
	}

	for _, r := range "hello" {
		m = step(t, m, string(r))
	}
	m = step(t, m, "enter")
	if len(a.sends) != 0 {
		t.Errorf("sends = %+v to an unreachable supervisor, want none", a.sends)
	}
	if m.action != "" {
		t.Errorf("action = %q, want nothing in flight", m.action)
	}
	if !m.notice.isErr || m.notice.text == "" {
		t.Error("the refusal was silent; the footer has to say why nothing was sent")
	}
	if got := m.focusedTab().sup.input.Value(); got != "hello" {
		t.Errorf("the box holds %q, want the turn kept for a retry", got)
	}

	// mercury's agent is reachable but idle in the ordinary snapshot, which
	// is the interrupt-only half of the same rule.
	m = newTestModelWithHost(&fakeData{snap: testSnapshot()}, a, &fakeHost{t: t})
	m = stepPump(t, m, "a")
	m = step(t, m, "esc")
	if len(a.interrupt) != 0 {
		t.Errorf("interrupts = %d on an idle agent, want 0", len(a.interrupt))
	}
	if !m.notice.isErr {
		t.Error("the refusal was silent")
	}
}

// TestSupervisorWorkingFollowsTheAgentStatus pins where the spinner's state
// comes from. Not from the tail: events.ndjson carries lines that are not
// sdk-events at all, so "the last event is not a result" is a guess that can
// never go false again — and a spinner that never stops keeps the whole
// dashboard redrawing at spinner cadence for the rest of the session, which
// is exactly what model.go's own tick chain is written to avoid.
func TestSupervisorWorkingFollowsTheAgentStatus(t *testing.T) {
	h := &fakeHost{t: t}
	m := newTestModelWithHost(&fakeData{snap: testSnapshot()}, &recordingActor{}, h)
	m = stepPump(t, m, "a")
	tb := m.focusedTab()
	if tb == nil || tb.sup == nil {
		t.Fatal("no supervisor tab")
	}

	// A tail whose last line is an assistant turn, and no agent status
	// saying anything is running.
	mm, _ := m.Update(supervisorEventsMsg{id: tb.id, lines: supervisorLines()[:1]})
	m = mm.(Model)
	if tb.sup.working {
		t.Error("the supervisor is working with nothing reporting that it is")
	}

	mm, _ = m.Update(liveMsg{states: map[sandboxKey]liveState{
		{Project: "alpha", Name: "mercury"}: {
			Agent: control.AgentStatus{Reachable: true, State: "working"}},
	}})
	m = mm.(Model)
	// tb is the same *tab pointer m.tabs holds, so its sup field's mutation
	// by Update is visible here whether or not the returned Model is kept —
	// there is nothing left to do with it after this, the test's last call.
	_, cmd := m.Update(supervisorEventsMsg{id: tb.id, lines: supervisorLines()[:1]})
	if !tb.sup.working {
		t.Error("the supervisor is idle while the fast ticker reports the agent working")
	}
	if cmd == nil {
		t.Error("this spinner's own tick chain never started")
	}
}
