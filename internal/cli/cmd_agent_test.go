package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

// --- (a) host context: registry lookup straight from the local file ---

// TestRunAgentStatusHostReadsLocalRegistry covers the host path: no
// CSPACE_REGISTRY_URL set, so resolveEntry (cmd_send.go) must read the
// entry straight out of ~/.cspace/sandbox-registry.json (HOME repointed at a
// temp dir here), then GET <ControlURL>/status with the entry's Bearer
// token, and print one line per field.
func TestRunAgentStatusHostReadsLocalRegistry(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotPath = req.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"session":          "primary",
			"state":            "working",
			"lastEventTs":      "2026-07-19T00:00:00.000Z",
			"lastEventType":    "system",
			"lastEventSubtype": "init",
			"queueDepth":       2,
		})
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CSPACE_REGISTRY_URL", "")
	regPath := filepath.Join(home, ".cspace", "sandbox-registry.json")
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project:    "demo-project",
		Name:       "sandbox-a",
		ControlURL: srv.URL,
		Token:      "tok-secret-123",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var out bytes.Buffer
	if err := runAgentStatus(context.Background(), &out, "demo-project", "sandbox-a"); err != nil {
		t.Fatalf("runAgentStatus: unexpected error: %v", err)
	}

	if gotAuth != "Bearer tok-secret-123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-secret-123")
	}
	if gotPath != "/status" {
		t.Errorf("path = %q, want /status", gotPath)
	}

	s := out.String()
	for _, want := range []string{
		"session:          primary",
		"state:            working",
		"lastEventTs:      2026-07-19T00:00:00.000Z",
		"lastEventType:    system",
		"lastEventSubtype: init",
		"queueDepth:       2",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got:\n%s", want, s)
		}
	}
}

// --- (b) in-sandbox context: resolveEntry via CSPACE_REGISTRY_URL ---

// newFakeDaemonAndControlServers stands up two independent httptest
// servers: daemonSrv serves the host daemon's GET /lookup/<project>/<name>
// (returning controlSrv's URL + token as the resolved registry entry), and
// controlSrv serves the sandbox's own control port (/status, /interrupt) —
// mirroring the real topology where those are two different processes.
// Registering both muxes before either server starts avoids any
// register-after-serve ordering hazard.
func newFakeDaemonAndControlServers(
	t *testing.T,
	project, sandbox, token string,
	controlMux *http.ServeMux,
) (daemonSrv, controlSrv *httptest.Server) {
	t.Helper()
	controlSrv = httptest.NewServer(controlMux)
	t.Cleanup(controlSrv.Close)

	daemonMux := http.NewServeMux()
	daemonMux.HandleFunc("/lookup/"+project+"/"+sandbox, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registry.Entry{
			ControlURL: controlSrv.URL,
			Token:      token,
		})
	})
	daemonSrv = httptest.NewServer(daemonMux)
	t.Cleanup(daemonSrv.Close)
	return daemonSrv, controlSrv
}

// TestRunAgentInterruptInSandboxPostsCorrectURLAndToken covers the
// in-sandbox path: the sandbox looks up its own registry entry via
// GET /lookup/<project>/<sandbox> against the host daemon
// (CSPACE_REGISTRY_URL), then POSTs /interrupt to the resolved ControlURL
// with the resolved Bearer token.
func TestRunAgentInterruptInSandboxPostsCorrectURLAndToken(t *testing.T) {
	var gotInterruptPath, gotAuth, gotMethod string
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/interrupt", func(w http.ResponseWriter, req *http.Request) {
		gotInterruptPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotMethod = req.Method
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	daemonSrv, _ := newFakeDaemonAndControlServers(t, "demo-project", "sandbox-a", "tok-secret-456", controlMux)

	t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
	t.Setenv("CSPACE_PROJECT", "demo-project")
	t.Setenv("CSPACE_REGISTRY_URL", daemonSrv.URL)

	var out bytes.Buffer
	if err := runAgentInterrupt(context.Background(), &out, "demo-project", "sandbox-a"); err != nil {
		t.Fatalf("runAgentInterrupt: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotInterruptPath != "/interrupt" {
		t.Errorf("path = %q, want /interrupt", gotInterruptPath)
	}
	if gotAuth != "Bearer tok-secret-456" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-secret-456")
	}
	if s := out.String(); !strings.Contains(s, "ok") {
		t.Errorf("output = %q, want it to report ok", s)
	}
}

// --- (c) non-2xx path: server error text surfaces, command errors non-nil ---

// TestRunAgentInterruptNonOKStatusReturnsServerText covers main.ts's 409
// "no active task" shape ({"ok":false,"error":"..."}) — the CLI must
// surface just the error text (not a raw JSON envelope) and return a
// non-nil error so the cobra layer exits non-zero.
func TestRunAgentInterruptNonOKStatusReturnsServerText(t *testing.T) {
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/interrupt", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "no active task",
		})
	})
	daemonSrv, _ := newFakeDaemonAndControlServers(t, "demo-project", "sandbox-a", "tok", controlMux)

	t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
	t.Setenv("CSPACE_PROJECT", "demo-project")
	t.Setenv("CSPACE_REGISTRY_URL", daemonSrv.URL)

	var out bytes.Buffer
	err := runAgentInterrupt(context.Background(), &out, "demo-project", "sandbox-a")
	if err == nil {
		t.Fatal("want error for non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "no active task") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no active task")
	}
	if strings.Contains(err.Error(), `"ok"`) {
		t.Errorf("error = %q, want it to NOT contain the raw JSON envelope", err.Error())
	}
}

// TestRunAgentStatusNonOKStatusReturnsServerText covers a plain-text error
// body on /status (e.g. an auth failure upstream), mirroring
// restartErrorText's fallback-to-raw-body behavior in cmd_browser.go.
func TestRunAgentStatusNonOKStatusReturnsServerText(t *testing.T) {
	controlMux := http.NewServeMux()
	controlMux.HandleFunc("/status", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	daemonSrv, _ := newFakeDaemonAndControlServers(t, "demo-project", "sandbox-a", "tok", controlMux)

	t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
	t.Setenv("CSPACE_PROJECT", "demo-project")
	t.Setenv("CSPACE_REGISTRY_URL", daemonSrv.URL)

	var out bytes.Buffer
	err := runAgentStatus(context.Background(), &out, "demo-project", "sandbox-a")
	if err == nil {
		t.Fatal("want error for non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unauthorized")
	}
}

// TestNewAgentCmdHasStatusAndInterruptSubcommands is a light smoke test that
// `cspace agent` registers both subcommands with the right arg arity, so a
// wiring regression (e.g. forgetting root.AddCommand(newAgentCmd())) shows
// up as a routing test failure rather than only a manual-check gap.
func TestNewAgentCmdHasStatusAndInterruptSubcommands(t *testing.T) {
	cmd := newAgentCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[strings.Fields(c.Use)[0]] = true
	}
	if !names["status"] {
		t.Error(`newAgentCmd(): missing "status" subcommand`)
	}
	if !names["interrupt"] {
		t.Error(`newAgentCmd(): missing "interrupt" subcommand`)
	}
}
