package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/miekg/dns"
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
	// Reap the spawned child asynchronously so the OS releases the pid once
	// the daemon exits. Without this, the process lingers as a zombie after
	// stopRegistryDaemon's pkill succeeds (ports free, but the pid is still
	// "alive" to kill(pid, 0) until waited on) — a test-harness artifact, not
	// a production concern, since the real daemon is reparented to init.
	go func() { _ = spawner.Wait() }()

	waitForPort(t, "127.0.0.1:"+httpPort, 5*time.Second)

	// Point this test process's stopRegistryDaemon (running in-process,
	// not the spawned daemon) at the same addrs the spawned daemon bound.
	t.Setenv("CSPACE_REGISTRY_DAEMON_PORT", httpPort)
	t.Setenv("CSPACE_DAEMON_DNS_ADDR", dnsAddr)
	t.Setenv("CSPACE_DAEMON_GATEWAY_ADDR", gatewayAddr)

	if err := stopRegistryDaemon(); err != nil {
		t.Fatalf("stopRegistryDaemon() = %v, want nil", err)
	}

	// The async Wait() above races with stopRegistryDaemon returning, so
	// poll briefly for the pid to be reaped rather than checking once.
	alive := true
	for i := 0; i < 20; i++ {
		if syscall.Kill(pid, 0) != nil {
			alive = false
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if alive {
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

// TestLiveSandboxIPNegativeCache verifies that a FAILING inspect is bound
// to at most one call per name per daemonDNSTTL, same as a succeeding one.
// Without negative-caching the failure, a name whose container is
// permanently gone would re-invoke inspectContainerIP (and pay its full 2s
// timeout) on every single DNS query, defeating the "at most one inspect
// per name per TTL" bound the memo exists to provide.
func TestLiveSandboxIPNegativeCache(t *testing.T) {
	orig := inspectContainerIP
	t.Cleanup(func() { inspectContainerIP = orig })
	const container = "cspace-p-venus"
	t.Cleanup(func() { sandboxIPMemo.Delete(container) })
	sandboxIPMemo.Delete(container) // avoid colliding with any leftover entry

	var calls int
	inspectContainerIP = func(string) (string, error) {
		calls++
		return "", errContainerGone
	}

	for i := 0; i < 2; i++ {
		if got := liveSandboxIP("p", "venus", "192.168.64.9"); got != "192.168.64.9" {
			t.Errorf("call %d: want registry fallback 192.168.64.9, got %s", i, got)
		}
	}
	if calls != 1 {
		t.Errorf("want inspectContainerIP invoked exactly once (second call served from negative memo), got %d calls", calls)
	}
}

// TestMemoizedContainerIPBoundedSeam verifies that memoizedContainerIP — the
// helper shared by liveSandboxIP and lookupSidecarIP — resolves through the
// inspectContainerIP seam rather than shelling out on its own. Production
// inspectContainerIP already bounds a single inspect to a 2s ctx (see its
// doc comment); routing through it is what gives lookupSidecarIP the same
// bound, closing the gap where it used to call lookupSidecarIPCtx with an
// unbounded context.Background() directly. This suite only ever fakes
// inspectContainerIP — it must never shell out to the real `container` CLI.
func TestMemoizedContainerIPBoundedSeam(t *testing.T) {
	const container = "cspace-boundtest-browser"
	t.Cleanup(func() { sandboxIPMemo.Delete(container) })
	sandboxIPMemo.Delete(container)

	var gotName string
	stubInspectContainerIP(t, func(name string) (string, error) {
		gotName = name
		return "192.168.64.77", nil
	})

	ip, err := memoizedContainerIP(container)
	if err != nil || ip != "192.168.64.77" {
		t.Fatalf("memoizedContainerIP() = (%q, %v), want (192.168.64.77, nil)", ip, err)
	}
	if gotName != container {
		t.Errorf("inspectContainerIP called with %q, want %q", gotName, container)
	}
}

// TestMemoizedContainerIPMemoized verifies a second call for the same
// container name within daemonDNSTTL is served from the memo rather than
// re-invoking inspectContainerIP — the property lookupSidecarIP (browser +
// 3-label service DNS paths) lacked before this fix, since it called
// lookupSidecarIPCtx directly on every query with no cache at all.
func TestMemoizedContainerIPMemoized(t *testing.T) {
	const container = "cspace-memotest-browser"
	t.Cleanup(func() { sandboxIPMemo.Delete(container) })
	sandboxIPMemo.Delete(container)

	var calls int
	stubInspectContainerIP(t, func(string) (string, error) {
		calls++
		return "192.168.64.78", nil
	})

	for i := 0; i < 2; i++ {
		if ip, err := memoizedContainerIP(container); err != nil || ip != "192.168.64.78" {
			t.Fatalf("call %d: memoizedContainerIP() = (%q, %v), want (192.168.64.78, nil)", i, ip, err)
		}
	}
	if calls != 1 {
		t.Errorf("want inspectContainerIP invoked exactly once within TTL, got %d calls", calls)
	}
}

// TestMemoizedContainerIPNegativeCache verifies a failing inspect is
// negative-cached the same as a successful one, so a permanently-gone
// sidecar (or a hung apiserver) doesn't pay the inspect cost — or its
// bounded 2s worst case — on every query for the rest of the TTL window.
func TestMemoizedContainerIPNegativeCache(t *testing.T) {
	const container = "cspace-negtest-browser"
	t.Cleanup(func() { sandboxIPMemo.Delete(container) })
	sandboxIPMemo.Delete(container)

	var calls int
	stubInspectContainerIP(t, func(string) (string, error) {
		calls++
		return "", errContainerGone
	})

	for i := 0; i < 2; i++ {
		if _, err := memoizedContainerIP(container); err == nil {
			t.Errorf("call %d: want a miss error, got nil", i)
		}
	}
	if calls != 1 {
		t.Errorf("want inspectContainerIP invoked exactly once (second call served from negative memo), got %d calls", calls)
	}
}

// TestLookupSidecarIPUsesMemoizedContainerIP drives the DNS handler's actual
// sidecar-resolution entry point (lookupSidecarIP, which lookupSidecarIPFn
// defaults to) end to end, confirming it now benefits from the same
// bounded+memoized path as memoizedContainerIP above rather than the
// pre-fix unbounded, uncached lookupSidecarIPCtx(context.Background(), ...)
// call. Faking only ever happens at the inspectContainerIP seam, so this
// never shells out to the real `container` CLI.
func TestLookupSidecarIPUsesMemoizedContainerIP(t *testing.T) {
	const name = "cspace-e2etest-browser"
	t.Cleanup(func() { sandboxIPMemo.Delete(name) })
	sandboxIPMemo.Delete(name)

	var calls int
	stubInspectContainerIP(t, func(string) (string, error) {
		calls++
		return "192.168.64.79", nil
	})

	for i := 0; i < 2; i++ {
		if ip, err := lookupSidecarIP(name); err != nil || ip != "192.168.64.79" {
			t.Fatalf("call %d: lookupSidecarIP() = (%q, %v), want (192.168.64.79, nil)", i, ip, err)
		}
	}
	if calls != 1 {
		t.Errorf("want inspectContainerIP invoked exactly once (lookupSidecarIP memoized), got %d calls", calls)
	}
}

// recordingDNSWriter is a minimal dns.ResponseWriter stub that captures the
// reply passed to WriteMsg, so tests can drive daemonDNSHandler directly
// without a real network listener.
type recordingDNSWriter struct {
	msg *dns.Msg
}

func (w *recordingDNSWriter) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (w *recordingDNSWriter) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (w *recordingDNSWriter) WriteMsg(m *dns.Msg) error   { w.msg = m; return nil }
func (w *recordingDNSWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *recordingDNSWriter) Close() error                { return nil }
func (w *recordingDNSWriter) TsigStatus() error           { return nil }
func (w *recordingDNSWriter) TsigTimersOnly(bool)         {}
func (w *recordingDNSWriter) Hijack()                     {}

// dnsQuestion builds a single-question A-record query message for name.
func dnsQuestion(name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	return m
}

// stubInspectContainerIP replaces the package-level inspectContainerIP seam
// for the duration of a test so liveSandboxIP's registry-path tests never
// shell out to the real `container` CLI, and restores the original on
// cleanup.
func stubInspectContainerIP(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := inspectContainerIP
	t.Cleanup(func() { inspectContainerIP = orig })
	inspectContainerIP = fn
}

// stubLookupSidecarIPFn replaces the lookupSidecarIPFn seam for the duration
// of a test and restores the original on cleanup.
func stubLookupSidecarIPFn(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := lookupSidecarIPFn
	t.Cleanup(func() { lookupSidecarIPFn = orig })
	lookupSidecarIPFn = fn
}

// TestDaemonDNSHandlerBrowserLabel covers the new reserved "browser"
// leftmost label at the 2-label position: browser.<project>.cspace.test
// must resolve the project's shared browser sidecar via lookupSidecarIPFn,
// entirely bypassing the registry (which has no entry for the sidecar). A
// bare 1-label "browser" query must NOT be treated as reserved — it falls
// through to the ordinary sandbox-name registry path and NXDOMAINs there.
func TestDaemonDNSHandlerBrowserLabel(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	// Seed one unrelated entry to prove the browser case doesn't
	// accidentally match/consult it.
	if err := r.Register(registry.Entry{
		Project: "demo", Name: "someother", IP: "192.168.64.10", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	var lastActivity atomic.Int64

	t.Run("resolves shared browser sidecar", func(t *testing.T) {
		var gotName string
		stubLookupSidecarIPFn(t, func(name string) (string, error) {
			gotName = name
			return "192.168.64.150", nil
		})

		w := &recordingDNSWriter{}
		daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion("browser.demo."+daemonDNSDomain))

		wantName := browserSingletonName("demo")
		if gotName != wantName {
			t.Fatalf("lookupSidecarIPFn called with %q, want %q", gotName, wantName)
		}
		if w.msg == nil || len(w.msg.Answer) != 1 {
			t.Fatalf("got msg %+v, want exactly 1 answer", w.msg)
		}
		a, ok := w.msg.Answer[0].(*dns.A)
		if !ok || a.A.String() != "192.168.64.150" {
			t.Fatalf("answer = %+v, want A 192.168.64.150", w.msg.Answer[0])
		}
	})

	t.Run("sidecar lookup failure returns NXDOMAIN", func(t *testing.T) {
		stubLookupSidecarIPFn(t, func(string) (string, error) {
			return "", errors.New("container not found")
		})

		w := &recordingDNSWriter{}
		daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion("browser.demo."+daemonDNSDomain))

		if w.msg == nil || w.msg.Rcode != dns.RcodeNameError {
			t.Fatalf("got %+v, want Rcode NXDOMAIN", w.msg)
		}
	})

	t.Run("1-label browser stays NXDOMAIN", func(t *testing.T) {
		called := false
		stubLookupSidecarIPFn(t, func(string) (string, error) {
			called = true
			return "192.168.64.150", nil
		})

		w := &recordingDNSWriter{}
		daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion("browser."+daemonDNSDomain))

		if called {
			t.Fatal("lookupSidecarIPFn must not be called for a 1-label 'browser' query")
		}
		if w.msg == nil || w.msg.Rcode != dns.RcodeNameError {
			t.Fatalf("got %+v, want Rcode NXDOMAIN", w.msg)
		}
	})
}

// TestDaemonDNSHandlerServiceLabelUsesSeam guards the pre-existing 3-label
// <service>.<sandbox>.<project> resolution path, now routed through the
// lookupSidecarIPFn seam instead of calling lookupSidecarIP directly. This
// must keep behaving exactly as before: registry confirms the sandbox
// exists, then the sidecar container name is resolved through the seam.
func TestDaemonDNSHandlerServiceLabelUsesSeam(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "svcproj", Name: "svcsandbox", IP: "192.168.64.20", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	var lastActivity atomic.Int64

	// The sandbox's own IP resolution goes through the separate
	// inspectContainerIP seam (liveSandboxIP) — stub it too so this test
	// never shells out to the real `container` CLI.
	stubInspectContainerIP(t, func(string) (string, error) {
		return "", errContainerGone
	})

	var gotName string
	stubLookupSidecarIPFn(t, func(name string) (string, error) {
		gotName = name
		return "192.168.64.151", nil
	})

	w := &recordingDNSWriter{}
	daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion("browser.svcsandbox.svcproj."+daemonDNSDomain))

	wantName := "cspace-svcproj-svcsandbox-browser"
	if gotName != wantName {
		t.Fatalf("lookupSidecarIPFn called with %q, want %q", gotName, wantName)
	}
	if w.msg == nil || len(w.msg.Answer) != 1 {
		t.Fatalf("got msg %+v, want exactly 1 answer", w.msg)
	}
	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok || a.A.String() != "192.168.64.151" {
		t.Fatalf("answer = %+v, want A 192.168.64.151", w.msg.Answer[0])
	}
}

// TestDaemonDNSHandlerSandboxResolutionUnchanged guards the ordinary
// 1-label and 2-label sandbox-name registry resolution paths, which must be
// entirely unaffected by the new "browser" reserved label (they don't touch
// lookupSidecarIPFn at all).
func TestDaemonDNSHandlerSandboxResolutionUnchanged(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{
		Project: "plainproj", Name: "plainsandbox", IP: "192.168.64.30", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	var lastActivity atomic.Int64

	stubInspectContainerIP(t, func(string) (string, error) {
		return "", errContainerGone
	})
	stubLookupSidecarIPFn(t, func(string) (string, error) {
		t.Fatal("lookupSidecarIPFn must not be called for plain sandbox resolution")
		return "", nil
	})

	for _, name := range []string{
		"plainsandbox." + daemonDNSDomain,
		"plainsandbox.plainproj." + daemonDNSDomain,
	} {
		w := &recordingDNSWriter{}
		daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion(name))

		if w.msg == nil || len(w.msg.Answer) != 1 {
			t.Fatalf("%s: got msg %+v, want exactly 1 answer", name, w.msg)
		}
		a, ok := w.msg.Answer[0].(*dns.A)
		if !ok || a.A.String() != "192.168.64.30" {
			t.Fatalf("%s: answer = %+v, want A 192.168.64.30", name, w.msg.Answer[0])
		}
	}
}

// TestDaemonDNSHandlerUnknownSandboxNXDOMAIN guards the existing "no
// matching registry entry" NXDOMAIN behavior.
func TestDaemonDNSHandlerUnknownSandboxNXDOMAIN(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	var lastActivity atomic.Int64

	w := &recordingDNSWriter{}
	daemonDNSHandler(r, &lastActivity).ServeDNS(w, dnsQuestion("nosuchsandbox."+daemonDNSDomain))

	if w.msg == nil || w.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("got %+v, want Rcode NXDOMAIN", w.msg)
	}
}

// stubRestartBrowserFn replaces the restartBrowserFn seam for the duration
// of a test and restores the original on cleanup, matching
// stubInspectContainerIP/stubLookupSidecarIPFn above.
func stubRestartBrowserFn(t *testing.T, fn func(ctx context.Context, project, plVersion string) (*BrowserSidecar, error)) {
	t.Helper()
	orig := restartBrowserFn
	t.Cleanup(func() { restartBrowserFn = orig })
	restartBrowserFn = fn
}

// TestBrowserRestartHandlerLoopbackAllowed covers brief case (a): a loopback
// caller (the host CLI, via httptest.NewServer which reports RemoteAddr
// 127.0.0.1) is allowed without any Authorization header, and a successful
// restartBrowserFn's BrowserSidecar fields flow into the 200 JSON body.
func TestBrowserRestartHandlerLoopbackAllowed(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}

	var gotProject, gotPlVersion string
	stubRestartBrowserFn(t, func(ctx context.Context, project, plVersion string) (*BrowserSidecar, error) {
		gotProject, gotPlVersion = project, plVersion
		return &BrowserSidecar{
			ContainerName:  "cspace-demo-browser",
			IP:             "192.168.64.99",
			CDPURL:         "http://192.168.64.99:9222",
			RunServerWSURL: "ws://192.168.64.99:3000/",
		}, nil
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /browser/restart/{project}", browserRestartHandler(r))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/browser/restart/demo", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotProject != "demo" {
		t.Fatalf("restartBrowserFn called with project %q, want demo", gotProject)
	}
	_ = gotPlVersion // recorded for visibility only; brief doesn't constrain this value

	var body struct {
		OK             bool   `json:"ok"`
		IP             string `json:"ip"`
		CDPURL         string `json:"cdpUrl"`
		RunServerWSURL string `json:"runServerWsUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.IP != "192.168.64.99" ||
		body.CDPURL != "http://192.168.64.99:9222" ||
		body.RunServerWSURL != "ws://192.168.64.99:3000/" {
		t.Fatalf("got %+v, want ok=true with sidecar fields", body)
	}
}

// TestBrowserRestartHandlerNonLoopbackNoTokenUnauthorized covers brief case
// (b): a non-loopback caller (simulated via a crafted RemoteAddr, since
// direct handler invocation bypasses any real listener) presenting no
// Authorization header must get 401, and the restart ladder must never run.
func TestBrowserRestartHandlerNonLoopbackNoTokenUnauthorized(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{Project: "demo", Name: "mercury", Token: "tok-demo", StartedAt: time.Now()}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	called := false
	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		called = true
		return nil, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/browser/restart/demo", nil)
	req.RemoteAddr = "192.168.64.85:5555"
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()

	browserRestartHandler(r)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("restartBrowserFn must not be called when auth fails")
	}
}

// TestBrowserRestartHandlerNonLoopbackValidTokenAuthorized covers brief case
// (c): a non-loopback caller with a Bearer token matching a registry entry
// for the SAME project is authorized and gets a 200.
func TestBrowserRestartHandlerNonLoopbackValidTokenAuthorized(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{Project: "demo", Name: "mercury", Token: "tok-demo", StartedAt: time.Now()}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		return &BrowserSidecar{
			IP:             "192.168.64.10",
			CDPURL:         "http://192.168.64.10:9222",
			RunServerWSURL: "ws://192.168.64.10:3000/",
		}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/browser/restart/demo", nil)
	req.RemoteAddr = "192.168.64.85:5555"
	req.Header.Set("Authorization", "Bearer tok-demo")
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()

	browserRestartHandler(r)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestBrowserRestartHandlerTokenFromDifferentProjectUnauthorized covers
// brief case (d): a valid token, but issued to a DIFFERENT project's
// registry entry, must 401 — proves the handler checks Project == project,
// not merely "does this token exist anywhere".
func TestBrowserRestartHandlerTokenFromDifferentProjectUnauthorized(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}
	if err := r.Register(registry.Entry{Project: "other", Name: "venus", Token: "tok-other", StartedAt: time.Now()}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	called := false
	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		called = true
		return nil, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/browser/restart/demo", nil)
	req.RemoteAddr = "192.168.64.85:5555"
	req.Header.Set("Authorization", "Bearer tok-other")
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()

	browserRestartHandler(r)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("restartBrowserFn must not be called for a token belonging to a different project")
	}
}

// TestBrowserRestartHandlerLadderErrorReturns502 covers brief case (e): when
// restartBrowserFn returns an error, the handler must respond 502 with
// {"ok":false,"error":...} rather than leaking a 500 or crashing.
func TestBrowserRestartHandlerLadderErrorReturns502(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}

	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		return nil, errors.New("boom: container wedged")
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /browser/restart/{project}", browserRestartHandler(r))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/browser/restart/demo", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK || !strings.Contains(body.Error, "boom") {
		t.Fatalf("got %+v, want ok=false with error containing 'boom'", body)
	}
}

// TestBrowserRestartHandlerEmptyProjectBadRequest covers the brief's 400
// case: an empty project path value (simulated via SetPathValue, since the
// mux itself won't route a trailing-slash-only path to this pattern) must
// 400 before any auth check or ladder call.
func TestBrowserRestartHandlerEmptyProjectBadRequest(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}

	called := false
	stubRestartBrowserFn(t, func(context.Context, string, string) (*BrowserSidecar, error) {
		called = true
		return nil, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/browser/restart/", nil)
	req.SetPathValue("project", "")
	rec := httptest.NewRecorder()

	browserRestartHandler(r)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if called {
		t.Fatal("restartBrowserFn must not be called for an empty project")
	}
}

// TestBrowserRestartHandlerSerializesPerProject verifies the per-project
// sync.Map-of-*sync.Mutex serialization: two concurrent requests for the
// SAME project must not run restartBrowserFn overlapping — the second must
// wait for the first to finish. We assert this by having the fake ladder
// record whether it ever observed another call already in flight.
func TestBrowserRestartHandlerSerializesPerProject(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	r := &registry.Registry{Path: regPath}

	var inFlight atomic.Int32
	var overlapped atomic.Bool
	release := make(chan struct{})
	stubRestartBrowserFn(t, func(ctx context.Context, project, plVersion string) (*BrowserSidecar, error) {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		defer inFlight.Add(-1)
		<-release
		return &BrowserSidecar{IP: "192.168.64.1"}, nil
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /browser/restart/{project}", browserRestartHandler(r))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	post := func(done chan<- struct{}) {
		resp, err := http.Post(srv.URL+"/browser/restart/demo", "application/json", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
		close(done)
	}

	done1, done2 := make(chan struct{}), make(chan struct{})
	go post(done1)
	// Give the first request time to enter the handler and block inside the
	// fake ladder on <-release before starting the second — this guarantees
	// the second request is the one contending for the per-project lock
	// rather than racing to get there first.
	time.Sleep(100 * time.Millisecond)
	go post(done2)
	// Give the second request time to reach (and block on) the per-project
	// mutex before we unblock the first.
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-done1
	<-done2

	if overlapped.Load() {
		t.Fatal("restartBrowserFn ran concurrently for the same project; want per-project serialization")
	}
}
