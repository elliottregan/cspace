package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/tui"
)

func drain(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestTUIActorSendPostsToControlURL(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotCT = req.Header.Get("Content-Type")
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotBody = body["text"]
		w.WriteHeader(200)
		_, _ = w.Write([]byte("queued"))
	}))
	defer srv.Close()

	a := newTUIActor(nil, nil, "/home/x")
	row := tui.Row{Kind: tui.RowSandbox, Project: "alpha", Name: "mercury", ControlURL: srv.URL, Token: "tok"}
	msg := drain(a.Send(row, "hello"))

	if err := tui.ResultErr(msg); err != nil {
		t.Errorf("send should succeed, got %v", err)
	}
	if l, _ := tui.ResultLabel(msg); l != "send" {
		t.Errorf("label = %q, want \"send\"", l)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" || gotBody != "hello" {
		t.Errorf("send request: path=%q auth=%q ct=%q body=%q", gotPath, gotAuth, gotCT, gotBody)
	}
}

func TestTUIActorInterrupt409IsBenign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no active task"})
	}))
	defer srv.Close()

	a := newTUIActor(nil, nil, "/home/x")
	row := tui.Row{Kind: tui.RowSandbox, ControlURL: srv.URL, Token: "tok"}
	msg := drain(a.Interrupt(row))
	// A 409 "no active task" is not an error state — the agent was simply idle.
	if err := tui.ResultErr(msg); err != nil {
		t.Errorf("interrupt 409 should be benign, got error: %v", err)
	}
	if l, _ := tui.ResultLabel(msg); l != "interrupt" {
		t.Errorf("label = %q, want \"interrupt\"", l)
	}
}

func TestTUIActorInterrupt500Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "boom"})
	}))
	defer srv.Close()

	a := newTUIActor(nil, nil, "/home/x")
	row := tui.Row{Kind: tui.RowSandbox, ControlURL: srv.URL, Token: "tok"}
	msg := drain(a.Interrupt(row))
	if !msgHasError(msg, "boom") {
		t.Errorf("interrupt 500 should surface an error: %#v", msg)
	}
}

func msgHasError(msg tea.Msg, want string) bool {
	err := tui.ResultErr(msg)
	return err != nil && strings.Contains(err.Error(), want)
}
