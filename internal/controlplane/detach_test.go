package controlplane

import (
	"testing"
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
