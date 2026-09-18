package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// execCall is one recorded Exec, so a test can assert both the container it
// targeted and the argv it ran.
type execCall struct {
	name string
	cmd  []string
}

// fakeContainers is the ContainerCLI seam: canned results so control's tests
// never shell out to the real `container` CLI.
type fakeContainers struct {
	out      []applecontainer.ContainerSummary
	err      error
	stats    []applecontainer.ContainerStats
	statsErr error
	// statsCalls counts Stats calls. Snapshot samples stats on its own
	// goroutine but waits for it before returning, so a plain int read
	// after Snapshot returns is properly ordered.
	statsCalls int

	execOut    string
	execStderr string
	execExit   int
	execErr    error
	execCalls  []execCall
}

func (f *fakeContainers) List(context.Context) ([]applecontainer.ContainerSummary, error) {
	return f.out, f.err
}

func (f *fakeContainers) Stats(context.Context) ([]applecontainer.ContainerStats, error) {
	f.statsCalls++
	return f.stats, f.statsErr
}

// execCalls is appended to without a lock: nothing in these tests calls Exec
// concurrently, so it stays unsynchronized on purpose.
func (f *fakeContainers) Exec(_ context.Context, name string, cmd []string, _ substrate.ExecOpts) (substrate.ExecResult, error) {
	f.execCalls = append(f.execCalls, execCall{name: name, cmd: cmd})
	return substrate.ExecResult{Stdout: f.execOut, Stderr: f.execStderr, ExitCode: f.execExit}, f.execErr
}

func writeRegistry(t *testing.T, project, name, controlURL, token string) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reg.json")
	r := &registry.Registry{Path: path}
	if err := r.Register(registry.Entry{
		Project: project, Name: name, ControlURL: controlURL, Token: token, State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func TestSnapshotFansOutStatusAndCorrelates(t *testing.T) {
	var gotAuth string
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "session": "primary", "state": "idle", "queueDepth": 0,
		})
	}))
	defer control.Close()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": "1.0.0-rc.40"})
	}))
	defer daemon.Close()

	reg := writeRegistry(t, "alpha", "mercury", control.URL, "tok-xyz")
	c := New(Options{
		Containers: &fakeContainers{out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
		}},
		Entries:   reg,
		DaemonURL: daemon.URL,
		Now:       func() time.Time { return time.Unix(1_000_000, 0) },
	})

	snap := c.Snapshot(context.Background())

	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("status Authorization = %q, want Bearer tok-xyz", gotAuth)
	}
	if !snap.Daemon.Reachable || snap.Daemon.Version != "1.0.0-rc.40" {
		t.Errorf("daemon = %+v", snap.Daemon)
	}
	// project header + sandbox row
	if len(snap.Rows) != 2 || snap.Rows[1].State != StateRunning || !snap.Rows[1].Agent.Reachable {
		t.Fatalf("rows = %+v", snap.Rows)
	}
}

func TestSnapshotListErrorCarriedAndDaemonUnreachable(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	c := New(Options{
		Containers: &fakeContainers{err: os.ErrPermission},
		Entries:    reg,
		DaemonURL:  "http://127.0.0.1:1", // unreachable daemon
		Now:        func() time.Time { return time.Unix(0, 0) },
	})
	snap := c.Snapshot(context.Background())
	if snap.Err == nil {
		t.Error("want Err carried from the container lister's failure")
	}
	if snap.Daemon.Reachable {
		t.Error("daemon should be unreachable")
	}
}

// A Client built with no ContainerCLI must fail closed rather than nil-panic
// on c.containers.List.
func TestSnapshotErrorsWithoutContainerCLI(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	takenAt := time.Unix(42, 0)
	c := New(Options{Entries: reg, Now: func() time.Time { return takenAt }})
	snap := c.Snapshot(context.Background())
	if !errors.Is(snap.Err, ErrNoContainerCLI) {
		t.Errorf("Err = %v, want ErrNoContainerCLI", snap.Err)
	}
	if !snap.TakenAt.Equal(takenAt) {
		t.Errorf("TakenAt = %v, want %v", snap.TakenAt, takenAt)
	}
	if snap.Rows != nil {
		t.Errorf("Rows = %+v, want none", snap.Rows)
	}
}

// A Client built with no EntryStore must fail closed rather than nil-panic
// on c.entries.List.
func TestSnapshotErrorsWithoutEntryStore(t *testing.T) {
	takenAt := time.Unix(42, 0)
	c := New(Options{Containers: &fakeContainers{}, Now: func() time.Time { return takenAt }})
	snap := c.Snapshot(context.Background())
	if !errors.Is(snap.Err, ErrNoEntryStore) {
		t.Errorf("Err = %v, want ErrNoEntryStore", snap.Err)
	}
	if !snap.TakenAt.Equal(takenAt) {
		t.Errorf("TakenAt = %v, want %v", snap.TakenAt, takenAt)
	}
}

func TestSnapshotProbesBrowserHealth(t *testing.T) {
	// A CDP /json/version stub standing in for the browser sidecar.
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/json/version" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Browser": "Chrome/140.0"})
	}))
	defer cdp.Close()

	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	c := New(Options{
		Containers: &fakeContainers{},
		Entries:    reg,
		DaemonURL:  "http://127.0.0.1:1",
		Now:        func() time.Time { return time.Unix(0, 0) },
	})
	// Redirect the CDP probe at the stub (production uses the fixed :9222 port,
	// which httptest can't bind — the browserCDPURL seam exists for exactly this).
	c.browserCDPURL = func(ip string) string { return cdp.URL + "/json/version" }

	// A running "-browser" container is probed and mapped by container name;
	// a non-browser container and a stopped browser are skipped.
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-browser", State: "running", IP: "10.0.0.9"},
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
		{Name: "cspace-beta-browser", State: "stopped", IP: ""},
	}
	m := c.fetchBrowserHealth(context.Background(), containers)
	if got := m["cspace-alpha-browser"]; !got.Reachable || got.Version != "Chrome/140.0" {
		t.Errorf("browser health = %+v, want reachable Chrome/140.0", got)
	}
	if _, ok := m["cspace-alpha-mercury"]; ok {
		t.Error("non-browser container should not be probed")
	}
	if _, ok := m["cspace-beta-browser"]; ok {
		t.Error("stopped browser should not be probed")
	}
}

// The medium ticker polls twice a second-and-a-half; `container stats` costs
// ~2s. SkipStats is what keeps that cadence affordable, so it must actually
// skip the call, not just discard its result.
func TestSnapshotWithSkipStatsDoesNotSampleStats(t *testing.T) {
	cli := &fakeContainers{
		out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "192.168.64.5", MemoryB: 16 << 30},
		},
		stats: []applecontainer.ContainerStats{
			{Name: "cspace-alpha-mercury", MemoryUsedB: 1 << 30},
		},
	}
	reg := writeRegistry(t, "alpha", "mercury", "", "")
	c := New(Options{Containers: cli, Entries: reg, Now: func() time.Time { return time.Unix(1_000_000, 0) }})

	snap := c.SnapshotWith(context.Background(), SnapshotOpts{SkipStats: true})
	if cli.statsCalls != 0 {
		t.Errorf("Stats called %d times, want 0", cli.statsCalls)
	}
	var found bool
	for _, r := range snap.Rows {
		if r.Kind == RowSandbox && r.Name == "mercury" {
			found = true
			if r.MemoryUsedB != 0 {
				t.Errorf("MemoryUsedB = %d, want 0 with no sample", r.MemoryUsedB)
			}
			if r.MemoryB != 16<<30 {
				t.Errorf("MemoryB = %d, want the cap to survive", r.MemoryB)
			}
		}
	}
	if !found {
		t.Fatalf("no mercury row in %+v", snap.Rows)
	}
}

// The default is unchanged: Snapshot still samples, and the sample still
// lands on the row, because the slow ticker and every existing caller
// depend on it.
func TestSnapshotSamplesStatsByDefault(t *testing.T) {
	cli := &fakeContainers{
		out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "192.168.64.5", MemoryB: 16 << 30},
		},
		stats: []applecontainer.ContainerStats{
			{Name: "cspace-alpha-mercury", MemoryUsedB: 1 << 30},
		},
	}
	reg := writeRegistry(t, "alpha", "mercury", "", "")
	c := New(Options{Containers: cli, Entries: reg, Now: func() time.Time { return time.Unix(1_000_000, 0) }})

	snap := c.Snapshot(context.Background())
	if cli.statsCalls != 1 {
		t.Errorf("Stats called %d times, want 1", cli.statsCalls)
	}
	for _, r := range snap.Rows {
		if r.Kind == RowSandbox && r.Name == "mercury" && r.MemoryUsedB != 1<<30 {
			t.Errorf("MemoryUsedB = %d, want the sample", r.MemoryUsedB)
		}
	}
}

// The dashboard's attach must probe for tmux and hand BeginAttach the same
// driver the Client uses, so the memoized presence probe and the exec
// transport are shared rather than duplicated.
func TestTmuxReturnsTheClientsDriver(t *testing.T) {
	tm := NewTmux()
	c := New(Options{Containers: &fakeContainers{}, Tmux: tm})
	if c.Tmux() != tm {
		t.Error("Tmux() should hand back the injected driver")
	}
	if New(Options{Containers: &fakeContainers{}}).Tmux() == nil {
		t.Error("Tmux() should never be nil: New always builds one")
	}
}
