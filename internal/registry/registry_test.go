package registry

import (
	"fmt"
	"path/filepath"
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

func TestFreePort(t *testing.T) {
	p, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if p < 1024 || p > 65535 {
		t.Fatalf("expected ephemeral port, got %d", p)
	}
}
