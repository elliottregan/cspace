package registry

import (
	"path/filepath"
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

func TestFreePort(t *testing.T) {
	p, err := FreePort()
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if p < 1024 || p > 65535 {
		t.Fatalf("expected ephemeral port, got %d", p)
	}
}
