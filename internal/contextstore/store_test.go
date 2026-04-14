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

	want := filepath.Join(s.Root, "docs/context/decisions/2026-04-13-use-go-mcp-sdk.md")
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
		p := filepath.Join(s.Root, "docs/context", name)
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
	dir := filepath.Join(s.Root, "docs/context/direction.md")
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
