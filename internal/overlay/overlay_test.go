package overlay

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/planets"
)

func newTestModel() Model {
	return NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: make(chan ProvisionEvent, 4),
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
}

func TestViewShowsInstanceName(t *testing.T) {
	m := newTestModel()
	m.phase = "Validating"
	m.phaseNum = 1
	out := m.View()
	if !strings.Contains(out, "mercury") {
		t.Error("expected instance name in view")
	}
}

func TestViewShowsPhaseName(t *testing.T) {
	m := newTestModel()
	m.phase = "Installing plugins"
	m.phaseNum = 14
	out := m.View()
	if !strings.Contains(out, "Installing plugins") {
		t.Error("expected phase name in view")
	}
}

func TestViewShowsElapsed(t *testing.T) {
	calls := 0
	m := NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: make(chan ProvisionEvent, 4),
		Now: func() time.Time {
			defer func() { calls++ }()
			if calls == 0 {
				return time.Unix(0, 0) // seeds start at NewModel construction
			}
			return time.Unix(107, 0) // every subsequent call (including View)
		},
	})
	m.phase = "Validating"
	m.phaseNum = 1
	out := m.View()
	if !strings.Contains(out, "01:47") {
		t.Errorf("expected elapsed 01:47 in view, got:\n%s", out)
	}
}

func TestViewErrorPanel(t *testing.T) {
	m := newTestModel()
	m.err = errors.New("compose up failed: exit 1")
	m.errPhase = "Starting containers"
	out := m.View()
	for _, want := range []string{
		"Provisioning failed",
		"Starting containers",
		"--verbose",
		"Press any key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("error panel missing %q\nfull view:\n%s", want, out)
		}
	}
}
