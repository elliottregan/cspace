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
	// The v1 --interval flag is gone: the v2 dashboard polls on three
	// cadences and no single interval describes it.
	if f := cmd.Flags().Lookup("interval"); f != nil {
		t.Errorf("--interval should be gone in the v2 dashboard, got default %q", f.DefValue)
	}
	// The command takes no arguments: it shows every project on the host.
	if err := cmd.Args(cmd, []string{"mercury"}); err == nil {
		t.Error("tui should refuse positional arguments")
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
