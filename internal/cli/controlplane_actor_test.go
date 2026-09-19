package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/registry"
)

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
