package controlplane

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInitRunsTheStartupSweep(t *testing.T) {
	h := &fakeHost{t: t}
	m := New(&fakeData{snap: testSnapshot()}, &recordingActor{}, h, NewKeyMap(nil))
	for _, msg := range drain(m.Init()) {
		if _, ok := msg.(sweepMsg); ok {
			if h.sweeps != 1 {
				t.Errorf("sweeps = %d, want 1", h.sweeps)
			}
			return
		}
	}
	t.Fatal("Init never swept")
}

func TestQuitClosesEveryPaneBeforeItQuits(t *testing.T) {
	h := &fakeHost{t: t}
	m := openOne(t, h)
	d := m.tabs[0].detach.(*fakeDetacher)
	m.focus = focusSidebar

	mm, cmd := m.Update(press("q"))
	m = mm.(Model)
	if !m.quitting {
		t.Fatal("q did not quit")
	}
	if cmd == nil {
		t.Fatal("quit produced no command; the panes were never closed")
	}
	// The quit command closes every pane and then yields QuitMsg, so running
	// it is what performs the detach — and the message proves the program
	// still ends afterwards.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("the quit command did not end the program")
	}
	if d.closed != 1 {
		t.Errorf("detacher closed %d times on quit, want 1 — a quit that skipped the detach leaves a client attached and a record behind", d.closed)
	}
}
