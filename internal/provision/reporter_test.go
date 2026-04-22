package provision

import "testing"

type captured struct {
	kind  string // "phase", "warn", "done", "error"
	name  string
	num   int
	total int
	err   error
}

type recordingReporter struct{ events []captured }

func (r *recordingReporter) Phase(name string, num, total int) {
	r.events = append(r.events, captured{kind: "phase", name: name, num: num, total: total})
}
func (r *recordingReporter) Log(msg string) {
	r.events = append(r.events, captured{kind: "log", name: msg})
}
func (r *recordingReporter) Port(label, url string) {
	r.events = append(r.events, captured{kind: "port", name: label + " " + url})
}
func (r *recordingReporter) Warn(msg string) {
	r.events = append(r.events, captured{kind: "warn", name: msg})
}
func (r *recordingReporter) Done() {
	r.events = append(r.events, captured{kind: "done"})
}
func (r *recordingReporter) Error(phase string, err error) {
	r.events = append(r.events, captured{kind: "error", name: phase, err: err})
}

func TestReporterInterfaceImplementations(t *testing.T) {
	// Compile-time assertion: both reporter types implement Reporter.
	var _ Reporter = (*recordingReporter)(nil)
	var _ Reporter = logReporter{}
}

func TestPhasesReference(t *testing.T) {
	if len(Phases) != 17 {
		t.Errorf("Phases: got %d entries, want 17", len(Phases))
	}
	if Phases[0] != "Validating name" {
		t.Errorf("Phases[0]: got %q", Phases[0])
	}
	if Phases[5] != "Starting search stack" {
		t.Errorf("Phases[5]: got %q", Phases[5])
	}
	if Phases[14] != "Installing plugins" {
		t.Errorf("Phases[14]: got %q", Phases[14])
	}
	if Phases[15] != "Syncing workspace" {
		t.Errorf("Phases[15]: got %q", Phases[15])
	}
	if Phases[16] != "Bootstrapping search" {
		t.Errorf("Phases[16]: got %q", Phases[16])
	}
}
