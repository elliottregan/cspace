package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInteractiveState(t *testing.T) {
	cases := []struct {
		name      string
		write     bool
		contents  string
		wantState string
		wantKnown bool
	}{
		{
			name:      "a complete record parses",
			write:     true,
			contents:  `{"state":"working","at":"2026-09-17T18:40:12Z","session_id":"abc123","event":"UserPromptSubmit"}`,
			wantState: "working",
			wantKnown: true,
		},
		{
			// cspace-agent-state.sh writes a temp file and renames, but a
			// crashed writer (or a non-atomic filesystem) can still leave a
			// half-written record. It must read as "unknown", never as an error.
			name:      "a torn write reads as unknown",
			write:     true,
			contents:  `{"state":"nee`,
			wantState: "",
			wantKnown: false,
		},
		{
			name:      "an empty file reads as unknown",
			write:     true,
			contents:  "",
			wantState: "",
			wantKnown: false,
		},
		{
			name:      "a missing file reads as unknown",
			write:     false,
			wantState: "",
			wantKnown: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-state.json")
			if tc.write {
				if err := os.WriteFile(path, []byte(tc.contents), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got := ReadInteractiveState(path)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.Known() != tc.wantKnown {
				t.Errorf("Known() = %v, want %v", got.Known(), tc.wantKnown)
			}
		})
	}
}

func TestClientInteractiveStateAndEventsReadTheSessionDir(t *testing.T) {
	home := t.TempDir()
	dir := SessionDir(home, "alpha", "mercury")
	if err := os.MkdirAll(filepath.Join(dir, "primary"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(AgentStatePath(home, "alpha", "mercury"),
		[]byte(`{"state":"needs-input","event":"PermissionRequest"}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.WriteFile(SessionEventsPath(home, "alpha", "mercury"),
		[]byte(`{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	c := New(Options{Containers: &fakeContainers{}, Home: home})

	if got := c.InteractiveState("alpha", "mercury"); got.State != "needs-input" || got.Event != "PermissionRequest" {
		t.Errorf("InteractiveState = %+v", got)
	}
	lines, err := c.Events("alpha", "mercury", 8)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(lines) != 1 || lines[0].Type != "assistant" {
		t.Errorf("Events = %+v", lines)
	}
}
