package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeContextDir creates a .cspace/context/ tree under dir with the given
// files. Each key is a relative path from .cspace/context/ (e.g.
// "findings/2026-04-13-some-finding.md"), each value is the file content.
func makeContextDir(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	ctxDir := filepath.Join(dir, ".cspace", "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		abs := filepath.Join(ctxDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// drainRecords collects all records and errors from a ContextCorpus enumeration.
func drainRecords(t *testing.T, recs <-chan Record, errs <-chan error) []Record {
	t.Helper()
	var out []Record
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range errs {
			t.Errorf("unexpected error: %v", e)
		}
	}()
	for r := range recs {
		out = append(out, r)
	}
	<-done
	return out
}

func TestContextCorpus_EnumeratesAllArtifactTypes(t *testing.T) {
	dir := t.TempDir()
	makeContextDir(t, dir, map[string]string{
		"principles.md":                          "# Principles\nKeep it simple.",
		"findings/2026-04-13-some-finding.md":    "# Finding\nSomething was found.",
		"decisions/2026-04-10-use-go.md":         "# Decision\nUse Go for the CLI.",
		"discoveries/2026-04-12-perf-insight.md": "# Discovery\nPerf insight here.",
	})

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 4 {
		t.Fatalf("expected 4 records, got %d", len(records))
	}

	// Build a map of path -> record for assertion.
	byPath := map[string]Record{}
	for _, r := range records {
		byPath[r.Path] = r
	}

	// Check principles.md
	r, ok := byPath[".cspace/context/principles.md"]
	if !ok {
		t.Fatal("missing record for principles.md")
	}
	if r.Kind != "context" {
		t.Errorf("principles.md: expected Kind=context, got %q", r.Kind)
	}
	if r.Extra["subkind"] != "principles" {
		t.Errorf("principles.md: expected subkind=principles, got %v", r.Extra["subkind"])
	}

	// Check finding
	r, ok = byPath[".cspace/context/findings/2026-04-13-some-finding.md"]
	if !ok {
		t.Fatal("missing record for finding")
	}
	if r.Kind != "finding" {
		t.Errorf("finding: expected Kind=finding, got %q", r.Kind)
	}

	// Check decision
	r, ok = byPath[".cspace/context/decisions/2026-04-10-use-go.md"]
	if !ok {
		t.Fatal("missing record for decision")
	}
	if r.Kind != "decision" {
		t.Errorf("decision: expected Kind=decision, got %q", r.Kind)
	}

	// Check discovery
	r, ok = byPath[".cspace/context/discoveries/2026-04-12-perf-insight.md"]
	if !ok {
		t.Fatal("missing record for discovery")
	}
	if r.Kind != "discovery" {
		t.Errorf("discovery: expected Kind=discovery, got %q", r.Kind)
	}

	// All records should have ContentHash and EmbedText.
	for _, r := range records {
		if r.ContentHash == "" {
			t.Errorf("record %s missing ContentHash", r.Path)
		}
		if r.EmbedText == "" {
			t.Errorf("record %s missing EmbedText", r.Path)
		}
	}
}

func TestContextCorpus_MissingSubdirectories(t *testing.T) {
	dir := t.TempDir()
	// Only create principles.md — no findings/, decisions/, discoveries/.
	makeContextDir(t, dir, map[string]string{
		"principles.md": "# Principles\nKeep it simple.",
	})

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 1 {
		t.Fatalf("expected 1 record (only principles.md), got %d", len(records))
	}
	if records[0].Kind != "context" {
		t.Errorf("expected Kind=context, got %q", records[0].Kind)
	}
	if records[0].Path != ".cspace/context/principles.md" {
		t.Errorf("expected path .cspace/context/principles.md, got %q", records[0].Path)
	}
}

func TestContextCorpus_EmptyContextDir(t *testing.T) {
	dir := t.TempDir()
	// Create the context dir but put nothing in it.
	if err := os.MkdirAll(filepath.Join(dir, ".cspace", "context"), 0o755); err != nil {
		t.Fatal(err)
	}

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 0 {
		t.Fatalf("expected 0 records for empty context dir, got %d", len(records))
	}
}

func TestContextCorpus_NoContextDirAtAll(t *testing.T) {
	dir := t.TempDir()
	// Don't even create .cspace/context/.

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 0 {
		t.Fatalf("expected 0 records when .cspace/context/ does not exist, got %d", len(records))
	}
}

func TestContextCorpus_ID(t *testing.T) {
	cc := &ContextCorpus{}
	if cc.ID() != "context" {
		t.Errorf("expected ID=context, got %q", cc.ID())
	}
}

func TestContextCorpus_CollectionName(t *testing.T) {
	cc := &ContextCorpus{}
	got := cc.Collection(".")
	if !strings.HasPrefix(got, "context-") {
		t.Errorf("expected context- prefix, got %q", got)
	}
}

func TestContextCorpus_EmbedTextFormat(t *testing.T) {
	dir := t.TempDir()
	makeContextDir(t, dir, map[string]string{
		"direction.md": "We are heading north.",
	})

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	r := records[0]
	// EmbedText should start with the header.
	if !strings.HasPrefix(r.EmbedText, "Context (direction): .cspace/context/direction.md\n\n") {
		t.Errorf("unexpected EmbedText prefix: %q", r.EmbedText[:80])
	}
	if !strings.Contains(r.EmbedText, "We are heading north.") {
		t.Error("EmbedText should contain file content")
	}
}

func TestContextCorpus_RoadmapAndDirection(t *testing.T) {
	dir := t.TempDir()
	makeContextDir(t, dir, map[string]string{
		"direction.md": "# Direction\nGo north.",
		"roadmap.md":   "# Roadmap\nPhase 1.",
	})

	cc := &ContextCorpus{}
	recs, errs := cc.Enumerate(dir)
	records := drainRecords(t, recs, errs)

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	byPath := map[string]Record{}
	for _, r := range records {
		byPath[r.Path] = r
	}

	r, ok := byPath[".cspace/context/direction.md"]
	if !ok {
		t.Fatal("missing direction.md record")
	}
	if r.Kind != "context" || r.Extra["subkind"] != "direction" {
		t.Errorf("direction.md: expected Kind=context subkind=direction, got Kind=%q subkind=%v", r.Kind, r.Extra["subkind"])
	}

	r, ok = byPath[".cspace/context/roadmap.md"]
	if !ok {
		t.Fatal("missing roadmap.md record")
	}
	if r.Kind != "context" || r.Extra["subkind"] != "roadmap" {
		t.Errorf("roadmap.md: expected Kind=context subkind=roadmap, got Kind=%q subkind=%v", r.Kind, r.Extra["subkind"])
	}
}
