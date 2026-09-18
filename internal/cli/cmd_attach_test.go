package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/control"
)

// fakeExecer is a control.Execer stand-in for tests that need to drive
// defaultTmux's probe without a real `container` CLI. It always reports the
// same (stdout, exitCode, err) triple regardless of what it's asked to run.
type fakeExecer struct {
	stdout   string
	exitCode int
	err      error
}

func (f fakeExecer) Exec(context.Context, string, []string) (string, int, error) {
	return f.stdout, f.exitCode, f.err
}

// withFakeExec swaps defaultTmux.Exec for the duration of a test and
// restores it afterward. defaultTmux is a process-wide var shared by every
// test in this package, so callers must use a container name unique to the
// test — Present's memoization is per-container and would otherwise leak a
// cached answer from an unrelated test.
func withFakeExec(t *testing.T, e control.Execer) {
	t.Helper()
	orig := defaultTmux.Exec
	defaultTmux.Exec = e
	t.Cleanup(func() { defaultTmux.Exec = orig })
}

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

// TestAttachInteractiveAbortsWhenTheTmuxProbeFails — Important 3. A
// transport error means we don't know whether tmux is there, not that it
// isn't; falling back to a direct exec would just hit the same transport
// failure a moment later, so attachInteractive refuses instead of guessing.
func TestAttachInteractiveAbortsWhenTheTmuxProbeFails(t *testing.T) {
	withFakeExec(t, fakeExecer{err: errors.New("boom: transport down")})

	err := attachInteractive(context.Background(), io.Discard,
		"proj", "probe-error-sandbox", "cspace-proj-probe-error-sandbox", true)
	if err == nil {
		t.Fatal("attachInteractive() error = nil, want the probe failure surfaced")
	}
	if !strings.Contains(err.Error(), "cannot reach sandbox probe-error-sandbox to probe for tmux") {
		t.Errorf("error = %q, want it to name the probe failure", err.Error())
	}
}

// TestBeginAttachOrWarnFallsBackWhenHomeUnavailable — Important 4(b). No
// resolvable home directory means nowhere to put the lock/records; that must
// degrade to a warning and an inert attachment, not refuse the whole attach.
func TestBeginAttachOrWarnFallsBackWhenHomeUnavailable(t *testing.T) {
	var buf bytes.Buffer
	att, warned, err := beginAttachOrWarn(context.Background(), &buf, defaultTmux,
		"", errors.New("$HOME is not defined"),
		"proj", "sandbox-home-fail", "cspace-proj-sandbox-home-fail", control.SessionClaude)
	if err != nil {
		t.Fatalf("beginAttachOrWarn() error = %v, want nil (should degrade to an inert attach)", err)
	}
	if att == nil {
		t.Fatal("beginAttachOrWarn() returned a nil Attachment")
	}
	if !warned {
		t.Error("beginAttachOrWarn() warned = false, want true when bookkeeping degrades")
	}
	if !strings.Contains(buf.String(), "attach bookkeeping unavailable") {
		t.Errorf("warning = %q, want it to mention bookkeeping being unavailable", buf.String())
	}
}

// TestBeginAttachOrWarnFallsBackWhenControlPlaneDirUnusable — Important
// 4(b)'s other trigger: BeginAttach itself reports
// control.ErrBookkeepingUnavailable (here, because the given home directory
// is actually a regular file, so the control-plane directory under it can
// never be created).
func TestBeginAttachOrWarnFallsBackWhenControlPlaneDirUnusable(t *testing.T) {
	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "home-is-a-file")
	if err := os.WriteFile(fakeHome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	att, warned, err := beginAttachOrWarn(context.Background(), &buf, defaultTmux,
		fakeHome, nil,
		"proj", "sandbox-dir-fail", "cspace-proj-sandbox-dir-fail", control.SessionClaude)
	if err != nil {
		t.Fatalf("beginAttachOrWarn() error = %v, want nil (should degrade to an inert attach)", err)
	}
	if att == nil {
		t.Fatal("beginAttachOrWarn() returned a nil Attachment")
	}
	if !warned {
		t.Error("beginAttachOrWarn() warned = false, want true when bookkeeping degrades")
	}
	if !strings.Contains(buf.String(), "attach bookkeeping unavailable") {
		t.Errorf("warning = %q, want it to mention bookkeeping being unavailable", buf.String())
	}
}
