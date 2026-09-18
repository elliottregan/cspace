package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadUser answers from the embedded defaults alone when the user has no
// config file — the overwhelmingly common case, and not an error.
func TestLoadUserWithNoFileReturnsTheDefaults(t *testing.T) {
	cfg, err := LoadUser(t.TempDir())
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if len(cfg.TUI.Keys) == 0 {
		t.Fatal("defaults.json should carry a tui.keys object")
	}
	if got := cfg.TUI.Keys["attach"]; len(got) == 0 {
		t.Errorf("tui.keys.attach = %v, want the default keystrokes", got)
	}
}

// A user file overrides one action and leaves every other alone: DeepMerge
// merges objects recursively and replaces arrays wholesale, so "attach" is
// replaced, not appended to, and "quit" survives untouched.
func TestLoadUserMergesOverTheDefaults(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(UserConfigPath(home),
		[]byte(`{"tui":{"keys":{"attach":["o"]}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadUser(home)
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if got := cfg.TUI.Keys["attach"]; len(got) != 1 || got[0] != "o" {
		t.Errorf("tui.keys.attach = %v, want [o] (arrays replace wholesale)", got)
	}
	if len(cfg.TUI.Keys["quit"]) == 0 {
		t.Error("an override of one action must not drop the others")
	}
}

// A malformed user file is an error: silently ignoring it would leave a
// person staring at bindings they thought they had changed.
func TestLoadUserRejectsMalformedJSON(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(UserConfigPath(home), []byte("{nope"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadUser(home); err == nil {
		t.Error("want an error for a malformed user config")
	}
}

// LoadUser needs no git repository and no project root: the dashboard it
// serves spans every project and may be started from anywhere.
func TestLoadUserNeedsNoProjectRoot(t *testing.T) {
	cfg, err := LoadUser(t.TempDir())
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	if cfg.ProjectRoot != "" {
		t.Errorf("ProjectRoot = %q, want empty", cfg.ProjectRoot)
	}
}
