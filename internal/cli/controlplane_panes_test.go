package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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

// The warning is only useful if the operator can read the remedy, and the
// footer it lands in truncates to the window width — so the whole line has
// to fit an ordinary terminal, remedy included. It carries both steps of
// that remedy, because the rebuild alone leaves the running sandbox on the
// old image.
func TestNoTmuxWarningFitsAFooterAndNamesBothStepsOfTheRemedy(t *testing.T) {
	warn := noTmuxWarning()
	for _, want := range []string{"image build", "down"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning = %q, want it to name %q", warn, want)
		}
	}
	if n := ansi.StringWidth(warn); n > warningWidth {
		t.Errorf("warning is %d cells, want at most %d: %q", n, warningWidth, warn)
	}
}

// The other degraded open has to reach the footer too: it is the one the
// operator cannot recover from on their own, because nothing records the
// tmux client it strands. See openWarning.
func TestDegradedAttachWarningFitsAFooterAndSaysWhatItCosts(t *testing.T) {
	warn := degradedAttachWarning()
	if !strings.Contains(warn, "tmux client") {
		t.Errorf("warning = %q, want it to say what is left behind", warn)
	}
	if n := ansi.StringWidth(warn); n > warningWidth {
		t.Errorf("warning is %d cells, want at most %d: %q", n, warningWidth, warn)
	}
}

// openWarning is the whole of Opened.Warning: a clean open says nothing, a
// degraded attachment is reported rather than swallowed, and with both
// broken the line names the one with a remedy.
func TestOpenWarningPicksOneLine(t *testing.T) {
	cases := []struct {
		name                       string
		tmuxPresent, bookkeepingOK bool
		want                       string
	}{
		{"a clean open warns about nothing", true, true, ""},
		{"a degraded attachment is surfaced", true, false, degradedAttachWarning()},
		{"no tmux names the remedy", false, true, noTmuxWarning()},
		{"both broken: the remedy wins", false, false, noTmuxWarning()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openWarning(tc.tmuxPresent, !tc.bookkeepingOK); got != tc.want {
				t.Errorf("openWarning = %q, want %q", got, tc.want)
			}
		})
	}
}

// Which pane kinds get pane.ExtendedKeys is a decision with no test of its
// own until here — the option is passed inside Open, past a tmux probe that
// needs a container — so this pins the rule the way the code states it:
// Claude under tmux, and nothing else. A shell has no CSI-u decoder (bash
// and zsh answer the forced form with a beep and the tail of the sequence
// typed onto the command line), and a no-tmux pane's child negotiates for
// itself.
func TestOnlyAClaudePaneUnderTmuxForcesTheExtendedKeys(t *testing.T) {
	cases := []struct {
		kind    controlplane.Kind
		session string
		want    bool
	}{
		{controlplane.KindClaude, "cspace", true},
		{controlplane.KindClaude, "", false},
		{controlplane.KindShell, "cspace", false},
		{controlplane.KindShell, "", false},
		{controlplane.KindHostShell, "", false},
	}
	for _, tc := range cases {
		if got := forcesExtendedKeys(tc.kind, tc.session); got != tc.want {
			t.Errorf("kind %v session %q forces extended keys = %v, want %v",
				tc.kind, tc.session, got, tc.want)
		}
	}
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
