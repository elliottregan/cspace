package contextstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Root: dir, Now: func() time.Time { return time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC) }}
}

func TestLogDecisionWritesFileAndSeeds(t *testing.T) {
	s := newStore(t)

	path, err := s.LogDecision(LogDecisionInput{
		Title:        "Use Go MCP SDK",
		Context:      "needed a server",
		Alternatives: "Node SDK",
		Decision:     "Go SDK",
		Consequences: "Go module dep",
	})
	if err != nil {
		t.Fatalf("LogDecision: %v", err)
	}

	want := filepath.Join(s.Root, ".cspace/context/decisions/2026-04-13-use-go-mcp-sdk.md")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "kind: decision") {
		t.Errorf("missing kind: %s", body)
	}

	// Seeding side effect: direction/principles/roadmap created.
	for _, name := range []string{"direction.md", "principles.md", "roadmap.md"} {
		p := filepath.Join(s.Root, ".cspace/context", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("seed missing: %v", err)
		}
	}
}

func TestLogDiscoveryAddsCollisionSuffix(t *testing.T) {
	s := newStore(t)

	first, err := s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "x", Impact: "y"})
	if err != nil {
		t.Fatalf("LogDiscovery 1: %v", err)
	}
	second, err := s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "x", Impact: "y"})
	if err != nil {
		t.Fatalf("LogDiscovery 2: %v", err)
	}
	if filepath.Base(first) != "2026-04-13-firewall.md" {
		t.Errorf("first = %s", first)
	}
	if filepath.Base(second) != "2026-04-13-firewall-2.md" {
		t.Errorf("second = %s", second)
	}
}

func TestSeedOnlyRunsOnce(t *testing.T) {
	s := newStore(t)

	if _, err := s.LogDiscovery(LogDiscoveryInput{Title: "T", Finding: "f", Impact: "i"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root, ".cspace/context/direction.md")
	if err := os.WriteFile(dir, []byte("custom direction"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.LogDiscovery(LogDiscoveryInput{Title: "U", Finding: "f", Impact: "i"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dir)
	if string(got) != "custom direction" {
		t.Errorf("direction.md was overwritten: %q", got)
	}
}

func TestListEntriesFiltersKindAndDate(t *testing.T) {
	s := newStore(t)

	s.Now = func() time.Time { return time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC) }
	_, _ = s.LogDecision(LogDecisionInput{Title: "old", Context: "c", Decision: "d"})

	s.Now = func() time.Time { return time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC) }
	_, _ = s.LogDecision(LogDecisionInput{Title: "new decision", Decision: "d"})
	_, _ = s.LogDiscovery(LogDiscoveryInput{Title: "new discovery", Finding: "f"})

	all, err := s.ListEntries(ListOptions{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListEntries got %d, want 3", len(all))
	}

	decisions, _ := s.ListEntries(ListOptions{Kind: KindDecision})
	if len(decisions) != 2 {
		t.Errorf("decisions got %d, want 2", len(decisions))
	}

	since := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	recent, _ := s.ListEntries(ListOptions{Since: &since})
	if len(recent) != 2 {
		t.Errorf("recent got %d, want 2 (new decision + new discovery)", len(recent))
	}
}

func TestReadEntriesReturnsBodies(t *testing.T) {
	s := newStore(t)
	_, _ = s.LogDiscovery(LogDiscoveryInput{Title: "Firewall", Finding: "blocked", Impact: "allowlist"})

	entries, err := s.ReadEntries(ListOptions{Kind: KindDiscovery})
	if err != nil {
		t.Fatalf("ReadEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d", len(entries))
	}
	if entries[0].Sections["Finding"] != "blocked" {
		t.Errorf("Finding = %q", entries[0].Sections["Finding"])
	}
}

func TestReadHumanFilesReturnsSeedsWhenPresent(t *testing.T) {
	s := newStore(t)
	if err := s.ensureSeeded(); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadHuman("direction")
	if err != nil {
		t.Fatalf("ReadHuman: %v", err)
	}
	if !strings.Contains(got, "# Direction") {
		t.Errorf("unexpected content: %q", got)
	}

	if _, err := s.ReadHuman("nonexistent"); err == nil {
		t.Error("expected error for unknown section")
	}
}

func TestRemoveEntry(t *testing.T) {
	s := newStore(t)
	path, _ := s.LogDiscovery(LogDiscoveryInput{Title: "Zap", Finding: "f", Impact: "i"})

	if err := s.RemoveEntry(KindDiscovery, "2026-04-13-zap"); err != nil {
		t.Fatalf("RemoveEntry: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}

	if err := s.RemoveEntry(KindDiscovery, "does-not-exist"); err == nil {
		t.Error("expected error for missing slug")
	}
}

func TestRemoveEntryRejectsInvalidSlugs(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.ContextDir(), "discoveries"), 0755); err != nil {
		t.Fatal(err)
	}

	invalidSlugs := []string{
		"../../outside/secret",
		"../decisions/foo",
		"foo/bar",
		"foo\\bar",
		".",
		"..",
		"",
		"FOO",     // uppercase — not produced by Slugify
		"foo.bar", // inner "." is not allowed
	}
	for _, bad := range invalidSlugs {
		err := s.RemoveEntry(KindDiscovery, bad)
		if err == nil {
			t.Errorf("RemoveEntry(%q): expected error, got nil", bad)
			continue
		}
		// Validation must happen before the filesystem stat — "entry not found"
		// implies we attempted a lookup with the unsafe input.
		if strings.Contains(err.Error(), "entry not found") {
			t.Errorf("RemoveEntry(%q): got %q, want invalid-slug error (validation must precede stat)", bad, err)
		}
	}
}

func TestRemoveEntryCannotReachSiblingKind(t *testing.T) {
	s := newStore(t)

	// Plant a decision file.
	decisionPath, err := s.LogDecision(LogDecisionInput{Title: "Keep me", Context: "c", Decision: "d"})
	if err != nil {
		t.Fatal(err)
	}

	// Path traversal from discoveries/ to decisions/ should be rejected, not
	// silently normalized into a successful delete of a sibling-kind file.
	_ = s.RemoveEntry(KindDiscovery, "../decisions/2026-04-13-keep-me")

	if _, err := os.Stat(decisionPath); err != nil {
		t.Errorf("decision file was deleted via cross-kind traversal: %v", err)
	}
}

func TestRemoveEntryDistinguishesNotFoundFromOtherErrors(t *testing.T) {
	s := newStore(t)

	// Make .cspace/context/decisions a regular file so any stat on a child path
	// returns ENOTDIR (a non-NotExist error).
	if err := os.MkdirAll(s.ContextDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.ContextDir(), "decisions"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	err := s.RemoveEntry(KindDecision, "2026-04-13-anything")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "entry not found") {
		t.Errorf("non-NotExist error masked as 'entry not found': %v", err)
	}
}
