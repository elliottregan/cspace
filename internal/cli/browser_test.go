package cli

import (
	"context"
	"errors"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBrowserSidecarRunArgsSetsResourceCaps guards against the sidecar
// falling back to Apple Container's default 1 GiB, which OOM-wedged the
// shared browser under e2e load (cs-finding
// 2026-07-17-browser-sidecar-runs-on-default-1gib-and-ooms-under-e2e-load).
func TestBrowserSidecarRunArgsSetsResourceCaps(t *testing.T) {
	args := browserSidecarRunArgs("cspace-resume-redux-browser", "1.60.0", "192.168.65.1")

	imageIdx := slices.Index(args, browserImage("1.60.0"))
	if imageIdx < 0 {
		t.Fatalf("image ref missing from args: %v", args)
	}

	for flag, want := range map[string]string{
		"--memory": "4096MiB",
		"--cpus":   "4",
	} {
		i := slices.Index(args, flag)
		if i < 0 {
			t.Fatalf("%s flag missing from args: %v", flag, args)
		}
		if i > imageIdx {
			t.Errorf("%s must precede the image ref (flag at %d, image at %d)", flag, i, imageIdx)
		}
		if got := args[i+1]; got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}

	// Extraction must preserve the existing invocation shape.
	for _, want := range []string{"run", "-d", "--name", "cspace-resume-redux-browser",
		"--label", "cspace.playwright-version=1.60.0", "--dns"} {
		if !slices.Contains(args, want) {
			t.Errorf("expected %q in args: %v", want, args)
		}
	}
}

func TestBrowserSingletonName(t *testing.T) {
	if got := browserSingletonName("resume-redux"); got != "cspace-resume-redux-browser" {
		t.Errorf("got %q, want cspace-resume-redux-browser", got)
	}
}

// startFakeWS returns addr of a listener that, per connection, sends
// `response` after reading the request (empty response = accept then hang).
func startFakeWS(t *testing.T, response string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 1024)
				_, _ = c.Read(buf)
				if response != "" {
					_, _ = c.Write([]byte(response))
				}
				// hang until test cleanup closes the listener
				time.Sleep(10 * time.Second)
				_ = c.Close()
			}(c)
		}
	}()
	return l.Addr().String()
}

// startFakeWSCapture returns addr of a listener that responds 101 to every
// connection and pushes the raw request bytes it received onto reqs (one
// send per connection). Lets a test inspect exactly what wsHandshakeOnce put
// on the wire — in particular the Sec-WebSocket-Key header value.
func startFakeWSCapture(t *testing.T, reqs chan<- string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				reqs <- string(buf[:n])
				_, _ = c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n\r\n"))
			}(c)
		}
	}()
	return l.Addr().String()
}

// secWebSocketKeyRE matches a conforming RFC 6455 Sec-WebSocket-Key: the
// base64 encoding of exactly 16 raw bytes is always 22 base64 chars followed
// by "==" padding. This is also the exact pattern the Node `ws` library
// (which Playwright's run-server uses) validates against, rejecting anything
// else with HTTP 400 — see the sidecar-restart incident that motivated this
// test.
var secWebSocketKeyRE = regexp.MustCompile(`(?m)^Sec-WebSocket-Key: ([+/0-9A-Za-z]{22}==)\r?$`)

// TestWaitForRunServerWSKeyFormat guards against a regression where
// wsHandshakeOnce's fixed Sec-WebSocket-Key was derived from 17 raw bytes
// instead of 16, producing a 24-char base64 value with a single "="
// (non-conformant). The Node `ws` library validates the header against
// ^[+/0-9A-Za-z]{22}==$ and returns HTTP 400 for anything else, which made
// waitForRunServerWS misreport a healthy run-server as dead.
func TestWaitForRunServerWSKeyFormat(t *testing.T) {
	reqs := make(chan string, 1)
	addr := startFakeWSCapture(t, reqs)

	if err := wsHandshakeOnce(addr, 3*time.Second); err != nil {
		t.Fatalf("wsHandshakeOnce: unexpected error: %v", err)
	}

	var raw string
	select {
	case raw = <-reqs:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for captured request")
	}

	m := secWebSocketKeyRE.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("Sec-WebSocket-Key header missing or malformed in request:\n%s", raw)
	}
	if !secWebSocketKeyRE.MatchString(raw) {
		t.Errorf("Sec-WebSocket-Key %q does not match RFC 6455 form ^[+/0-9A-Za-z]{22}==$", m[1])
	}
}

func TestWaitForRunServerWS(t *testing.T) {
	ok := startFakeWS(t, "HTTP/1.1 101 Switching Protocols\r\n\r\n")
	if err := waitForRunServerWS(context.Background(), ok, 3*time.Second); err != nil {
		t.Errorf("101 fixture: want nil, got %v", err)
	}
	bad := startFakeWS(t, "HTTP/1.1 400 Bad Request\r\n\r\n")
	if err := waitForRunServerWS(context.Background(), bad, 2*time.Second); err == nil {
		t.Error("400 fixture: want error, got nil")
	}
	hang := startFakeWS(t, "") // accepts TCP, never answers — the incident shape
	if err := waitForRunServerWS(context.Background(), hang, 2*time.Second); err == nil {
		t.Error("hang fixture: want error, got nil")
	}
}

// TestBrowserEnvURLs pins the exact strings cmd_up.go injects into a
// sandbox's env. The WS endpoint carries the stable DNS name (restart-safe,
// cs-finding 2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path).
// The CDP endpoints carry the in-sandbox loopback relay instead: Chrome's
// DevTools HTTP endpoint rejects Host headers that aren't an IP or
// localhost, so the DNS name can never work for CDP consumers (cs-finding
// 2026-07-19-chrome-cdp-rejects-dns-name-host-header). The entrypoint's
// relay dials the DNS name per connection, preserving restart-safety.
func TestBrowserEnvURLs(t *testing.T) {
	cdpURL, wsURL := browserEnvURLs("demo")
	if want := "http://127.0.0.1:9222"; cdpURL != want {
		t.Errorf("cdpURL = %q, want %q", cdpURL, want)
	}
	if want := "ws://browser.demo.cspace.test:3000/"; wsURL != want {
		t.Errorf("wsURL = %q, want %q", wsURL, want)
	}
}

func TestWorkspaceFriendlyHost(t *testing.T) {
	cases := map[string][2]string{
		"mercury.resume-redux.cspace.test": {"mercury", "resume-redux"},
		"venus.demo.cspace.test":           {"Venus", "Demo"}, // lowercased
	}
	for want, in := range cases {
		if got := workspaceFriendlyHost(in[0], in[1]); got != want {
			t.Errorf("workspaceFriendlyHost(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// --- Restart escalation ladder (spec §3) ---------------------------------

// scriptedExec is a fake browserExecCmd. It records every invocation and
// returns canned outcomes keyed by the substrate/process verb. `container
// inspect` responses are drawn from inspectStatus in FIFO order (the last
// element repeats once exhausted) so a test can drive the running → running →
// not-running progression the split-brain ladder polls for.
type scriptedExec struct {
	mu         sync.Mutex
	calls      [][]string
	inspectSt  []string // FIFO container states; "missing" => "[]"; last repeats
	inspectIdx int

	stopErr  error
	killOut  string
	killErr  error
	pgrepOut string
	pgrepErr error
}

func (s *scriptedExec) fn(_ context.Context, name string, args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]string{name}, args...))
	switch {
	case name == "container" && len(args) > 0 && args[0] == "inspect":
		return s.nextInspect(), nil
	case name == "container" && len(args) > 0 && args[0] == "stop":
		return "", s.stopErr
	case name == "container" && len(args) > 0 && args[0] == "kill":
		return s.killOut, s.killErr
	case name == "pgrep":
		return s.pgrepOut, s.pgrepErr
	default: // container start, container run, kill -9, ...
		return "", nil
	}
}

func (s *scriptedExec) nextInspect() string {
	if len(s.inspectSt) == 0 {
		return "[]"
	}
	idx := s.inspectIdx
	if idx >= len(s.inspectSt) {
		idx = len(s.inspectSt) - 1
	}
	s.inspectIdx++
	return fakeInspectJSON(s.inspectSt[idx])
}

func (s *scriptedExec) snapshot() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.calls))
	copy(out, s.calls)
	return out
}

func fakeInspectJSON(status string) string {
	if status == "missing" {
		return "[]"
	}
	// Apple Container 1.1.x inspect shape: runtime state (state word,
	// networks) nests under `status`; the version label sits under
	// configuration.labels. Covers the fields the three seam-routed parsers
	// read — containerStateRunning (status.state), waitForBrowserIP
	// (status.networks[].ipv4Address), sidecarVersion (the label marker).
	return `[{"id":"sidecar",` +
		`"configuration":{"labels":{"cspace.playwright-version":"1.59.0"}},` +
		`"status":{"state":"` + status + `",` +
		`"networks":[{"ipv4Address":"192.0.2.7/24"}]}}]`
}

func swapBrowserExec(t *testing.T, fn func(context.Context, string, ...string) (string, error)) {
	t.Helper()
	orig := browserExecCmd
	browserExecCmd = fn
	t.Cleanup(func() { browserExecCmd = orig })
}

func swapVerifyBrowser(t *testing.T, fn func(context.Context, *BrowserSidecar) error) {
	t.Helper()
	orig := verifyBrowserFn
	verifyBrowserFn = fn
	t.Cleanup(func() { verifyBrowserFn = orig })
}

func hasCall(calls [][]string, pred func([]string) bool) bool {
	for _, c := range calls {
		if pred(c) {
			return true
		}
	}
	return false
}

func isVerb(binary string, verb ...string) func([]string) bool {
	return func(c []string) bool {
		if len(c) < 1+len(verb) || c[0] != binary {
			return false
		}
		return slices.Equal(c[1:1+len(verb)], verb)
	}
}

// firstIndex returns the index of the first call matching pred, or -1.
func firstIndex(calls [][]string, pred func([]string) bool) int {
	return slices.IndexFunc(calls, pred)
}

// TestRestartBrowserSidecarCleanStopStart is scenario (a): a healthy stop→start
// cycle must go stop → state-check → start and NEVER escalate to pgrep/kill.
func TestRestartBrowserSidecarCleanStopStart(t *testing.T) {
	fake := &scriptedExec{inspectSt: []string{"running", "stopped"}}
	swapBrowserExec(t, fake.fn)
	verifyCalled := false
	swapVerifyBrowser(t, func(_ context.Context, bs *BrowserSidecar) error {
		verifyCalled = true
		bs.IP = "10.0.0.9"
		return nil
	})

	bs, err := restartBrowserSidecar(context.Background(), "demo", "1.59.0")
	if err != nil {
		t.Fatalf("restartBrowserSidecar: %v", err)
	}
	if bs == nil || bs.ContainerName != "cspace-demo-browser" {
		t.Fatalf("bad sidecar: %+v", bs)
	}
	if !verifyCalled {
		t.Error("verifyBrowserFn was not invoked")
	}

	calls := fake.snapshot()
	if !hasCall(calls, isVerb("container", "stop")) {
		t.Error("expected a `container stop` call")
	}
	if !hasCall(calls, isVerb("container", "start")) {
		t.Error("expected a `container start` call")
	}
	stopIdx := firstIndex(calls, isVerb("container", "stop"))
	startIdx := firstIndex(calls, isVerb("container", "start"))
	if stopIdx < 0 || startIdx < 0 || stopIdx > startIdx {
		t.Errorf("stop must precede start (stop=%d start=%d)", stopIdx, startIdx)
	}
	if hasCall(calls, func(c []string) bool { return c[0] == "pgrep" }) {
		t.Error("clean path must not pgrep")
	}
	if hasCall(calls, func(c []string) bool { return c[0] == "kill" }) {
		t.Error("clean path must not kill host process")
	}
	if hasCall(calls, isVerb("container", "kill")) {
		t.Error("clean path must not `container kill`")
	}
}

// TestRestartBrowserSidecarSplitBrain is scenario (b): stop errors, kill errors
// "not running", state reports running after both — so the ladder must pgrep
// the host runtime process (pattern carrying the container name), kill -9 it,
// poll until not-running, then start.
func TestRestartBrowserSidecarSplitBrain(t *testing.T) {
	fake := &scriptedExec{
		inspectSt: []string{"running", "running", "running", "stopped"},
		stopErr:   errors.New("Error: stop: dead exec session"),
		killOut:   "Error: cannot kill container cspace-demo-browser: container is not running",
		killErr:   errors.New("exit status 1"),
		pgrepOut:  "54321\n",
	}
	swapBrowserExec(t, fake.fn)
	swapVerifyBrowser(t, func(_ context.Context, _ *BrowserSidecar) error { return nil })

	bs, err := restartBrowserSidecar(context.Background(), "demo", "1.59.0")
	if err != nil {
		t.Fatalf("restartBrowserSidecar: %v", err)
	}
	if bs == nil {
		t.Fatal("nil sidecar")
	}

	calls := fake.snapshot()
	if !hasCall(calls, isVerb("container", "kill")) {
		t.Error("expected a `container kill` escalation")
	}
	pgrepIdx := firstIndex(calls, func(c []string) bool { return c[0] == "pgrep" })
	if pgrepIdx < 0 {
		t.Fatal("expected a pgrep call for split-brain teardown")
	}
	pgrep := calls[pgrepIdx]
	if !slices.ContainsFunc(pgrep, func(a string) bool { return strings.Contains(a, "cspace-demo-browser") }) {
		t.Errorf("pgrep pattern must carry the container name: %v", pgrep)
	}
	if !slices.ContainsFunc(pgrep, func(a string) bool { return strings.Contains(a, "container-runtime-linux") }) {
		t.Errorf("pgrep pattern must target container-runtime-linux: %v", pgrep)
	}
	if !hasCall(calls, func(c []string) bool {
		return c[0] == "kill" && slices.Equal(c[1:], []string{"-9", "54321"})
	}) {
		t.Errorf("expected `kill -9 54321`, calls: %v", calls)
	}
	// Teardown must precede the eventual start.
	killIdx := firstIndex(calls, func(c []string) bool { return c[0] == "kill" })
	startIdx := firstIndex(calls, isVerb("container", "start"))
	if startIdx < 0 || killIdx < 0 || killIdx > startIdx {
		t.Errorf("kill -9 must precede start (kill=%d start=%d)", killIdx, startIdx)
	}
}

// TestRestartBrowserSidecarMissingContainer is scenario (c): the sidecar was
// removed entirely, so the state-check reports no such container and the ladder
// recreates it via the exact `container run` argv from browserSidecarRunArgs.
func TestRestartBrowserSidecarMissingContainer(t *testing.T) {
	fake := &scriptedExec{inspectSt: []string{"missing"}}
	swapBrowserExec(t, fake.fn)
	swapVerifyBrowser(t, func(_ context.Context, _ *BrowserSidecar) error { return nil })

	if _, err := restartBrowserSidecar(context.Background(), "demo", "1.61.0"); err != nil {
		t.Fatalf("restartBrowserSidecar: %v", err)
	}

	calls := fake.snapshot()
	wantRun := append([]string{"container"}, browserSidecarRunArgs("cspace-demo-browser", "1.61.0", "192.168.65.1")...)
	if !hasCall(calls, func(c []string) bool { return slices.Equal(c, wantRun) }) {
		t.Errorf("expected recreate via run argv %v; calls: %v", wantRun, calls)
	}
	// A missing container is never started (run -d already starts it).
	if hasCall(calls, isVerb("container", "start")) {
		t.Error("missing-container path must not `container start`")
	}
}

// TestRestartBrowserSidecarSplitBrainUnresolved covers the degrade path: pgrep
// finds nothing and the container stays running, so the poll budget elapses and
// the ladder returns a clear error instead of panicking or falsely succeeding.
func TestRestartBrowserSidecarSplitBrainUnresolved(t *testing.T) {
	fake := &scriptedExec{
		inspectSt: []string{"running", "running", "running"}, // never clears
		killOut:   "container is not running",
		killErr:   errors.New("exit status 1"),
		pgrepErr:  errors.New("exit status 1"), // pgrep found nothing
	}
	swapBrowserExec(t, fake.fn)
	swapVerifyBrowser(t, func(_ context.Context, _ *BrowserSidecar) error {
		t.Error("verify must not run when teardown fails")
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	bs, err := restartBrowserSidecar(ctx, "demo", "1.59.0")
	if err == nil {
		t.Fatal("expected an error when the container never stops")
	}
	if bs != nil {
		t.Errorf("expected nil sidecar on failure, got %+v", bs)
	}
}

// TestContainerStateRunning verifies the three outcomes the ladder branches on.
func TestContainerStateRunning(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		wantRunning bool
		wantExists  bool
	}{
		{"running", fakeInspectJSON("running"), true, true},
		{"stopped", fakeInspectJSON("stopped"), false, true},
		{"missing", "[]", false, false},
		{"empty", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			swapBrowserExec(t, func(_ context.Context, _ string, _ ...string) (string, error) {
				return tc.out, nil
			})
			running, exists := containerStateRunning(context.Background(), "cspace-demo-browser")
			if running != tc.wantRunning || exists != tc.wantExists {
				t.Errorf("got running=%v exists=%v, want running=%v exists=%v",
					running, exists, tc.wantRunning, tc.wantExists)
			}
		})
	}
}

// TestWaitForBrowserIP verifies the poller extracts the sidecar's IPv4 from
// the 1.1.x inspect shape (status.networks[].ipv4Address, CIDR stripped).
func TestWaitForBrowserIP(t *testing.T) {
	swapBrowserExec(t, func(_ context.Context, _ string, _ ...string) (string, error) {
		return fakeInspectJSON("running"), nil
	})
	ip, err := waitForBrowserIP(context.Background(), "cspace-demo-browser", 2*time.Second)
	if err != nil {
		t.Fatalf("waitForBrowserIP: %v", err)
	}
	if ip != "192.0.2.7" {
		t.Errorf("ip = %q, want 192.0.2.7", ip)
	}
}

// TestVerifyBrowserFnDefault exercises the default verification composition:
// IP acquisition first (failure surfaces as an IP error), and on a good IP the
// CDP/WS URLs are constructed from it before the CDP probe runs.
func TestVerifyBrowserFnDefault(t *testing.T) {
	t.Run("ip failure", func(t *testing.T) {
		swapBrowserExec(t, func(_ context.Context, _ string, _ ...string) (string, error) {
			return "[]", nil // no IP ever appears
		})
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		bs := &BrowserSidecar{ContainerName: "cspace-demo-browser"}
		err := verifyBrowserFn(ctx, bs)
		if err == nil || !strings.Contains(err.Error(), "IP") {
			t.Fatalf("want IP error, got %v", err)
		}
		if bs.IP != "" {
			t.Errorf("IP should stay empty on failure, got %q", bs.IP)
		}
	})

	t.Run("constructs urls then probes cdp", func(t *testing.T) {
		swapBrowserExec(t, func(_ context.Context, _ string, _ ...string) (string, error) {
			// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): guaranteed unroutable so
			// the CDP probe fails fast without touching any real service.
			return `[{"status":{"networks":[{"ipv4Address":"192.0.2.1/24"}]}}]`, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		bs := &BrowserSidecar{ContainerName: "cspace-demo-browser"}
		err := verifyBrowserFn(ctx, bs)
		if err == nil || !strings.Contains(err.Error(), "CDP") {
			t.Fatalf("want CDP error, got %v", err)
		}
		if bs.IP != "192.0.2.1" {
			t.Errorf("IP = %q, want 192.0.2.1", bs.IP)
		}
		if bs.CDPURL != "http://192.0.2.1:9222" {
			t.Errorf("CDPURL = %q", bs.CDPURL)
		}
		if bs.RunServerWSURL != "ws://192.0.2.1:3000/" {
			t.Errorf("RunServerWSURL = %q", bs.RunServerWSURL)
		}
	})
}

// --- Fix round 1 -----------------------------------------------------------

// TestBrowserExecCmdDefaultStdoutOnly exercises the REAL default browserExecCmd
// (no seam swap) via a tiny `sh` helper process — not the `container` CLI, per
// the fix brief. Guards against the seam's default returning CombinedOutput,
// which corrupts the three stdout-JSON parsers (waitForBrowserIP,
// sidecarVersion, containerStateRunning) whenever a healthy `container
// inspect` writes anything to stderr; in the worst case, resolveBrowserSplitBrain's
// poll misparses "running" as "not running" and teardown falsely reports success.
func TestBrowserExecCmdDefaultStdoutOnly(t *testing.T) {
	t.Run("stdout only, stderr excluded", func(t *testing.T) {
		out, err := browserExecCmd(context.Background(), "sh", "-c", "echo OUT; echo NOISE 1>&2")
		if err != nil {
			t.Fatalf("browserExecCmd: unexpected error: %v", err)
		}
		if out != "OUT\n" {
			t.Errorf("out = %q, want exactly %q (stderr NOISE must not leak into stdout)", out, "OUT\n")
		}
	})

	t.Run("stderr text surfaces in the error on failure", func(t *testing.T) {
		_, err := browserExecCmd(context.Background(), "sh", "-c", "echo ERRTEXT 1>&2; exit 1")
		if err == nil {
			t.Fatal("browserExecCmd: want error, got nil")
		}
		if !strings.Contains(err.Error(), "ERRTEXT") {
			t.Errorf("error = %q, want it to contain stderr text %q", err.Error(), "ERRTEXT")
		}
	})
}

// TestResolveBrowserSplitBrainEscapesPgrepPattern guards against browser.go:113
// embedding the container name into the pgrep -f pattern unescaped: a "." in a
// project name (e.g. "my.project") is a regex wildcard, so an unescaped
// pattern can match — and `kill -9` — a sibling project's
// container-runtime-linux process. The recorded pgrep pattern must carry the
// regexp.QuoteMeta-escaped form of the container name.
func TestResolveBrowserSplitBrainEscapesPgrepPattern(t *testing.T) {
	fake := &scriptedExec{
		inspectSt: []string{"running", "running", "running", "stopped"},
		stopErr:   errors.New("Error: stop: dead exec session"),
		killOut:   "Error: cannot kill container cspace-my.project-browser: container is not running",
		killErr:   errors.New("exit status 1"),
		pgrepOut:  "54321\n",
	}
	swapBrowserExec(t, fake.fn)
	swapVerifyBrowser(t, func(_ context.Context, _ *BrowserSidecar) error { return nil })

	if _, err := restartBrowserSidecar(context.Background(), "my.project", "1.59.0"); err != nil {
		t.Fatalf("restartBrowserSidecar: %v", err)
	}

	calls := fake.snapshot()
	pgrepIdx := firstIndex(calls, func(c []string) bool { return c[0] == "pgrep" })
	if pgrepIdx < 0 {
		t.Fatal("expected a pgrep call for split-brain teardown")
	}
	pgrep := calls[pgrepIdx]
	wantEscaped := regexp.QuoteMeta("cspace-my.project-browser")
	if wantEscaped != `cspace-my\.project-browser` {
		t.Fatalf("sanity check failed: QuoteMeta(%q) = %q", "cspace-my.project-browser", wantEscaped)
	}
	if !slices.ContainsFunc(pgrep, func(a string) bool { return strings.Contains(a, wantEscaped) }) {
		t.Errorf("pgrep pattern must carry the escaped container name %q: %v", wantEscaped, pgrep)
	}
	// The raw (unescaped) dotted name must NOT appear literally unless it
	// equals the escaped form — i.e. we must not regress to embedding "." raw.
	if slices.ContainsFunc(pgrep, func(a string) bool {
		return strings.Contains(a, "cspace-my.project-browser") && !strings.Contains(a, wantEscaped)
	}) {
		t.Errorf("pgrep pattern embeds the unescaped container name: %v", pgrep)
	}
}

// TestRemainingBudgetCapsAtFallback guards against remainingBudget returning
// the full remaining time on ctx's deadline regardless of fallback, which
// made the restart ladder's per-probe slices (30s/90s/30s) dead: with a
// generous outer budget (e.g. the ladder's 120s), the IP probe alone could
// consume the whole thing. remainingBudget must return min(remaining, fallback).
func TestRemainingBudgetCapsAtFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got := remainingBudget(ctx, 100*time.Millisecond)
	if got > 500*time.Millisecond {
		t.Errorf("remainingBudget = %v, want capped near the 100ms fallback, not the full ~10s remaining", got)
	}
}

// TestRemainingBudgetUsesRemainingWhenSmaller is the companion case: when
// ctx's remaining time is smaller than fallback, that smaller remaining value
// (not fallback) must win, so callers still respect an imminent deadline.
func TestRemainingBudgetUsesRemainingWhenSmaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)

	got := remainingBudget(ctx, 30*time.Second)
	if got <= 0 || got > 50*time.Millisecond {
		t.Errorf("remainingBudget = %v, want (0, 50ms]", got)
	}
}
