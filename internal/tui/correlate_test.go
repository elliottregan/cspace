package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

func TestCorrelateGroupsSortsAndNests(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1", MemoryB: 1 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "cspace-alpha-mercury-convex", State: "running", IP: "10.0.0.2", MemoryB: 1 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "cspace-alpha-browser", State: "running", IP: "10.0.0.3", MemoryB: 4 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "buildkit", State: "running", IP: "10.0.0.9", MemoryB: 2 << 30, Started: now.Add(-3 * time.Hour)},
	}
	entries := []registry.Entry{
		{Project: "alpha", Name: "mercury", ControlURL: "http://c/mercury", Token: "tok", State: "ready"},
	}
	statuses := map[string]AgentStatus{
		"cspace-alpha-mercury": {Reachable: true, State: "idle", Session: "primary"},
	}
	browserHealth := map[string]BrowserHealth{
		"cspace-alpha-browser": {Reachable: true, Version: "Chrome/140"},
	}
	snap := Correlate(now, containers, entries, statuses, browserHealth, DaemonHealth{Reachable: true, Version: "1.0"}, nil)

	// Expected row order: project header, sandbox, its sidecar, browser, system(buildkit)
	wantKinds := []RowKind{RowProject, RowSandbox, RowSidecar, RowBrowser, RowSystem}
	if len(snap.Rows) != len(wantKinds) {
		t.Fatalf("rows = %d, want %d: %+v", len(snap.Rows), len(wantKinds), snap.Rows)
	}
	for i, k := range wantKinds {
		if snap.Rows[i].Kind != k {
			t.Errorf("row[%d].Kind = %v, want %v", i, snap.Rows[i].Kind, k)
		}
	}
	sb := snap.Rows[1]
	if sb.State != StateRunning || !sb.Agent.Reachable || sb.Agent.State != "idle" {
		t.Errorf("sandbox row = %+v", sb)
	}
	if sb.Uptime != 2*time.Hour {
		t.Errorf("uptime = %v, want 2h", sb.Uptime)
	}
	if !snap.Rows[1].Selectable || !snap.Rows[3].Selectable {
		t.Error("sandbox and browser rows must be selectable")
	}
	if snap.Rows[0].Selectable || snap.Rows[2].Selectable || snap.Rows[4].Selectable {
		t.Error("project/sidecar/system rows must not be selectable")
	}
	// Token must travel in the row model so actions can use it (never rendered).
	if !containsToken(snap) {
		t.Error("sandbox Token must be carried in the row model for actions")
	}
	// The browser row carries its last-known health probe result.
	if br := snap.Rows[3]; !br.Browser.Reachable || br.Browser.Version != "Chrome/140" {
		t.Errorf("browser row health = %+v, want reachable Chrome/140", br.Browser)
	}
}

func containsToken(s Snapshot) bool {
	for _, r := range s.Rows {
		if r.Token == "tok" {
			return true
		}
	}
	return false
}

func TestCorrelateDegradedWhenSupervisorUnreachable(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", Started: now},
	}
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "ready"}}
	// no status for the sandbox => unreachable
	snap := Correlate(now, containers, entries, map[string]AgentStatus{}, nil, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateDegraded {
		t.Errorf("state = %v, want StateDegraded", snap.Rows[1].State)
	}
}

func TestCorrelateStoppedWhenNoContainer(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "ready"}}
	snap := Correlate(now, nil, entries, map[string]AgentStatus{}, nil, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateStopped {
		t.Errorf("state = %v, want StateStopped", snap.Rows[1].State)
	}
}

func TestCorrelateBootingFromRegistryState(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", Started: now},
	}
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "starting"}}
	snap := Correlate(now, containers, entries, map[string]AgentStatus{}, nil, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateBooting {
		t.Errorf("state = %v, want StateBooting", snap.Rows[1].State)
	}
}

func TestCorrelateCarriesListErr(t *testing.T) {
	e := errors.New("apiserver down")
	snap := Correlate(time.Unix(0, 0), nil, nil, map[string]AgentStatus{}, nil, DaemonHealth{}, e)
	if snap.Err == nil || snap.Err.Error() != "apiserver down" {
		t.Errorf("Err = %v, want carried", snap.Err)
	}
}
