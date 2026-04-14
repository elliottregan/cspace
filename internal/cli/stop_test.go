package cli

import "testing"

func TestStopCmdExists(t *testing.T) {
	cmd := newStopCmd()
	if cmd.Use == "" {
		t.Fatal("expected newStopCmd to return a configured command")
	}
	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}
}
