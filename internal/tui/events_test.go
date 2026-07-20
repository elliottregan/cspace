package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestTailEventsLastN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p,
		`{"ts":"2026-07-20T04:12:01Z","kind":"sdk-event","data":{"type":"assistant"}}`,
		`{"ts":"2026-07-20T04:12:02Z","kind":"sdk-event","data":{"type":"user"}}`,
		`{"ts":"2026-07-20T04:12:03Z","kind":"sdk-event","data":{"type":"result","subtype":"success"}}`,
	)
	got, err := TailEvents(p, 2)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Type != "user" || got[1].Type != "result" || got[1].Subtype != "success" {
		t.Errorf("got = %+v", got)
	}
}

func TestTailEventsFewerThanN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p, `{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`)
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestTailEventsToleratesMalformedTrailingLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	// Second line is a partially-flushed / malformed trailing line.
	if err := os.WriteFile(p, []byte(
		`{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`+"\n"+
			`{"ts":"t2","kind":"sdk-ev`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 1 || got[0].Type != "assistant" {
		t.Errorf("got = %+v, want the one valid line", got)
	}
}

func TestTailEventsMissingFileIsNotError(t *testing.T) {
	got, err := TailEvents(filepath.Join(t.TempDir(), "nope.ndjson"), 8)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestSessionEventsPath(t *testing.T) {
	got := SessionEventsPath("/home/x", "alpha", "mercury")
	want := "/home/x/.cspace/sessions/alpha/mercury/primary/events.ndjson"
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
