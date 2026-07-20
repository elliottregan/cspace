package cli

import (
	"testing"
)

func TestNewTuiCmdBasics(t *testing.T) {
	cmd := newTuiCmd()
	if cmd.Use != "tui" {
		t.Errorf("Use = %q, want tui", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short must be set")
	}
	// --interval flag exists with a sane default
	f := cmd.Flags().Lookup("interval")
	if f == nil {
		t.Fatal("--interval flag missing")
	}
	if f.DefValue != "2s" {
		t.Errorf("--interval default = %q, want 2s", f.DefValue)
	}
}

func TestRootRegistersTui(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "tui" {
			return
		}
	}
	t.Error("root does not register the tui command")
}
