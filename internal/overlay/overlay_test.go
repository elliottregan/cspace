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
	if !strings.Contains(out, "MERCURY") {
		t.Error("expected instance name (upper-cased) in view")
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

func TestViewShowsPortUplinks(t *testing.T) {
	events := make(chan ProvisionEvent, 4)
	m := NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: events,
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
	updated, _ := m.Update(ProvisionEvent{Kind: PortEvent, Label: "app", URL: "http://localhost:30001"})
	m = updated.(Model)
	updated, _ = m.Update(ProvisionEvent{Kind: PortEvent, Label: "storybook", URL: "http://localhost:30002"})
	m = updated.(Model)
	m.phase = "Installing plugins"
	m.phaseNum = 14
	out := m.View()
	for _, want := range []string{"UPLINKS", "app", ":30001", "storybook", ":30002"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in view\nfull view:\n%s", want, out)
		}
	}
}

func TestViewShowsRecentLogTail(t *testing.T) {
	m := newTestModel()
	m.phase = "Installing plugins"
	m.phaseNum = 14
	m.logs = []string{"superpowers", "github", "chrome-devtools"}
	out := m.View()
	for _, want := range []string{"superpowers", "github", "chrome-devtools"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected log entry %q in view\nfull view:\n%s", want, out)
		}
	}
}

func TestLogEventTrimsToTail(t *testing.T) {
	events := make(chan ProvisionEvent, 8)
	m := NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: events,
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
	for _, name := range []string{"one", "two", "three", "four", "five"} {
		updated, _ := m.Update(ProvisionEvent{Kind: LogEvent, Message: name})
		m = updated.(Model)
	}
	if got := len(m.logs); got != 3 {
		t.Fatalf("log tail length: got %d, want 3", got)
	}
	want := []string{"three", "four", "five"}
	for i, v := range want {
		if m.logs[i] != v {
			t.Errorf("m.logs[%d] = %q, want %q", i, m.logs[i], v)
		}
	}
}

func TestPhaseEventClearsLogs(t *testing.T) {
	events := make(chan ProvisionEvent, 4)
	m := NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: events,
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
	updated, _ := m.Update(ProvisionEvent{Kind: LogEvent, Message: "leftover"})
	m = updated.(Model)
	updated, _ = m.Update(ProvisionEvent{Kind: PhaseEvent, Phase: "Installing plugins", Num: 14, Total: 14})
	m = updated.(Model)
	if len(m.logs) != 0 {
		t.Errorf("expected logs cleared on PhaseEvent, got %v", m.logs)
	}
}

func TestProgressBarReflectsCompletedPhases(t *testing.T) {
	m := newTestModel()
	// Entering phase 14 of 14 means 13 are complete → progress bar must
	// read below 100 %, not at 100 %.
	m.phase = "Installing plugins"
	m.phaseNum = 14
	out := m.View()
	if strings.Contains(out, "100.0%") {
		t.Errorf("progress bar should not be 100%% while phase is running\nfull view:\n%s", out)
	}
	if !strings.Contains(out, "92.9%") {
		t.Errorf("expected progress 92.9%% (13/14) while running phase 14, got:\n%s", out)
	}
}

func TestViewErrorPanel(t *testing.T) {
	m := newTestModel()
	m.err = errors.New("compose up failed: exit 1")
	m.errPhase = "Starting containers"
	out := m.View()
	for _, want := range []string{
		"MISSION ABORT",
		"Starting containers",
		"--verbose",
		"DISENGAGE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("error panel missing %q\nfull view:\n%s", want, out)
		}
	}
}
