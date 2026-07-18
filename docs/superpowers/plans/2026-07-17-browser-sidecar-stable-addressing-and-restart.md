# Browser Sidecar Stable Addressing + Restart — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the shared browser sidecar a restart-stable DNS name (`browser.<project>.cspace.test`), protocol-level health probes, and an agent-invocable restart (daemon endpoint + `cspace browser restart|status`).

**Architecture:** Four pieces layered onto existing code: a `browser` special case in the daemon DNS handler (reusing `lookupSidecarIP`); a WebSocket-handshake probe joining the existing CDP probe; a restart escalation ladder in browser.go behind an exec seam; a daemon HTTP endpoint plus a `cspace browser` command that works host-side (direct) and in-sandbox (HTTP via gateway). Spec: `docs/superpowers/specs/2026-07-17-browser-sidecar-stable-addressing-and-restart-design.md` — read it first.

**Tech Stack:** Go (Cobra CLI), miekg/dns handler, net/http, Apple Container CLI via exec.

## Global Constraints

- TDD per task: write the failing test, watch it fail, implement, watch it pass, commit.
- NEVER run `TestCspaceLifecycle` — it mutates the host daemon and can trigger an image rebuild. Every test invocation for `./internal/cli` must use `-skip 'TestCspaceLifecycle'`.
- NEVER invoke `container` against live `cspace-resume-redux-*` containers. All new logic is unit-tested through seams/fixtures; no test may create, stop, exec into, or inspect a real container.
- Exec seams are package-level function vars (existing pattern: `validateGitHubToken` in internal/secrets/github.go). Tests swap them via `t.Cleanup` restore.
- DNS domain suffix constant is `daemonDNSDomain` (= "cspace.test."-adjacent handling in cmd_daemon.go — reuse it, never hardcode the suffix in new code).
- The reserved browser label is the literal `browser`; container name comes from the existing `browserSingletonName(project)` = `cspace-<project>-browser`.
- Ports: `browserCDPPort` (9222), `browserRunServerPort` (3000) — existing constants in browser.go; never literal numbers in new code.
- Restart verification budget: 120s total. Split-brain poll: 10s.
- Commit messages: short imperative; the final docs task's commit appends `(cs-finding:2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path)` and `(cs-finding:2026-07-17-tcp-connect-probes-pass-wedged-services)`.

---

### Task 1: WebSocket-handshake probe

**Files:**
- Modify: `internal/cli/browser.go` (add `waitForRunServerWS` next to `waitForCDP` at ~line 313)
- Test: `internal/cli/browser_test.go`

**Interfaces:**
- Produces: `func waitForRunServerWS(ctx context.Context, addr string, max time.Duration) error` — `addr` is `"host:port"`. Success ONLY when the first response line is `HTTP/1.1 101`. Used by Tasks 4 and 6, and wired into `ensureSharedBrowserSidecar`'s reuse path in this task.

- [ ] **Step 1: Write the failing test** — three local `net.Listener` fixtures in `browser_test.go`:

```go
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
```

- [ ] **Step 2:** `go test ./internal/cli -run TestWaitForRunServerWS -skip 'TestCspaceLifecycle'` → FAIL (undefined `waitForRunServerWS`).
- [ ] **Step 3: Implement** in browser.go (doc comment: cites that a wedged guest ACKs TCP, so only a completed upgrade counts — cs-finding 2026-07-17-tcp-connect-probes-pass-wedged-services):

```go
func waitForRunServerWS(ctx context.Context, addr string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if err := wsHandshakeOnce(addr, 3*time.Second); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for run-server WS handshake at %s", addr)
}

func wsHandshakeOnce(addr string, timeout time.Duration) error {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(timeout))
	key := base64.StdEncoding.EncodeToString([]byte("cspace-probe-16by"))
	req := "GET / HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		return err
	}
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 101") {
		return fmt.Errorf("unexpected handshake response: %q", string(buf[:n]))
	}
	return nil
}
```

Also wire it into `ensureSharedBrowserSidecar`'s two reuse branches: after each `waitForCDP(...) == nil` check, additionally require `waitForRunServerWS(ctx, fmt.Sprintf("%s:%d", ip, browserRunServerPort), 10*time.Second) == nil` before reusing.
- [ ] **Step 4:** test passes; `go test ./internal/cli -skip 'TestCspaceLifecycle'` all green.
- [ ] **Step 5:** Commit: `Add run-server WS-handshake probe; require it on sidecar reuse`

---

### Task 2: DNS `browser.<project>.cspace.test`

**Files:**
- Modify: `internal/cli/cmd_daemon.go` (`daemonDNSHandler`, 2-label case at ~line 340)
- Test: `internal/cli/cmd_daemon_test.go`

**Interfaces:**
- Consumes: `lookupSidecarIP(name string)` (existing, memoized). Make it seam-able: `var lookupSidecarIPFn = lookupSidecarIP` and change BOTH call sites in the handler (3-label service case and the new browser case) to use the var. First inspect `cmd_daemon_test.go`/`probes_test.go` for an existing fake pattern and follow it.
- Produces: 2-label queries whose leftmost label is `browser` resolve `browserSingletonName(project)` via `lookupSidecarIPFn`, skipping the registry match entirely. NXDOMAIN when lookup fails/empty. 1-label `browser` stays NXDOMAIN (falls through existing registry path and matches nothing — add a test asserting this, and a guard comment).

- [ ] **Step 1: Failing test** — drive `daemonDNSHandler` directly (build a `dns.Msg` A question for `browser.demo.cspace.test.`, a `registry.Registry` backed by a temp file with one unrelated entry, and a fake `lookupSidecarIPFn` returning `192.168.64.150` for `cspace-demo-browser`; use a recording `dns.ResponseWriter` stub — if one doesn't exist in the test files yet, write a minimal one implementing `WriteMsg` capture). Assert: one A answer = 192.168.64.150. Second case: fake returns error → RcodeNameError. Third: `browser.cspace.test.` (1-label) → RcodeNameError.
- [ ] **Step 2:** run → FAIL (browser currently NXDOMAINs because no registry entry matches).
- [ ] **Step 3: Implement.** In the `case 2:` branch, before the registry list/match block:

```go
if parts[0] == "browser" {
	// Reserved label: the project's shared browser sidecar. No registry
	// entry exists for it — resolve the container directly so the name
	// keeps working across sidecar restarts (new IP within one memo TTL).
	sidecarIP, err := lookupSidecarIPFn(browserSingletonName(parts[1]))
	if err != nil || sidecarIP == "" {
		reply.Rcode = dns.RcodeNameError
		continue
	}
	// fall through to the answer-append with ip = sidecarIP
}
```

Restructure minimally so the answer-append at the bottom is shared (e.g. set `ip` and `goto`-free skip of the registry block via a small `resolved bool`). Keep the existing paths byte-identical in behavior.
- [ ] **Step 4:** tests pass; whole-package suite green (with the mandatory `-skip`).
- [ ] **Step 5:** Commit: `Resolve browser.<project>.cspace.test to the shared sidecar in daemon DNS`

---

### Task 3: Reserve `browser` as a sandbox name

**Files:**
- Modify: `internal/cli/cmd_up.go` (explicit-name path, near `pickPlanetName` usage ~lines 59-67)
- Test: `internal/cli/cmd_up_test.go`

**Interfaces:**
- Produces: `func validateSandboxName(name string) error` — returns an error for `browser` (message: `"browser" is reserved for the shared browser sidecar (browser.<project>.cspace.test)`); nil otherwise. Called in the up RunE before any provisioning when an explicit name is given.

- [ ] Steps: failing test (`validateSandboxName("browser")` errors, `validateSandboxName("issue-42")` nil, and the error text names the DNS form) → implement → wire into RunE → suite green → Commit: `Reserve "browser" as a sandbox name`

---

### Task 4: Restart escalation ladder

**Files:**
- Modify: `internal/cli/browser.go`
- Test: `internal/cli/browser_test.go`

**Interfaces:**
- Produces: `var browserExecCmd = func(ctx context.Context, name string, args ...string) (string, error)` (default: `exec.CommandContext(...).CombinedOutput()` wrapped) — ALL substrate/pgrep/kill invocations inside the ladder AND inside `containerStateRunning` go through it.
- Produces: `func restartBrowserSidecar(ctx context.Context, project, plVersion string) (*BrowserSidecar, error)` implementing spec §3: read version label (best-effort via existing `sidecarVersion` — route its exec through the seam too) → `container stop -t 5` → if still running `container kill --signal SIGKILL` → if STILL running (split-brain) `pgrep -f "container-runtime-linux.*<name>"` + `kill -9 <pid>` + poll not-running (10s) → `container start`; if container absent entirely → `runBrowserSidecar(ctx, name, plVersion)` → verify `waitForBrowserIP` + `waitForCDP` + `waitForRunServerWS` within a 120s overall budget → return `*BrowserSidecar`.
- Consumed by Tasks 5 and 6.

- [ ] **Step 1: Failing tests** — table-driven with a scripted fake `browserExecCmd` that records the invocation sequence and returns canned outputs per call pattern. Cases: (a) clean stop→start (assert order: stop, state-check, start, and NO pgrep/kill); (b) split-brain (stop errors, kill errors "not running", state says running twice then not-running after pgrep/kill-9 — assert pgrep+kill invoked with the container name in the pattern); (c) container missing (state-check says no such container → assert the `run` argv from `browserSidecarRunArgs` was invoked). Verification probes: point `waitForCDP`/`waitForRunServerWS` at Task-1-style local fixtures by letting the fake exec return an inspect JSON whose IP is the fixture listener's host — OR (simpler, allowed) seam the verify step as `var verifyBrowserFn = func(ctx, bs *BrowserSidecar) error` and assert it was called; default implementation composes the three real probes and gets its own small test.
- [ ] **Step 2:** run → FAIL (undefined symbols).
- [ ] **Step 3:** implement exactly per the interface block above. Split-brain detection = state-per-inspect says `running` after kill returned an error containing `not running` (match loosely, lowercase contains). Poll interval 500ms.
- [ ] **Step 4:** package suite green (with `-skip`).
- [ ] **Step 5:** Commit: `Add browser sidecar restart ladder with split-brain escalation`

---

### Task 5: Daemon endpoint `POST /browser/restart/{project}`

**Files:**
- Modify: `internal/cli/cmd_daemon.go` (mux in `runDaemonServe`; extract the handler as `browserRestartHandler(r *registry.Registry) http.HandlerFunc` for testability)
- Test: `internal/cli/cmd_daemon_test.go`

**Interfaces:**
- Consumes: `var restartBrowserFn = restartBrowserSidecar` (new seam var, defined in browser.go next to the func).
- Produces: route `mux.HandleFunc("POST /browser/restart/{project}", browserRestartHandler(r))`. Behavior: project from `req.PathValue("project")` (400 if empty). Auth: allow if `req.RemoteAddr` host is `127.0.0.1`/`::1`; otherwise require `Authorization: Bearer <tok>` where some `r.List()` entry has `Token == tok && Project == project` (401 otherwise). Per-project serialization: package-level `sync.Map` of `*sync.Mutex`. On success: 200 JSON `{"ok":true,"ip":...,"cdpUrl":...,"runServerWsUrl":...}` (from the returned `BrowserSidecar`); on ladder error: 502 JSON `{"ok":false,"error":...}`.

- [ ] **Step 1: Failing tests** via `httptest.NewServer` (which reports RemoteAddr 127.0.0.1 — exercise the loopback-allow path) and direct handler invocation with a crafted `req.RemoteAddr = "192.168.64.85:5555"` for the auth paths: (a) loopback, fake ladder success → 200 + body fields; (b) non-loopback, no token → 401 and ladder NOT called; (c) non-loopback, token matching a registry entry of the project → 200; (d) token from a DIFFERENT project's entry → 401; (e) ladder error → 502. Fake `restartBrowserFn` records calls.
- [ ] **Steps 2-4:** RED → implement → green (with `-skip`).
- [ ] **Step 5:** Commit: `Add daemon endpoint POST /browser/restart/{project}`

---

### Task 6: `cspace browser restart|status` CLI

**Files:**
- Create: `internal/cli/cmd_browser.go`
- Modify: `internal/cli/root.go` (register `newBrowserCmd()`)
- Test: `internal/cli/cmd_browser_test.go`

**Interfaces:**
- Consumes: `restartBrowserFn` (host path), `sandboxmode.IsInSandbox()/Project()/RegistryURL()` (in-sandbox path), `waitForCDP`/`waitForRunServerWS` (status), `browserSingletonName`, and for in-sandbox auth the self-lookup `GET <registry>/lookup/<project>/<sandbox>` → `Token` (mirror `cmd_send.go:resolveEntry`).
- Produces: `newBrowserCmd()` cobra group. `restart`: host → `restartBrowserFn(ctx, project, "")` and print refreshed endpoints; sandbox → POST `<CSPACE_REGISTRY_URL>/browser/restart/<project>` with the self-looked-up Bearer token, print response. `status`: host → resolve IP via the seam-able exec (inspect) then probe both endpoints; sandbox → probe `browser.<project>.cspace.test` directly; print one line per endpoint: `CDP :9222 ok|FAIL (<detail>)` / `run-server :3000 ok|FAIL (<detail>)`, non-zero exit if either fails. Project resolution: sandboxmode in-sandbox; `cfg.Project.Name` host-side (nil-cfg → clear error).

- [ ] Steps: failing tests for (a) in-sandbox restart posts to the right URL with the right Bearer token (httptest fake daemon serving both `/lookup/` and `/browser/restart/`, sandboxmode env set via `t.Setenv`); (b) host restart calls `restartBrowserFn` with the config project; (c) status exit behavior against Task-1 WS fixtures + an httptest CDP fake (200 on `/json/version`) — probe helpers accept addresses so tests inject fixture addrs; RED → implement → green → register in root.go → Commit: `Add cspace browser restart/status (host + in-sandbox)`

---

### Task 7: Names in sandbox env, docs, findings

**Files:**
- Modify: `internal/cli/browser.go` (add helper), `internal/cli/cmd_up.go` (~650-659), `CLAUDE.md`, `docs/env-cspace.md`
- Modify: `.cspace/context/findings/2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path.md`, `.cspace/context/findings/2026-07-17-tcp-connect-probes-pass-wedged-services.md`
- Test: `internal/cli/browser_test.go`

**Interfaces:**
- Produces: `func browserEnvURLs(project string) (cdpURL, wsURL string)` returning `http://browser.<project>.cspace.test:9222` and `ws://browser.<project>.cspace.test:3000/` (build the host with the same suffix constant the DNS handler strips; ports from the existing constants).

- [ ] Failing test: exact strings for project `demo`. Implement helper; in cmd_up.go replace the three env assignments (`CSPACE_BROWSER_CDP_URL`, `PLAYWRIGHT_MCP_CDP_ENDPOINT` ← cdpURL; `PW_TEST_CONNECT_WS_ENDPOINT` ← wsURL) — the adjacent status print at ~line 666 may keep showing `bs.CDPURL` (IP) for human debugging, plus the name. Docs: CLAUDE.md Commands list gains `cspace browser restart|status`; Browser sidecar section gains the DNS name + restart endpoint sentence; env-cspace.md `$CSPACE_WORKSPACE_HOST` section gains a short "Reaching the browser" note (the env vars now carry the stable name). Findings: append resolved Updates entries + flip `status:` to `resolved` in both. Full check: `make vet && make lint && go test ./... -skip 'TestCspaceLifecycle'` — note the applecontainer integration tests self-skip only if the CLI is absent; on this machine run `go test ./internal/substrate/applecontainer -run TestParseSystemStatus` instead of the whole package.
- [ ] Commit: `Point sandbox browser env at browser.<project>.cspace.test; docs (cs-finding:2026-07-17-sidecar-addressed-by-boot-baked-ip-no-recovery-path) (cs-finding:2026-07-17-tcp-connect-probes-pass-wedged-services)`

---

## Self-review notes

- Type consistency: `waitForRunServerWS(ctx, addr string, max)` used in Tasks 1/4/6; `restartBrowserSidecar(ctx, project, plVersion) (*BrowserSidecar, error)` in 4/5/6; `browserEnvURLs` only in 7. `lookupSidecarIPFn` var introduced in Task 2 and untouched after.
- Spec coverage: §1 → Tasks 2, 3, 7; §2 → Task 1; §3 → Task 4; §4 → Tasks 5, 6; security/docs → Task 7.
- Every task independently testable without containers; the mandated `-skip` appears in the global constraints and Task 7's final check.
