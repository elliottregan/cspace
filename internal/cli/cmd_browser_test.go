package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/registry"
)

// withCfg temporarily swaps the package-level cfg var, restoring the prior
// value on test cleanup. Mirrors the t.Setenv/t.Cleanup pattern already used
// for restartBrowserFn (see stubRestartBrowserFn in cmd_daemon_test.go).
func withCfg(t *testing.T, c *config.Config) {
	t.Helper()
	orig := cfg
	t.Cleanup(func() { cfg = orig })
	cfg = c
}

// --- (a) in-sandbox restart: right URL, right Bearer token ---

// TestRunBrowserRestartInSandboxPostsCorrectURLAndToken covers brief case
// (a): the in-sandbox restart path must (1) self-look-up its own registry
// entry via GET /lookup/<project>/<sandbox> to fetch a Bearer token, then
// (2) POST /browser/restart/<project> with that token, and (3) report the
// response's refreshed endpoints.
func TestRunBrowserRestartInSandboxPostsCorrectURLAndToken(t *testing.T) {
	var gotRestartPath, gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/lookup/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/lookup/demo-project/sandbox-a" {
			t.Errorf("lookup path = %q, want /lookup/demo-project/sandbox-a", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(registry.Entry{
			ControlURL: "http://192.168.64.5:9000",
			Token:      "tok-secret-123",
		})
	})
	mux.HandleFunc("/browser/restart/", func(w http.ResponseWriter, req *http.Request) {
		gotRestartPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"ip":             "192.168.64.42",
			"cdpUrl":         "http://192.168.64.42:9222",
			"runServerWsUrl": "ws://192.168.64.42:3000/",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
	t.Setenv("CSPACE_PROJECT", "demo-project")
	t.Setenv("CSPACE_REGISTRY_URL", srv.URL)

	var out bytes.Buffer
	if err := runBrowserRestartInSandbox(context.Background(), &out); err != nil {
		t.Fatalf("runBrowserRestartInSandbox: unexpected error: %v", err)
	}

	if gotRestartPath != "/browser/restart/demo-project" {
		t.Errorf("restart path = %q, want /browser/restart/demo-project", gotRestartPath)
	}
	if gotAuth != "Bearer tok-secret-123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-secret-123")
	}
	if s := out.String(); !strings.Contains(s, "192.168.64.42") ||
		!strings.Contains(s, "http://192.168.64.42:9222") ||
		!strings.Contains(s, "ws://192.168.64.42:3000/") {
		t.Errorf("output missing refreshed endpoints: %s", s)
	}
}

// TestRunBrowserRestartInSandboxNonOKStatusReturnsServerText covers a
// non-2xx daemon response in both shapes browserRestartHandler
// (cmd_daemon.go) can produce: a plain-text http.Error body (e.g. the auth /
// bad-request paths) and its normal failure shape,
// {"ok":false,"error":"..."} (the restart-ladder-failed path). Either way the
// CLI must surface just the meaningful error text — not a raw JSON envelope —
// and return a non-nil error (non-zero exit at the cobra layer).
func TestRunBrowserRestartInSandboxNonOKStatusReturnsServerText(t *testing.T) {
	cases := []struct {
		name       string
		writeBody  func(w http.ResponseWriter)
		wantText   string
		wantAbsent string // must NOT appear in the surfaced error
	}{
		{
			name: "plain text body",
			writeBody: func(w http.ResponseWriter) {
				http.Error(w, "verify restarted browser sidecar: timeout", http.StatusBadGateway)
			},
			wantText: "timeout",
		},
		{
			name: "JSON-shaped ok:false body",
			writeBody: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":    false,
					"error": "verify restarted browser sidecar cspace-demo-browser: timeout",
				})
			},
			wantText:   "verify restarted browser sidecar cspace-demo-browser: timeout",
			wantAbsent: `"ok"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/lookup/", func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(registry.Entry{Token: "tok"})
			})
			mux.HandleFunc("/browser/restart/", func(w http.ResponseWriter, req *http.Request) {
				tc.writeBody(w)
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
			t.Setenv("CSPACE_PROJECT", "demo-project")
			t.Setenv("CSPACE_REGISTRY_URL", srv.URL)

			var out bytes.Buffer
			err := runBrowserRestartInSandbox(context.Background(), &out)
			if err == nil {
				t.Fatal("want error for non-2xx response, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantText)
			}
			if tc.wantAbsent != "" && strings.Contains(err.Error(), tc.wantAbsent) {
				t.Errorf("error = %q, want it to NOT contain raw JSON envelope %q", err.Error(), tc.wantAbsent)
			}
		})
	}
}

// TestRunBrowserRestartInSandboxNoRegistryURLIsClearError guards the
// self-review concern: sandbox mode without CSPACE_REGISTRY_URL must return
// a clear error, not panic or hang.
func TestRunBrowserRestartInSandboxNoRegistryURLIsClearError(t *testing.T) {
	t.Setenv("CSPACE_SANDBOX_NAME", "sandbox-a")
	t.Setenv("CSPACE_PROJECT", "demo-project")
	t.Setenv("CSPACE_REGISTRY_URL", "")

	var out bytes.Buffer
	err := runBrowserRestartInSandbox(context.Background(), &out)
	if err == nil {
		t.Fatal("want error when CSPACE_REGISTRY_URL is unset, got nil")
	}
	if !strings.Contains(err.Error(), "CSPACE_REGISTRY_URL") {
		t.Errorf("error = %q, want it to name the missing var", err.Error())
	}
}

// --- (b) host restart: calls restartBrowserFn with the config project ---

// TestRunBrowserRestartHostCallsSeamWithConfigProject covers brief case (b):
// the host path must call restartBrowserFn with cfg.Project.Name and report
// the returned BrowserSidecar's endpoints.
func TestRunBrowserRestartHostCallsSeamWithConfigProject(t *testing.T) {
	withCfg(t, &config.Config{Project: config.ProjectConfig{Name: "host-proj"}})

	var gotProject string
	stubRestartBrowserFn(t, func(ctx context.Context, project, plVersion string) (*BrowserSidecar, error) {
		gotProject = project
		return &BrowserSidecar{
			IP:             "192.168.64.7",
			CDPURL:         "http://192.168.64.7:9222",
			RunServerWSURL: "ws://192.168.64.7:3000/",
		}, nil
	})

	var out bytes.Buffer
	if err := runBrowserRestartHost(context.Background(), &out); err != nil {
		t.Fatalf("runBrowserRestartHost: unexpected error: %v", err)
	}
	if gotProject != "host-proj" {
		t.Errorf("restartBrowserFn called with project %q, want host-proj", gotProject)
	}
	if s := out.String(); !strings.Contains(s, "192.168.64.7") {
		t.Errorf("output missing refreshed endpoint: %s", s)
	}
}

// TestRunBrowserRestartHostNilConfigIsClearError guards the self-review
// concern: a nil cfg (no project loaded) must return a clear error, not
// panic.
func TestRunBrowserRestartHostNilConfigIsClearError(t *testing.T) {
	withCfg(t, nil)

	called := false
	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		called = true
		return nil, nil
	})

	var out bytes.Buffer
	err := runBrowserRestartHost(context.Background(), &out)
	if err == nil {
		t.Fatal("want error for nil cfg, got nil")
	}
	if called {
		t.Error("restartBrowserFn must not be called when project resolution fails")
	}
}

// TestRunBrowserRestartHostSeamErrorPropagates ensures a failing restart
// ladder surfaces its error rather than being swallowed.
func TestRunBrowserRestartHostSeamErrorPropagates(t *testing.T) {
	withCfg(t, &config.Config{Project: config.ProjectConfig{Name: "host-proj"}})
	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		return nil, errors.New("ladder failed: boom")
	})

	var out bytes.Buffer
	err := runBrowserRestartHost(context.Background(), &out)
	if err == nil {
		t.Fatal("want error when restartBrowserFn fails, got nil")
	}
}

// --- (c) status: injectable probe targets against local fixtures ---

// TestProbeBrowserEndpointsBothHealthy covers brief case (c): a passing CDP
// fixture (httptest 200 on /json/version) and a passing run-server WS
// fixture (startFakeWS 101) both report ok, with the exact labels the
// status command prints.
func TestProbeBrowserEndpointsBothHealthy(t *testing.T) {
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cdp.Close)
	wsAddr := startFakeWS(t, "HTTP/1.1 101 Switching Protocols\r\n\r\n")

	results := probeBrowserEndpoints(context.Background(), cdp.URL, wsAddr, 2*time.Second)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].label != "CDP :9222" || results[0].err != nil {
		t.Errorf("CDP result = %+v, want label %q and nil err", results[0], "CDP :9222")
	}
	if results[1].label != "run-server :3000" || results[1].err != nil {
		t.Errorf("run-server result = %+v, want label %q and nil err", results[1], "run-server :3000")
	}

	var out bytes.Buffer
	if ok := printBrowserStatus(&out, results); !ok {
		t.Error("printBrowserStatus: want ok=true when both probes succeed")
	}
	want := "CDP :9222 ok\nrun-server :3000 ok\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// TestProbeBrowserEndpointsBothFailing covers the failure shape: a
// non-200 CDP fixture and a non-101 WS fixture both report FAIL with a
// detail, and the aggregate reports not-ok so the caller can exit non-zero.
func TestProbeBrowserEndpointsBothFailing(t *testing.T) {
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(cdp.Close)
	wsAddr := startFakeWS(t, "HTTP/1.1 400 Bad Request\r\n\r\n")

	results := probeBrowserEndpoints(context.Background(), cdp.URL, wsAddr, 1*time.Second)

	var out bytes.Buffer
	ok := printBrowserStatus(&out, results)
	if ok {
		t.Error("printBrowserStatus: want ok=false when a probe fails")
	}
	s := out.String()
	if !strings.Contains(s, "CDP :9222 FAIL (") {
		t.Errorf("output missing CDP FAIL line: %s", s)
	}
	if !strings.Contains(s, "run-server :3000 FAIL (") {
		t.Errorf("output missing run-server FAIL line: %s", s)
	}
}

// TestBrowserSandboxHost verifies the DNS name shape the in-sandbox status
// path probes: browser.<project>.<suffix>, suffix derived from
// daemonDNSDomain with its trailing dot stripped.
func TestBrowserSandboxHost(t *testing.T) {
	if got, want := browserSandboxHost("demo-project"), "browser.demo-project.cspace.test"; got != want {
		t.Errorf("browserSandboxHost(%q) = %q, want %q", "demo-project", got, want)
	}
}

// TestBrowserStatusTargetsInSandbox verifies the in-sandbox probe target
// shape without touching a real DNS resolver.
func TestBrowserStatusTargetsInSandbox(t *testing.T) {
	t.Setenv("CSPACE_PROJECT", "demo-project")

	cdpURL, wsAddr, err := browserStatusTargets(context.Background(), true)
	if err != nil {
		t.Fatalf("browserStatusTargets: unexpected error: %v", err)
	}
	// CDP probes the loopback relay (the actual consumer path — Chrome
	// 500s name-based Host headers); WS keeps the DNS name.
	if want := "http://127.0.0.1:9222"; cdpURL != want {
		t.Errorf("cdpURL = %q, want %q", cdpURL, want)
	}
	if want := "browser.demo-project.cspace.test:3000"; wsAddr != want {
		t.Errorf("wsAddr = %q, want %q", wsAddr, want)
	}
}

// TestBrowserStatusTargetsInSandboxNoProjectIsClearError guards against a
// panic/empty-host probe when CSPACE_PROJECT is unset in sandbox mode.
func TestBrowserStatusTargetsInSandboxNoProjectIsClearError(t *testing.T) {
	t.Setenv("CSPACE_PROJECT", "")

	_, _, err := browserStatusTargets(context.Background(), true)
	if err == nil {
		t.Fatal("want error when CSPACE_PROJECT is unset, got nil")
	}
}

// TestBrowserStatusTargetsHostNilConfigIsClearError guards the host path's
// project-resolution error surfaces cleanly rather than panicking.
func TestBrowserStatusTargetsHostNilConfigIsClearError(t *testing.T) {
	withCfg(t, nil)

	_, _, err := browserStatusTargets(context.Background(), false)
	if err == nil {
		t.Fatal("want error for nil cfg, got nil")
	}
}
