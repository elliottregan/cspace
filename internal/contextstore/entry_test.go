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

func TestSectionOrderFinding(t *testing.T) {
	got := SectionOrder(KindFinding)
	want := []string{"Summary", "Details", "Updates"}
	if len(got) != len(want) {
		t.Fatalf("section count: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("section %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRenderFindingIncludesFrontmatter(t *testing.T) {
	e := Entry{
		Kind:     KindFinding,
		Title:    "Signup button unresponsive",
		Date:     time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		Status:   FindingStatusOpen,
		Category: FindingCategoryBug,
		Tags:     []string{"ux", "signup"},
		Related:  []string{"2026-04-10-onboarding-friction"},
		Sections: map[string]string{
			"Summary": "Click does nothing",
			"Details": "5 of 7 personas hit this",
			"Updates": "### 2026-04-15T10:00:00Z — @coord — status: open\nfiled",
		},
	}
	got := e.Render()
	for _, want := range []string{
		"title: Signup button unresponsive",
		"kind: finding",
		"status: open",
		"category: bug",
		"tags: ux, signup",
		"related: 2026-04-10-onboarding-friction",
		"## Summary\nClick does nothing",
		"## Updates\n### 2026-04-15T10:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render missing %q:\n%s", want, got)
		}
	}
}

func TestRenderFindingRoundTrip(t *testing.T) {
	orig := Entry{
		Kind:     KindFinding,
		Title:    "Onboarding step X confusion",
		Date:     time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		Status:   FindingStatusAcknowledged,
		Category: FindingCategoryObservation,
		Tags:     []string{"ux", "onboarding", "persona-eval"},
		Related:  []string{"owner/repo#234", "2026-04-10-signup"},
		Sections: map[string]string{
			"Summary": "Personas stopping at step X",
			"Details": "Maria, Priya, Carlos, James, Aisha all dropped off here",
			"Updates": "### 2026-04-15T10:00:00Z — @persona-coord — status: open\ninitial aggregation\n\n### 2026-04-16T14:00:00Z — @coord — status: acknowledged\ntriaged",
		},
	}
	raw := orig.Render()
	parsed, err := ParseEntry(raw)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if parsed.Title != orig.Title {
		t.Errorf("Title: got %q, want %q", parsed.Title, orig.Title)
	}
	if parsed.Kind != orig.Kind {
		t.Errorf("Kind: got %q, want %q", parsed.Kind, orig.Kind)
	}
	if parsed.Status != orig.Status {
		t.Errorf("Status: got %q, want %q", parsed.Status, orig.Status)
	}
	if parsed.Category != orig.Category {
		t.Errorf("Category: got %q, want %q", parsed.Category, orig.Category)
	}
	if strings.Join(parsed.Tags, ",") != strings.Join(orig.Tags, ",") {
		t.Errorf("Tags: got %v, want %v", parsed.Tags, orig.Tags)
	}
	if strings.Join(parsed.Related, ",") != strings.Join(orig.Related, ",") {
		t.Errorf("Related: got %v, want %v", parsed.Related, orig.Related)
	}
	for _, k := range []string{"Summary", "Details", "Updates"} {
		if parsed.Sections[k] != orig.Sections[k] {
			t.Errorf("Section %s:\n  got: %q\n  want: %q", k, parsed.Sections[k], orig.Sections[k])
		}
	}
	// Updates body must preserve the order of the two `###` subheadings.
	first := strings.Index(parsed.Sections["Updates"], "2026-04-15T10:00:00Z")
	second := strings.Index(parsed.Sections["Updates"], "2026-04-16T14:00:00Z")
	if first < 0 || second < 0 || first >= second {
		t.Errorf("Updates ordering not preserved: first=%d, second=%d\n%s",
			first, second, parsed.Sections["Updates"])
	}
}

func TestParseEntryHandlesEmptyOrMissingTags(t *testing.T) {
	// Tags field present but empty → parse returns nil (not []string{""})
	raw := "---\ntitle: T\ndate: 2026-04-15\nkind: finding\ntags: \n---\n\n## Summary\nx\n"
	e, err := ParseEntry(raw)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if len(e.Tags) != 0 {
		t.Errorf("expected empty tags, got %v", e.Tags)
	}
	// Tags field missing entirely → parse returns nil
	raw2 := "---\ntitle: T\ndate: 2026-04-15\nkind: finding\n---\n\n## Summary\nx\n"
	e2, err := ParseEntry(raw2)
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	if len(e2.Tags) != 0 {
		t.Errorf("expected nil tags, got %v", e2.Tags)
	}
}

func TestRenderNonFindingOmitsNewFrontmatter(t *testing.T) {
	// A decision/discovery with Status/Category/Tags set (shouldn't happen
	// in practice) must still render only the base three frontmatter keys
	// when those fields are explicitly empty — i.e., existing entries are
	// unchanged by this schema extension.
	e := Entry{
		Kind:  KindDecision,
		Title: "T",
		Date:  time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC),
		Sections: map[string]string{
			"Context": "c", "Alternatives": "a", "Decision": "d", "Consequences": "con",
		},
	}
	got := e.Render()
	for _, unwanted := range []string{"status:", "category:", "tags:", "related:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Render should not emit %q for non-finding: %s", unwanted, got)
		}
	}
}
