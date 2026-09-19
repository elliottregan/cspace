package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestTailEventsReadsAssistantTextAndToolCalls(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p,
		`{"ts":"2026-09-18T10:00:00Z","kind":"sdk-event","data":{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the file now."},{"type":"tool_use","name":"Read","input":{"file_path":"/workspace/main.go"}}]}}}`,
		`{"ts":"2026-09-18T10:00:01Z","kind":"sdk-event","data":{"type":"user","message":{"content":"a plain string body"}}}`,
		`{"ts":"2026-09-18T10:00:02Z","kind":"sdk-event","data":{"type":"result","subtype":"success"}}`,
	)
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Text != "Reading the file now." {
		t.Errorf("assistant text = %q", got[0].Text)
	}
	if len(got[0].Tools) != 1 || !strings.Contains(got[0].Tools[0], "Read") ||
		!strings.Contains(got[0].Tools[0], "main.go") {
		t.Errorf("tools = %v, want one Read naming the file", got[0].Tools)
	}
	// A content field that is a bare string must not lose the whole line:
	// the SDK's shapes vary and the tail has to tolerate all of them.
	if got[1].Text != "a plain string body" {
		t.Errorf("string content = %q", got[1].Text)
	}
	if got[2].Type != "result" || got[2].Subtype != "success" {
		t.Errorf("result line = %+v", got[2])
	}
}

// A Client built with no Home must fail closed rather than silently read an
// events log path relative to the process cwd.
func TestClientEventsErrorsWithoutHome(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	if _, err := c.Events("alpha", "mercury", 8); !errors.Is(err, ErrNoHome) {
		t.Errorf("err = %v, want ErrNoHome", err)
	}
}
