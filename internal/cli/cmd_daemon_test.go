package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHealthReportsVersion verifies /health reports the running binary's
// Version, which ensureRegistryDaemon uses to distinguish "a daemon is up"
// from "a daemon matching this build is up".
func TestHealthReportsVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "v9.9.9-test"

	rec := httptest.NewRecorder()
	healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Version != "v9.9.9-test" {
		t.Fatalf("got %+v, want ok+version v9.9.9-test", body)
	}
}

// TestDaemonHealthVersion covers both branches of the reuse check:
// a reachable daemon whose /health decodes to a version, and the "nothing is
// listening" case ensureRegistryDaemon relies on to decide to spawn.
func TestDaemonHealthVersion(t *testing.T) {
	t.Run("reachable daemon reports its version", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port

		mux := http.NewServeMux()
		mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"version":"old"}`))
		})
		srv := &http.Server{Handler: mux}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })

		t.Setenv("CSPACE_REGISTRY_DAEMON_PORT", strconv.Itoa(port))

		v, ok := daemonHealthVersion(2 * time.Second)
		if !ok || v != "old" {
			t.Fatalf("daemonHealthVersion() = (%q, %v), want (\"old\", true)", v, ok)
		}
	})

	t.Run("nothing listening returns false", func(t *testing.T) {
		// Grab a free port, then release it immediately so nothing answers.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}

		t.Setenv("CSPACE_REGISTRY_DAEMON_PORT", strconv.Itoa(port))

		if v, ok := daemonHealthVersion(200 * time.Millisecond); ok {
			t.Fatalf("daemonHealthVersion() = (%q, true), want ok=false when nothing is listening", v)
		}
	})
}

// TestWaitPortFree covers both branches: a port that's already free returns
// promptly, and a port held by a real listener blocks until the deadline
// (not indefinitely, and not before the port is actually released).
func TestWaitPortFree(t *testing.T) {
	t.Run("already free returns promptly", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		waitPortFree(addr, 2*time.Second)
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("waitPortFree on an already-free port took %s, want a prompt return", elapsed)
		}
	})

	t.Run("held port blocks until deadline", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		addr := ln.Addr().String()

		start := time.Now()
		waitPortFree(addr, 300*time.Millisecond)
		elapsed := time.Since(start)
		if elapsed < 250*time.Millisecond {
			t.Fatalf("waitPortFree returned after %s while the port was still held, want it to wait out its ~300ms deadline", elapsed)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("waitPortFree took %s, far past its 300ms deadline", elapsed)
		}
	})
}

// realDaemonServeRunning reports whether a process matching stopRegistryDaemon's
// `pkill -f "daemon serve"` pattern is already running on this machine. It's
// used to skip tests that would otherwise call the real, system-wide
// stopRegistryDaemon and risk killing a developer's live cspace daemon as
// collateral damage — pkill -f is not scoped to any PID a test spawns itself.
func realDaemonServeRunning(t *testing.T) (string, bool) {
	t.Helper()
	out, err := exec.Command("pgrep", "-fl", "daemon serve").CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err == nil && trimmed != "" {
		return trimmed, true
	}
	return "", false
}

// TestStopRegistryDaemonPkillNoMatches is the minimum required coverage from
// the task brief: pkill exit code 1 (no matches) must be treated as success,
// not an error.
func TestStopRegistryDaemonPkillNoMatches(t *testing.T) {
	if procs, running := realDaemonServeRunning(t); running {
		t.Skipf("a real 'daemon serve' process is already running; skipping stopRegistryDaemon "+
			"to avoid killing it (pkill -f is system-wide, not scoped to this test):\n%s", procs)
	}

	if err := stopRegistryDaemon(); err != nil {
		t.Fatalf("stopRegistryDaemon() with no matching process = %v, want nil (pkill exit 1 == no matches == success)", err)
	}
}

// TestStopRegistryDaemonKillsMatchingProcessAndFreesPorts spawns a real
// `<bin> daemon serve` process on isolated ports and verifies
// stopRegistryDaemon actually kills it AND blocks until its HTTP/DNS ports
// are free, per the brief's race-safety requirement. Guarded like the test
// above: pkill -f "daemon serve" is system-wide, so this only runs when no
// other matching process already exists on the machine.
func TestStopRegistryDaemonKillsMatchingProcessAndFreesPorts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + spawns real processes")
	}
	if procs, running := realDaemonServeRunning(t); running {
		t.Skipf("a real 'daemon serve' process is already running; skipping to avoid killing it "+
			"as collateral damage (pkill -f is system-wide):\n%s", procs)
	}

	bin := buildCspaceForTest(t)
	home := t.TempDir()

	// Fixed, distinct-from-other-tests ports so this daemon can't collide
	// with a developer's real one (6280/5354) or with
	// TestDaemonSurvivesSpawnerExit's ports (6299/15354/15355).
	const (
		httpPort    = "6295"
		dnsAddr     = "127.0.0.1:15364"
		gatewayAddr = "127.0.0.1:15365"
	)

	env := append(os.Environ(),
		"HOME="+home,
		"CSPACE_REGISTRY_DAEMON_PORT="+httpPort,
		"CSPACE_DAEMON_DNS_ADDR="+dnsAddr,
		"CSPACE_DAEMON_GATEWAY_ADDR="+gatewayAddr,
		"CSPACE_REGISTRY_DAEMON_IDLE=1h",
	)
	spawner := exec.Command(bin, "daemon", "serve")
	spawner.Env = env
	logf, err := os.Create(filepath.Join(home, "d.log"))
	if err != nil {
		t.Fatal(err)
	}
	spawner.Stdout, spawner.Stderr = logf, logf
	spawner.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := spawner.Start(); err != nil {
		t.Fatal(err)
	}
	pid := spawner.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = logf.Close()
	})

	waitForPort(t, "127.0.0.1:"+httpPort, 5*time.Second)

	// Point this test process's stopRegistryDaemon (running in-process,
	// not the spawned daemon) at the same addrs the spawned daemon bound.
	t.Setenv("CSPACE_REGISTRY_DAEMON_PORT", httpPort)
	t.Setenv("CSPACE_DAEMON_DNS_ADDR", dnsAddr)
	t.Setenv("CSPACE_DAEMON_GATEWAY_ADDR", gatewayAddr)

	if err := stopRegistryDaemon(); err != nil {
		t.Fatalf("stopRegistryDaemon() = %v, want nil", err)
	}

	if syscall.Kill(pid, 0) == nil {
		t.Error("daemon process still alive after stopRegistryDaemon returned")
	}
	if c, derr := net.DialTimeout("tcp", "127.0.0.1:"+httpPort, 200*time.Millisecond); derr == nil {
		_ = c.Close()
		t.Error("HTTP port still accepting connections after stopRegistryDaemon returned")
	}
	if c, derr := net.DialTimeout("tcp", dnsAddr, 200*time.Millisecond); derr == nil {
		_ = c.Close()
		t.Error("DNS port still accepting connections after stopRegistryDaemon returned")
	}
}

// TestLiveSandboxIP verifies liveSandboxIP prefers the live inspected IP
// over the registry's (potentially stale, post-vmnet-reassignment) IP, and
// falls back to the registry IP when the inspect fails.
//
// Both assertions target the same (project, name) so they share a memo
// entry; the memo is deliberately TTL'd (see liveSandboxIP) to bound
// inspects on the DNS hot path, so the entry is cleared between the two
// stub swaps below to simulate TTL expiry rather than asserting a stale
// cache hit.
func TestLiveSandboxIP(t *testing.T) {
	orig := inspectContainerIP
	t.Cleanup(func() { inspectContainerIP = orig })
	const container = "cspace-p-mercury"
	t.Cleanup(func() { sandboxIPMemo.Delete(container) })

	inspectContainerIP = func(string) (string, error) { return "192.168.64.42", nil }
	if got := liveSandboxIP("p", "mercury", "192.168.64.9"); got != "192.168.64.42" {
		t.Errorf("want live 192.168.64.42, got %s", got)
	}

	sandboxIPMemo.Delete(container) // simulate TTL expiry before the next inspect
	inspectContainerIP = func(string) (string, error) { return "", errContainerGone }
	if got := liveSandboxIP("p", "mercury", "192.168.64.9"); got != "192.168.64.9" {
		t.Errorf("want registry fallback 192.168.64.9, got %s", got)
	}
}
