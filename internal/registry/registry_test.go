package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegisterAndLookup(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	entry := Entry{
		Project:    "myproj",
		Name:       "test1",
		ControlURL: "http://127.0.0.1:16201",
		Token:      "tok123",
		IP:         "192.168.64.5",
		StartedAt:  time.Now().UTC(),
	}
	if err := r.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("myproj", "test1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ControlURL != entry.ControlURL || got.Token != entry.Token {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}

func TestLookupMissing(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	if _, err := r.Lookup("none", "none"); err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestUnregister(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	_ = r.Register(Entry{Project: "p", Name: "n", ControlURL: "http://x", StartedAt: time.Now()})
	if err := r.Unregister("p", "n"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := r.Lookup("p", "n"); err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	_ = r.Register(Entry{Project: "p", Name: "a", ControlURL: "http://a", StartedAt: time.Now()})
	_ = r.Register(Entry{Project: "p", Name: "b", ControlURL: "http://b", StartedAt: time.Now()})

	entries, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

func TestRegisterAndLookupBrowserContainer(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	entry := Entry{
		Project:          "myproj",
		Name:             "withbrowser",
		ControlURL:       "http://127.0.0.1:16201",
		Token:            "tok456",
		IP:               "192.168.64.7",
		StartedAt:        time.Now().UTC(),
		BrowserContainer: "cspace-myproj-withbrowser-browser",
	}
	if err := r.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("myproj", "withbrowser")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.BrowserContainer != entry.BrowserContainer {
		t.Fatalf("BrowserContainer round-trip: got %q, want %q",
			got.BrowserContainer, entry.BrowserContainer)
	}

	// Also confirm an entry registered without BrowserContainer round-trips
	// as the empty string (omitempty doesn't accidentally produce "null" or
	// drop the key in a way that breaks downstream reads).
	plain := Entry{
		Project:    "myproj",
		Name:       "nobrowser",
		ControlURL: "http://127.0.0.1:16202",
		StartedAt:  time.Now().UTC(),
	}
	if err := r.Register(plain); err != nil {
		t.Fatalf("Register plain: %v", err)
	}
	gotPlain, err := r.Lookup("myproj", "nobrowser")
	if err != nil {
		t.Fatalf("Lookup plain: %v", err)
	}
	if gotPlain.BrowserContainer != "" {
		t.Fatalf("BrowserContainer for plain entry: got %q, want empty",
			gotPlain.BrowserContainer)
	}
}

func TestConcurrentRegister(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			entry := Entry{
				Project:    "p",
				Name:       fmt.Sprintf("sandbox-%d", i),
				ControlURL: fmt.Sprintf("http://x:%d", 6000+i),
				StartedAt:  time.Now(),
			}
			if err := r.Register(entry); err != nil {
				t.Errorf("Register %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	entries, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != N {
		t.Fatalf("expected %d entries after concurrent register, got %d", N, len(entries))
	}
}

func TestEntryStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}
	if err := r.Register(Entry{
		Project: "p", Name: "n",
		ControlURL: "http://x", StartedAt: time.Now(),
		State: "starting",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	e, err := r.Lookup("p", "n")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.State != "starting" {
		t.Fatalf("State after Register: got %q, want %q", e.State, "starting")
	}
	if err := r.MarkReady("p", "n"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	e, err = r.Lookup("p", "n")
	if err != nil {
		t.Fatalf("Lookup after MarkReady: %v", err)
	}
	if e.State != "ready" {
		t.Fatalf("State after MarkReady: got %q, want %q", e.State, "ready")
	}
	// Other fields should be preserved through MarkReady.
	if e.ControlURL != "http://x" {
		t.Fatalf("ControlURL not preserved through MarkReady: got %q", e.ControlURL)
	}
}

func TestMarkReadyOnMissingIsNoOp(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}
	if err := r.MarkReady("missing", "missing"); err != nil {
		t.Fatalf("MarkReady on missing should be no-op, got: %v", err)
	}
}

func TestMarkStopped(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "registry.json")}
	if err := r.Register(Entry{Project: "demo", Name: "mercury", State: "starting"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.MarkStopped("demo", "mercury"); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	e, err := r.Lookup("demo", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.State != "stopped" {
		t.Errorf("state = %q, want stopped", e.State)
	}
	// A missing entry is not an error: a racing `down` may have removed it.
	if err := r.MarkStopped("demo", "gone"); err != nil {
		t.Errorf("MarkStopped on a missing entry = %v, want nil", err)
	}
}

func TestFreePort(t *testing.T) {
	p, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if p < 1024 || p > 65535 {
		t.Fatalf("expected ephemeral port, got %d", p)
	}
}

func TestEntryRoundTripsTheProjectRoot(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "sandbox-registry.json")}
	if err := r.Register(Entry{
		Project:     "myproj",
		Name:        "mercury",
		ControlURL:  "http://192.168.64.5:6201",
		ProjectRoot: "/Users/x/code/myproj",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("myproj", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ProjectRoot != "/Users/x/code/myproj" {
		t.Errorf("ProjectRoot = %q, want /Users/x/code/myproj", got.ProjectRoot)
	}

	// The on-disk key is snake_case like every other field; the daemon
	// serves this file over HTTP, so the name is part of the wire format.
	data, err := os.ReadFile(r.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"project_root": "/Users/x/code/myproj"`) {
		t.Errorf("registry file missing project_root:\n%s", data)
	}
}

// An entry written before this field existed must still load, with an empty
// root rather than an error — control.Up falls back for exactly that case.
func TestLegacyEntryWithoutAProjectRootLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox-registry.json")
	legacy := `{"myproj:mercury":{"control_url":"http://192.168.64.5:6201","started_at":"2026-09-18T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := &Registry{Path: path}
	got, err := r.Lookup("myproj", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ProjectRoot != "" {
		t.Errorf("ProjectRoot = %q, want empty for a legacy entry", got.ProjectRoot)
	}
}

func TestCountForProject(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	for _, e := range []Entry{
		{Project: "alpha", Name: "mercury", IP: "10.0.0.1"},
		{Project: "alpha", Name: "venus", IP: "10.0.0.2"},
		{Project: "beta", Name: "mercury", IP: "10.0.0.3"},
	} {
		if err := r.Register(e); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	for proj, want := range map[string]int{"alpha": 2, "beta": 1, "gamma": 0} {
		got, err := r.CountForProject(proj)
		if err != nil {
			t.Fatalf("count %s: %v", proj, err)
		}
		if got != want {
			t.Errorf("CountForProject(%q) = %d, want %d", proj, got, want)
		}
	}
}

// TestMarkStoppedClearsTheAddress — a kept entry describes a container that
// no longer exists, so the two fields that address one must not survive it.
// The IP is the load-bearing one: the daemon's DNS handler skips entries
// with no IP, and a kept entry that held on to its old vmnet address would
// keep answering <name>.<project>.cspace.test with an address the substrate
// is free to have handed to a different container.
func TestMarkStoppedClearsTheAddress(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "registry.json")}
	if err := r.Register(Entry{
		Project:     "demo",
		Name:        "mercury",
		State:       StateReady,
		IP:          "192.168.64.7",
		ControlURL:  "http://192.168.64.7:6201",
		Token:       "tok",
		ProjectRoot: "/Users/someone/demo",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.MarkStopped("demo", "mercury"); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	e, err := r.Lookup("demo", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.State != StateStopped {
		t.Errorf("state = %q, want %q", e.State, StateStopped)
	}
	if e.IP != "" {
		t.Errorf("IP = %q, want it cleared", e.IP)
	}
	if e.ControlURL != "" {
		t.Errorf("ControlURL = %q, want it cleared", e.ControlURL)
	}
	// ...and everything a stopped row still needs survives: the dashboard
	// renders it from Project/Name/State, and the boot action it offers
	// needs the project root.
	if e.ProjectRoot != "/Users/someone/demo" {
		t.Errorf("ProjectRoot = %q, want it preserved for the boot action", e.ProjectRoot)
	}
}
