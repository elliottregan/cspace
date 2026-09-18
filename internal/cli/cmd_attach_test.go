package cli

import (
	"errors"
	"testing"
)

// Signal delivery (SIGINT/SIGTERM/SIGWINCH reaching the child, absorbing
// SIGINT here) and the SIGHUP signal-then-kill escalation are not
// unit-tested: they depend on a controlling terminal and process-group
// semantics that a `go test` process doesn't have. They are covered by Task
// 9's manual verification instead.

// TestRunAttachChildPropagatesExitStatus — attach stopped being a
// syscall.Exec, so the child's status has to travel back out by hand or an
// interactive `claude` that exits 1 would look like a success.
func TestRunAttachChildPropagatesExitStatus(t *testing.T) {
	code, err := runAttachChild("/bin/sh", []string{"sh", "-c", "exit 7"})
	if err != nil {
		t.Fatalf("runAttachChild() error: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestRunAttachChildCleanExit(t *testing.T) {
	code, err := runAttachChild("/bin/sh", []string{"sh", "-c", "exit 0"})
	if err != nil {
		t.Fatalf("runAttachChild() error: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// TestRunAttachChildReportsStartFailure — a missing binary is cspace's own
// error, not the session's exit status.
func TestRunAttachChildReportsStartFailure(t *testing.T) {
	if _, err := runAttachChild("/nonexistent/container", []string{"container"}); err == nil {
		t.Error("runAttachChild() returned nil for a binary that cannot start")
	}
}

// TestExitErrorCarriesTheCode — main prints nothing for this error and exits
// with the code, so a session that ended 130 (Ctrl-C) does not print
// "Error: exit status 130" over a terminal the user is done with.
func TestExitErrorCarriesTheCode(t *testing.T) {
	var err error = ExitError{Code: 130}
	var target ExitError
	if !errors.As(err, &target) {
		t.Fatal("ExitError is not recoverable with errors.As")
	}
	if target.Code != 130 {
		t.Errorf("Code = %d, want 130", target.Code)
	}
	if target.Error() == "" {
		t.Error("ExitError has an empty message")
	}
}

// TestAttachHasNoTmuxEscapeHatch — tmux by default, with an undocumented way
// out: the flag exists only until no image without tmux is in use.
func TestAttachHasNoTmuxEscapeHatch(t *testing.T) {
	cmd := newAttachCmd()
	flag := cmd.Flags().Lookup("no-tmux")
	if flag == nil {
		t.Fatal("cspace attach has no --no-tmux flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("--no-tmux defaults to %q, want false so attach uses tmux by default", flag.DefValue)
	}
	if !flag.Hidden {
		t.Error("--no-tmux is documented; the design says keep it undocumented")
	}
}
