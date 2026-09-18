package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/registry"
)

// countingExecer records how many commands were run through it, so a test
// can prove that nothing touched the host.
type countingExecer struct {
	calls atomic.Int64
	err   error
}

func (c *countingExecer) Exec(context.Context, string, []string) (string, int, error) {
	c.calls.Add(1)
	return "", 1, c.err
}

// drainMsg runs a command and returns its message.
func drainMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// cpActorAgainst builds an actor whose control client resolves
// alpha/mercury to the given stub supervisor, so the HTTP path runs end to
// end through the same registry lookup production uses.
func cpActorAgainst(t *testing.T, controlURL string) *cpActor {
	t.Helper()
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{
		Project: "alpha", Name: "mercury", ControlURL: controlURL, Token: "tok", State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return newControlPlaneActor(control.New(control.Options{Entries: reg}), t.TempDir())
}

func TestControlPlaneActorSendPostsToControlURL(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotCT = req.Header.Get("Content-Type")
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotBody = body["text"]
		w.WriteHeader(200)
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drainMsg(a.Send(row, "hello"))

	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("send should succeed, got %v", err)
	}
	if l, _ := controlplane.ResultLabel(msg); l != "send" {
		t.Errorf("label = %q, want \"send\"", l)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" || gotBody != "hello" {
		t.Errorf("send request: path=%q auth=%q ct=%q body=%q", gotPath, gotAuth, gotCT, gotBody)
	}
}

// Carry-forward from the step-2 review: a 409 means the agent was simply
// idle. The dashboard keeps reporting that as success.
func TestControlPlaneActorInterrupt409IsBenign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no active task"})
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drainMsg(a.Interrupt(row))
	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("interrupt 409 should be benign, got %v", err)
	}
	if l, _ := controlplane.ResultLabel(msg); l != "interrupt" {
		t.Errorf("label = %q, want \"interrupt\"", l)
	}
}

func TestControlPlaneActorInterrupt500Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "boom"})
	}))
	defer srv.Close()

	a := cpActorAgainst(t, srv.URL)
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	err := controlplane.ResultErr(drainMsg(a.Interrupt(row)))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("interrupt 500 should surface an error, got %v", err)
	}
}

// Up now names its project, so the dashboard can boot a sandbox of any
// project on screen. With no root recorded anywhere this fails cleanly
// rather than booting the wrong checkout.
func TestControlPlaneActorUpReportsAnUnresolvableProject(t *testing.T) {
	a := cpActorAgainst(t, "http://127.0.0.1:1")
	row := control.Row{Kind: control.RowSandbox, Project: "gamma", Name: "issue-7"}
	msg := drainMsg(a.Up(row))
	if l, _ := controlplane.ResultLabel(msg); l != "up" {
		t.Errorf("label = %q, want \"up\"", l)
	}
	if err := controlplane.ResultErr(msg); err == nil || !strings.Contains(err.Error(), "gamma") {
		t.Errorf("err = %v, want it to name the unresolvable project", err)
	}
}

// The step-1 review's carry-forward: the v1 actor ran the tmux probe and the
// attach bookkeeping synchronously inside Update, which froze the whole
// dashboard on a wedged container. Attach must do no I/O until bubbletea
// runs the ExecCommand — not while building the command, and not while the
// returned tea.Cmd produces its message.
func TestControlPlaneActorAttachTouchesNothingInUpdate(t *testing.T) {
	execer := &countingExecer{err: errors.New("boom: transport down")}
	tm := control.NewTmux()
	tm.Exec = execer
	a := newControlPlaneActor(control.New(control.Options{Tmux: tm}), t.TempDir())
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-attach-idle"}

	cmd := a.Attach(row)
	if cmd == nil {
		t.Fatal("Attach should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("the attach command produced no message")
	}
	if n := execer.calls.Load(); n != 0 {
		t.Errorf("Attach ran %d commands before bubbletea suspended the UI, want 0", n)
	}
}

// …and when bubbletea does run it, a probe that cannot reach the sandbox
// aborts the attach with that error rather than exec'ing into nothing.
func TestControlPlaneActorAttachAbortsWhenTheTmuxProbeFails(t *testing.T) {
	execer := &countingExecer{err: errors.New("boom: transport down")}
	tm := control.NewTmux()
	tm.Exec = execer
	a := newControlPlaneActor(control.New(control.Options{Tmux: tm}), t.TempDir())
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury",
		Container: "cspace-alpha-attach-probe-error"}

	ex := a.attachCommand(row)
	ex.SetStdin(strings.NewReader(""))
	ex.SetStdout(io.Discard)
	ex.SetStderr(io.Discard)

	err := ex.Run()
	if err == nil || !strings.Contains(err.Error(), "transport down") {
		t.Fatalf("Run() = %v, want the probe's transport error", err)
	}
	if execer.calls.Load() == 0 {
		t.Error("Run() should have probed the sandbox")
	}
	// The outcome reaches the dashboard as an attach result.
	if l, _ := controlplane.ResultLabel(attachResult(ex, err)); l != "attach" {
		t.Errorf("label = %q, want \"attach\"", l)
	}
	if got := controlplane.ResultErr(attachResult(ex, err)); got == nil {
		t.Error("attachResult should carry the error")
	}
}

// Spec, Error handling: an image built before tmux falls back to the direct
// exec "with a footer warning naming cspace image build". `cspace attach`
// prints that warning; the dashboard has to carry it too, and the only way
// out of a suspended program is the action result.
func TestAttachResultCarriesTheNoTmuxWarning(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}

	msg := attachResult(&attachExec{row: row, noTmux: true}, nil)
	if l, _ := controlplane.ResultLabel(msg); l != "attach" {
		t.Errorf("label = %q, want \"attach\"", l)
	}
	if err := controlplane.ResultErr(msg); err != nil {
		t.Errorf("a no-tmux attach is not a failure, got %v", err)
	}
	warn := controlplane.ResultWarnText(msg)
	if !strings.Contains(warn, "cspace image build") || !strings.Contains(warn, "mercury") {
		t.Errorf("warning = %q, want it to name the sandbox and the rebuild", warn)
	}

	// An ordinary tmux attach warns about nothing.
	if w := controlplane.ResultWarnText(attachResult(&attachExec{row: row}, nil)); w != "" {
		t.Errorf("warning = %q, want none when tmux was present", w)
	}
	// A failure is reported as a failure, warning or not.
	if err := controlplane.ResultErr(attachResult(&attachExec{row: row, noTmux: true},
		errors.New("exit status 1"))); err == nil {
		t.Error("an exec failure must still surface as an error")
	}
}
