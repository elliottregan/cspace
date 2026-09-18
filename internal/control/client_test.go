package control

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// containerExecer's transport-failure branch must fold trimmed stderr into
// the returned error text, matching CLIExecer's own transport-failure branch
// (tmux.go) — otherwise a caller reading only err.Error() loses the one
// piece of text that usually explains why the command could not even start.
func TestContainerExecerFoldsStderrIntoTransportFailure(t *testing.T) {
	wantErr := errors.New("boom")
	fc := &fakeContainers{execErr: wantErr, execStderr: "  container: no such container  \n"}
	e := containerExecer{cli: fc}

	_, code, err := e.Exec(context.Background(), "cspace-alpha-mercury", []string{"tmux", "list-clients"})
	if code != -1 {
		t.Errorf("code = %d, want -1", code)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want it to wrap %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "container: no such container") {
		t.Errorf("err = %v, want the trimmed stderr folded in", err)
	}
}

// A transport failure with no stderr at all must not add empty noise to the
// error text.
func TestContainerExecerTransportFailureWithNoStderr(t *testing.T) {
	wantErr := errors.New("boom")
	fc := &fakeContainers{execErr: wantErr}
	e := containerExecer{cli: fc}

	_, _, err := e.Exec(context.Background(), "cspace-alpha-mercury", []string{"tmux", "list-clients"})
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v, want exactly the underlying error with nothing appended", err)
	}
}

// noContainerExecer must fail every call closed, without needing a real
// ContainerCLI or touching the host.
func TestNoContainerExecerFailsClosed(t *testing.T) {
	var e noContainerExecer
	_, code, err := e.Exec(context.Background(), "cspace-alpha-mercury", []string{"tmux", "list-clients"})
	if !errors.Is(err, ErrNoContainerCLI) {
		t.Errorf("err = %v, want ErrNoContainerCLI", err)
	}
	if code != -1 {
		t.Errorf("code = %d, want -1", code)
	}
}
