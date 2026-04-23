package cli

import (
	"testing"
)

func TestWatchCmdExists(t *testing.T) {
	cmd := newWatchCmd()
	if cmd.Use == "" {
		t.Fatal("expected newWatchCmd to return a configured command")
	}
	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}
}

func TestWatchCmdFlags(t *testing.T) {
	cmd := newWatchCmd()
	if f := cmd.Flags().Lookup("addr"); f == nil {
		t.Error("expected --addr flag")
	}
	insideFlag := cmd.Flags().Lookup("inside")
	if insideFlag == nil {
		t.Error("expected --inside flag")
	} else if !insideFlag.Hidden {
		t.Error("expected --inside to be hidden")
	}
}
