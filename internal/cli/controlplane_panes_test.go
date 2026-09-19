package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

func TestPaneHostOpensAHostShellWithNoContainer(t *testing.T) {
	// pane.HostShell reads $SHELL at call time and runs it with -l, so
	// without this the test sources the operator's own .zprofile in a pty
	// during `go test`. /bin/sh -l is a login shell that does almost
	// nothing, which is all this case needs: that a pane opened and has no
	// detacher.
	t.Setenv("SHELL", "/bin/sh")

	h := newPaneHost(control.New(control.Options{}), t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opened, err := h.Open(ctx, controlplane.KindHostShell, control.Row{}, 40, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Pane == nil {
		t.Fatal("no pane")
	}
	if opened.Detach != nil {
		t.Error("a host shell is not a tmux client and must have no detacher")
	}
	_ = opened.Pane.Close(ctx)
}

func TestPaneHostRefusesASandboxWithNoContainer(t *testing.T) {
	h := newPaneHost(control.New(control.Options{}), t.TempDir())
	_, err := h.Open(context.Background(), controlplane.KindClaude,
		control.Row{Kind: control.RowSandbox, Project: "demo", Name: "mercury"}, 40, 10)
	if err == nil {
		t.Fatal("Open accepted a row with no container")
	}
	if !strings.Contains(err.Error(), "mercury") {
		t.Errorf("error = %v, want it to name the sandbox", err)
	}
}
