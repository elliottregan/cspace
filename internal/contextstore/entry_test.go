package contextstore

import (
	"strings"
	"testing"
	"time"
)

func TestRenderDecision(t *testing.T) {
	e := Entry{
		Kind:  KindDecision,
		Title: "Use Go MCP SDK",
		Date:  time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Sections: map[string]string{
			"Context":      "needed a server",
			"Alternatives": "Node SDK",
			"Decision":     "Go SDK",
			"Consequences": "Go module dep",
		},
	}
	got := e.Render()
	want := "---\ntitle: Use Go MCP SDK\ndate: 2026-04-13\nkind: decision\n---\n\n" +
		"## Context\nneeded a server\n\n" +
		"## Alternatives\nNode SDK\n\n" +
		"## Decision\nGo SDK\n\n" +
		"## Consequences\nGo module dep\n"
	if got != want {
		t.Errorf("Render mismatch:\n--got--\n%s\n--want--\n%s", got, want)
	}
}

func TestRenderDiscovery(t *testing.T) {
	e := Entry{
		Kind:  KindDiscovery,
		Title: "Firewall blocks foo",
		Date:  time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Sections: map[string]string{
			"Finding": "port blocked",
			"Impact":  "must allowlist",
		},
	}
	got := e.Render()
	if !strings.Contains(got, "kind: discovery") {
		t.Errorf("missing discovery kind in frontmatter: %s", got)
	}
	if !strings.Contains(got, "## Finding\nport blocked") {
		t.Errorf("missing Finding section: %s", got)
	}
}

func TestParseEntry(t *testing.T) {
	raw := "---\ntitle: Use Go MCP SDK\ndate: 2026-04-13\nkind: decision\n---\n\n" +
		"## Context\nneeded a server\n\n## Decision\nGo SDK\n"
	e, err := ParseEntry(raw)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if e.Title != "Use Go MCP SDK" {
		t.Errorf("Title = %q", e.Title)
	}
	if e.Kind != KindDecision {
		t.Errorf("Kind = %q", e.Kind)
	}
	if e.Date.Format("2006-01-02") != "2026-04-13" {
		t.Errorf("Date = %v", e.Date)
	}
	if e.Sections["Context"] != "needed a server" {
		t.Errorf("Context section = %q", e.Sections["Context"])
	}
	if e.Sections["Decision"] != "Go SDK" {
		t.Errorf("Decision section = %q", e.Sections["Decision"])
	}
}
