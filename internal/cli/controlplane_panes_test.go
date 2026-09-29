package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/pane"
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

// Exercise the same session preparation path as production, replacing only
// guest commands and the final pty spawn. No test needs Apple Container.
type paneSessionExecer struct {
	mu       sync.Mutex
	present  bool
	sessions []string
	creates  int
}

func (e *paneSessionExecer) Exec(_ context.Context, _ string, cmd []string) (string, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	joined := strings.Join(cmd, " ")
	if strings.Contains(joined, "command -v tmux") {
		if e.present {
			return "yes\n", 0, nil
		}
		return "no\n", 0, nil
	}
	if slices.Contains(cmd, "list-sessions") {
		return strings.Join(e.sessions, "\n"), 0, nil
	}
	if slices.Contains(cmd, "list-clients") {
		return "", 0, nil
	}
	if slices.Contains(cmd, "new-session") {
		if slices.Contains(cmd, "-A") {
			return "create must not attach to an existing session", 1, nil
		}
		nameAt, tokenAt := slices.Index(cmd, "-s"), slices.Index(cmd, "@cspace_id")
		if nameAt < 0 {
			return "missing session identity", 1, nil
		}
		name, token := cmd[nameAt+1], ""
		if tokenAt >= 0 {
			token = cmd[tokenAt+1]
		}
		for _, session := range e.sessions {
			if strings.HasPrefix(session, name+"\t") {
				return "duplicate session", 1, nil
			}
		}
		e.creates++
		record := fmt.Sprintf("%s\t$%d\t1700000000\t100\t1699999999\t%s", name, e.creates+1, token)
		e.sessions = append(e.sessions, record)
		return record + "\n", 0, nil
	}
	return "unexpected guest command: " + joined, 1, nil
}

func paneSessionHost(t *testing.T, present bool) (*paneHost, *paneSessionExecer, *[]pane.Command) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "container"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	e := &paneSessionExecer{present: present, sessions: []string{"cspace-claude\t$1\t1700000000\t100\t1699999999\t"}}
	tm := control.NewTmux()
	tm.Exec, tm.PollEvery, tm.PollFor = e, time.Millisecond, 25*time.Millisecond
	h := newPaneHost(control.New(control.Options{Tmux: tm}), t.TempDir())
	commands := new([]pane.Command)
	h.openPane = func(cmd pane.Command, _, _ int, _ ...pane.Option) (*pane.Pane, error) {
		*commands = append(*commands, cmd)
		return nil, nil
	}
	return h, e, commands
}

func paneSessionRow() control.Row {
	return control.Row{Kind: control.RowSandbox, Project: "demo", Name: "mercury", Container: "cspace-demo-mercury"}
}

func TestPaneHostNewSessionsHaveDistinctIdentitiesAndTargetedReconnect(t *testing.T) {
	h, execer, commands := paneSessionHost(t, true)
	ctx := context.Background()
	var sessions []control.Session
	for _, name := range []string{"cspace-claude-2", "cspace-claude-3"} {
		opened, err := h.OpenSession(ctx, paneSessionRow(), control.AttachRequest{New: true}, 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		if opened.Session.Name != name || opened.Session.ID == "" || opened.Warning != "" {
			t.Fatalf("opened = %+v, want named session %s", opened, name)
		}
		sessions = append(sessions, opened.Session)
		if err := opened.Detach.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if sessions[0].ID == sessions[1].ID {
		t.Fatal("new sessions share an identity")
	}
	opened, err := h.OpenSession(ctx, paneSessionRow(), control.AttachRequest{
		Session: sessions[0].Name, ExpectedID: sessions[0].ID,
	}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Detach.Close(ctx) //nolint:errcheck
	if opened.Session.ID != sessions[0].ID || execer.creates != 2 {
		t.Fatalf("reconnect created or selected another session: %+v; creates=%d", opened.Session, execer.creates)
	}
	if len(*commands) != 3 {
		t.Fatalf("spawned %d panes, want 3", len(*commands))
	}
	for _, cmd := range *commands {
		joined := strings.Join(cmd.Args, " ")
		if !strings.Contains(joined, "attach-session") || slices.Contains(cmd.Args, "-A") {
			t.Errorf("named-session argv is not attach-only: %q", cmd.Args)
		}
	}
}

func TestPaneHostMissingOrReplacedSessionDoesNotCreate(t *testing.T) {
	h, execer, commands := paneSessionHost(t, true)
	for _, req := range []control.AttachRequest{
		{Session: "cspace-claude-9"},
		{Session: control.SessionClaude, ExpectedID: "a previous incarnation"},
	} {
		if _, err := h.OpenSession(context.Background(), paneSessionRow(), req, 80, 24); !errors.Is(err, control.ErrSessionGone) {
			t.Fatalf("error = %v, want ErrSessionGone", err)
		}
	}
	if len(*commands) != 0 || execer.creates != 0 {
		t.Fatal("a missing session spawned or created a replacement")
	}
}

func TestPaneHostNewSessionRequiresTmuxButDefaultStillFallsBack(t *testing.T) {
	h, _, commands := paneSessionHost(t, false)
	if _, err := h.OpenSession(context.Background(), paneSessionRow(), control.AttachRequest{New: true}, 80, 24); !errors.Is(err, control.ErrTmuxRequired) {
		t.Fatalf("error = %v, want ErrTmuxRequired", err)
	}
	if len(*commands) != 0 {
		t.Fatal("new session fell back to a direct Claude process")
	}
	opened, err := h.Open(context.Background(), controlplane.KindClaude, paneSessionRow(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Detach.Close(context.Background()) //nolint:errcheck
	if opened.Warning != noTmuxWarning() || opened.Session.Name != "" {
		t.Fatalf("default fallback = %+v", opened)
	}
	if len(*commands) != 1 || slices.Contains((*commands)[0].Args, "tmux") {
		t.Fatalf("default did not launch directly: %+v", *commands)
	}
}

func TestPaneHostDefaultPinsExistingSessionIdentity(t *testing.T) {
	h, execer, commands := paneSessionHost(t, true)
	opened, err := h.Open(context.Background(), controlplane.KindClaude, paneSessionRow(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Detach.Close(context.Background()) //nolint:errcheck
	if opened.Session.Name != control.SessionClaude || opened.Session.ID == "" || execer.creates != 0 {
		t.Fatalf("default session identity = %+v; creates=%d", opened.Session, execer.creates)
	}
	if len(*commands) != 1 || slices.Contains((*commands)[0].Args, "-A") || !strings.Contains(strings.Join((*commands)[0].Args, " "), opened.Session.ID) {
		t.Fatalf("default client is not pinned to the reported identity: %+v", *commands)
	}
}

func TestPaneHostDefaultCreatesThenReportsADifferentIdentityAfterReplacement(t *testing.T) {
	h, execer, commands := paneSessionHost(t, true)
	first, err := h.Open(context.Background(), controlplane.KindClaude, paneSessionRow(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Detach.Close(context.Background())
	execer.mu.Lock()
	execer.sessions = nil
	execer.mu.Unlock()
	second, err := h.Open(context.Background(), controlplane.KindClaude, paneSessionRow(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Detach.Close(context.Background()) //nolint:errcheck
	if second.Session.Name != control.SessionClaude || second.Session.ID == "" || second.Session.ID == first.Session.ID || execer.creates != 1 {
		t.Fatalf("replacement is not independently identified: first=%+v second=%+v creates=%d", first.Session, second.Session, execer.creates)
	}
	if len(*commands) != 2 || !strings.Contains(strings.Join((*commands)[1].Args, " "), second.Session.ID) {
		t.Fatalf("replacement client does not carry its identity: %+v", *commands)
	}
}

func TestPaneHostDefaultStillWarnsWhenBookkeepingUnavailable(t *testing.T) {
	h, execer, commands := paneSessionHost(t, true)
	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.home = blockedHome
	opened, err := h.Open(context.Background(), controlplane.KindClaude, paneSessionRow(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Detach.Close(context.Background()) //nolint:errcheck
	if opened.Warning != degradedAttachWarning() || opened.Session.ID != "" || execer.creates != 0 || len(*commands) != 1 || !slices.Contains((*commands)[0].Args, "-A") {
		t.Fatalf("bookkeeping warning fallback changed: opened=%+v commands=%+v creates=%d", opened, *commands, execer.creates)
	}
}

func TestPaneHostFailedSpawnReleasesSessionAttachLock(t *testing.T) {
	h, _, _ := paneSessionHost(t, true)
	wantErr := errors.New("pty unavailable")
	h.openPane = func(pane.Command, int, int, ...pane.Option) (*pane.Pane, error) {
		return nil, wantErr
	}
	if _, err := h.OpenSession(context.Background(), paneSessionRow(), control.AttachRequest{New: true}, 80, 24); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	h.openPane = func(pane.Command, int, int, ...pane.Option) (*pane.Pane, error) { return nil, nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	opened, err := h.OpenSession(ctx, paneSessionRow(), control.AttachRequest{Session: "cspace-claude-2"}, 80, 24)
	if err != nil {
		t.Fatalf("attach after failed spawn: %v", err)
	}
	_ = opened.Detach.Close(ctx)
}
