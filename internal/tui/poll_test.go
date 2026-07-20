package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

type fakeLister struct {
	out []applecontainer.ContainerSummary
	err error
}

func (f fakeLister) List(context.Context) ([]applecontainer.ContainerSummary, error) {
	return f.out, f.err
}

func writeRegistry(t *testing.T, project, name, controlURL, token string) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "reg.json")
	r := &registry.Registry{Path: path}
	if err := r.Register(registry.Entry{
		Project: project, Name: name, ControlURL: controlURL, Token: token, State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func TestPollFansOutStatusAndCorrelates(t *testing.T) {
	var gotAuth string
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "session": "primary", "state": "idle", "queueDepth": 0,
		})
	}))
	defer control.Close()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": "1.0.0-rc.40"})
	}))
	defer daemon.Close()

	reg := writeRegistry(t, "alpha", "mercury", control.URL, "tok-xyz")
	lister := fakeLister{out: []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
	}}
	now := func() time.Time { return time.Unix(1_000_000, 0) }
	p := NewPoller(lister, reg, daemon.URL, now)

	snap := p.Poll(context.Background())

	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("status Authorization = %q, want Bearer tok-xyz", gotAuth)
	}
	if !snap.Daemon.Reachable || snap.Daemon.Version != "1.0.0-rc.40" {
		t.Errorf("daemon = %+v", snap.Daemon)
	}
	// project header + sandbox row
	if len(snap.Rows) != 2 || snap.Rows[1].State != StateRunning || !snap.Rows[1].Agent.Reachable {
		t.Fatalf("rows = %+v", snap.Rows)
	}
}

func TestPollListErrorCarriedAndDaemonUnreachable(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	lister := fakeLister{err: os.ErrPermission}
	now := func() time.Time { return time.Unix(0, 0) }
	p := NewPoller(lister, reg, "http://127.0.0.1:1", now) // unreachable daemon
	snap := p.Poll(context.Background())
	if snap.Err == nil {
		t.Error("want Err carried from lister failure")
	}
	if snap.Daemon.Reachable {
		t.Error("daemon should be unreachable")
	}
}

func TestPollProbesBrowserHealth(t *testing.T) {
	// A CDP /json/version stub standing in for the browser sidecar.
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/json/version" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Browser": "Chrome/140.0"})
	}))
	defer cdp.Close()

	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	p := NewPoller(fakeLister{}, reg, "http://127.0.0.1:1", func() time.Time { return time.Unix(0, 0) })
	// Redirect the CDP probe at the stub (production uses the fixed :9222 port,
	// which httptest can't bind — the browserCDPURL seam exists for exactly this).
	p.browserCDPURL = func(ip string) string { return cdp.URL + "/json/version" }

	// A running "-browser" container is probed and mapped by container name;
	// a non-browser container and a stopped browser are skipped.
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-browser", State: "running", IP: "10.0.0.9"},
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
		{Name: "cspace-beta-browser", State: "stopped", IP: ""},
	}
	m := p.fetchBrowserHealth(context.Background(), containers)
	if got := m["cspace-alpha-browser"]; !got.Reachable || got.Version != "Chrome/140.0" {
		t.Errorf("browser health = %+v, want reachable Chrome/140.0", got)
	}
	if _, ok := m["cspace-alpha-mercury"]; ok {
		t.Error("non-browser container should not be probed")
	}
	if _, ok := m["cspace-beta-browser"]; ok {
		t.Error("stopped browser should not be probed")
	}
}
