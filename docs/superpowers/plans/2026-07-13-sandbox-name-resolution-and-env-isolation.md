# Sandbox Name-Resolution Reliability + Env Isolation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make cspace's `*.cspace.test` name resolution survive the life of the sandboxes (not die mid-session), give the shared browser correct per-sandbox routing, fail loud when the DNS chain is broken, and add a non-invasive project env-override file — so the agent's MCP browser, the Playwright `run-server` e2e browser, host→site access, and the convex `__SELF__` proxy all resolve reliably.

**Architecture:** `cspace up` spawns the `.cspace.test` DNS daemon. Today it's spawned un-detached with stderr on a parent-held pipe (`cmd_up.go:1152-1159`), so it takes `SIGPIPE` on its next log write after `cspace up` exits. The fix detaches it (`Setsid` + log file), adds a `/health` version handshake with a race-safe stop-and-respawn, verifies in-container resolution at boot, extends `cspace doctor`, refreshes stale registry IPs on lookup, deletes the dead `ServiceIPs` field, sets `CSPACE_WORKSPACE_HOST` unconditionally, and documents an `.env.cspace` convention.

**Tech Stack:** Go (Cobra CLI, `github.com/miekg/dns`), Apple Container substrate (vmnet `192.168.64.0/24`, gateway `.1`), `~/.cspace/sandbox-registry.json`, dnsmasq inside containers forwarding `*.cspace.test` → `192.168.64.1:5354`.

**Spec:** `docs/superpowers/specs/2026-07-12-sandbox-name-resolution-and-env-isolation-design.md`

## Global Constraints

- **One PR**, implemented in the task order below (highest-leverage first).
- Build: `make build` (runs `make sync-embedded`). Test: `make test` (`go test ./...`). Static analysis: `make vet` and the repo's `golangci-lint` (default linters include `unused` — write-only fields/dead symbols fail the lint). All must pass before each commit.
- Go only; **no new external dependencies**.
- `SysProcAttr{Setsid: true}` compiles on both build targets (darwin + linux — the only goos in `.goreleaser.yml`), so the daemon-spawn code needs **no** `runtime.GOOS` guard. (Correcting an earlier assumption.)
- DNS facts are fixed: domain `cspace.test.`, host loopback `127.0.0.1:5354`, container-facing gateway `192.168.64.1:5354`, HTTP registry `127.0.0.1:6280` (`0.0.0.0` bind), TTL `5`. The daemon idle-exit gate (exit only when idle **and** `len(entries)==0`, `cmd_daemon.go:163-165`) is **correct — do not touch it.** The bug is detachment, not idle policy.
- `Version` is a mutable package var (`internal/cli/root.go:15`), ldflags-stamped. Tests that mutate it MUST restore it with `t.Cleanup`.
- Devcontainer base is `node:24-bookworm-slim` (Debian) and the sidecar is `mcr.microsoft.com/playwright:*-noble` (Ubuntu) — both glibc, both ship `getent`. (CLAUDE.md's "Alpine" is stale.)
- Commit bodies end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## File Structure

- `internal/cli/daemon_spawn.go` *(new)* — detached `*exec.Cmd` builder + `~/.cspace/daemon.log` path (unit-testable, keeps `cmd_up.go` focused).
- `internal/cli/cmd_up.go` — `ensureRegistryDaemon` (detach + version handshake); hoist `CSPACE_WORKSPACE_HOST`; call the boot gate.
- `internal/cli/cmd_daemon.go` — env-overridable DNS listen addrs; `/health` version; extract `stopRegistryDaemon`; `daemonDNSHandler` IP refresh.
- `internal/cli/cmd_send.go`, `internal/cli/cmd_attach.go` — re-ensure the daemon.
- `internal/cli/resolve_gate.go` *(new)* — `verifyInContainerResolution(...)`, reused by the boot gate and the doctor probe.
- `internal/cli/probes.go` — a gateway-DNS `ProbeCheck` and an in-container-resolution `ProbeCheck`, both inside `ProbeDaemon`.
- `internal/orchestrator/types.go`, `internal/orchestrator/lifecycle.go` — delete the dead `ServiceIPs()` method, the `serviceIPs` field, and its write (`lifecycle.go:205`). **Keep `InjectHosts`** (it is live — `InjectWorkspaceHost` calls it, `browser.go:209`); only fix its stale docstring.
- `docs/env-cspace.md`, agent-guidance docs — `.env.cspace` convention + `$CSPACE_WORKSPACE_HOST`.

---

## Task 1: Detach the daemon (the core SIGPIPE fix)

**Files:** Create `internal/cli/daemon_spawn.go`, `internal/cli/daemon_spawn_test.go`; Modify `internal/cli/cmd_daemon.go` (env-overridable listen addrs), `internal/cli/cmd_up.go:1139-1198`.

**Interfaces:**
- Produces `daemonLogPath() (string, error)` → `~/.cspace/daemon.log`.
- Produces `newDaemonCommand(self string) (*exec.Cmd, *os.File, error)` — `self daemon serve`, `SysProcAttr{Setsid:true}`, stdout+stderr = an `*os.File` log (never a parent-held pipe). Caller closes the file after `Start`.
- Produces (in `cmd_daemon.go`) env overrides `CSPACE_DAEMON_DNS_ADDR` / `CSPACE_DAEMON_GATEWAY_ADDR` so tests bind ephemeral ports instead of the real `5354`.

- [ ] **Step 1: Make the daemon DNS listen addresses overridable (so tests don't collide with a real daemon)**

In `cmd_daemon.go`, change the `const daemonDNSListenAddr`/`daemonDNSGatewayAddr` usage in `runDaemonServe` to read overrides first:
```go
func daemonDNSAddrs() (listen, gateway string) {
	listen = daemonDNSListenAddr
	if v := os.Getenv("CSPACE_DAEMON_DNS_ADDR"); v != "" {
		listen = v
	}
	gateway = daemonDNSGatewayAddr
	if v := os.Getenv("CSPACE_DAEMON_GATEWAY_ADDR"); v != "" {
		gateway = v
	}
	return
}
```
Use `listen, gateway := daemonDNSAddrs()` where `runDaemonServe` currently references the consts. Keep the consts as the defaults. Commit this small enabler with Step 8.

- [ ] **Step 2: Write the failing unit test** (`daemon_spawn_test.go`)

```go
package cli

import (
	"os"
	"strings"
	"testing"
)

func TestNewDaemonCommandIsDetached(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd, f, err := newDaemonCommand("/usr/local/bin/cspace")
	if err != nil {
		t.Fatalf("newDaemonCommand: %v", err)
	}
	defer f.Close()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("daemon command must set Setsid so it survives the parent")
	}
	if _, ok := cmd.Stderr.(*os.File); !ok {
		t.Errorf("stderr must be an *os.File log (not a parent-held pipe), got %T", cmd.Stderr)
	}
	if cmd.Stdout != cmd.Stderr {
		t.Error("stdout and stderr should share the log file")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.HasSuffix(joined, "daemon serve") {
		t.Errorf("args = %v, want ... daemon serve", cmd.Args)
	}
}
```

- [ ] **Step 3: Run test to verify it fails** — `go test ./internal/cli/ -run TestNewDaemonCommandIsDetached -v` → FAIL `undefined: newDaemonCommand`.

- [ ] **Step 4: Implement** (`daemon_spawn.go`)

```go
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func daemonLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

// newDaemonCommand builds a detached `<self> daemon serve`. Setsid detaches it
// from the parent's session; stdout+stderr go to an append-only log FILE (not
// a parent-held os.Pipe) so a later log write can't take EPIPE -> SIGPIPE after
// cspace up exits. Caller closes the returned file after Start.
func newDaemonCommand(self string) (*exec.Cmd, *os.File, error) {
	logPath, err := daemonLogPath()
	if err != nil {
		return nil, nil, err
	}
	if err := rotateIfLarge(logPath, 1<<20); err != nil { // 1 MiB cap (spec 1a)
		return nil, nil, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(self, "daemon", "serve")
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, f, nil
}

// rotateIfLarge truncates the log by renaming to .1 once it exceeds max bytes,
// bounding the gateway-retry chatter over long-lived hosts.
func rotateIfLarge(path string, max int64) error {
	if fi, err := os.Stat(path); err == nil && fi.Size() > max {
		return os.Rename(path, path+".1")
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes** — `go test ./internal/cli/ -run TestNewDaemonCommandIsDetached -v` → PASS.

- [ ] **Step 6: Rewrite `ensureRegistryDaemon`** (`cmd_up.go:1139-1198`)

Reap the child with a goroutine `Wait` (prevents a zombie if the daemon fast-fails on a squatted port) and hold no pipe:
```go
func ensureRegistryDaemon() error {
	if v, ok := daemonHealthVersion(time.Second); ok && v == Version {
		return nil // healthy and current (version check added in Task 2)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cspace binary: %w", err)
	}
	cmd, logFile, err := newDaemonCommand(self)
	if err != nil {
		return fmt.Errorf("prepare daemon: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("spawn cspace daemon: %w", err)
	}
	_ = logFile.Close() // detached daemon keeps its own handle on the log file
	go func() { _ = cmd.Wait() }() // reap; does NOT couple lifetimes (child is Setsid-detached)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:6280", 250*time.Millisecond); derr == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p, perr := daemonLogPath(); perr == nil {
		if b, rerr := os.ReadFile(p); rerr == nil && len(b) > 0 {
			tail := string(b)
			if len(tail) > 800 {
				tail = tail[len(tail)-800:]
			}
			return fmt.Errorf("daemon not accepting connections within 3s; ~/.cspace/daemon.log tail:\n%s", tail)
		}
	}
	return fmt.Errorf("daemon started but not accepting connections within 3s")
}
```
Then delete the now-unused `limitedBuffer` type (`cmd_up.go:1200-1229`) and drop any now-unused imports (`bytes`, `sync`) — `make vet` will flag them. (`daemonHealthVersion` is added in Task 2; if implementing Task 1 in isolation, temporarily inline a `net.DialTimeout` reuse check and replace it in Task 2.)

- [ ] **Step 7: Write the survival test that drives the REAL path and can catch the regression** (`daemon_spawn_test.go`, append)

```go
import (
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// A detached daemon must keep answering after its spawner exits. We build the
// real binary, run a throwaway "spawner" that calls the same spawn path and
// then exits, and assert the daemon is still up. DNS/HTTP ports are overridden
// so this never collides with a developer's live daemon.
func TestDaemonSurvivesSpawnerExit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + spawns real processes")
	}
	bin := buildCspaceForTest(t) // go build -o <tmp> ./cmd/cspace
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"CSPACE_REGISTRY_DAEMON_PORT=6299",
		"CSPACE_DAEMON_DNS_ADDR=127.0.0.1:15354",
		"CSPACE_DAEMON_GATEWAY_ADDR=127.0.0.1:15355", // loopback stand-in; gateway bind is best-effort
		"CSPACE_REGISTRY_DAEMON_IDLE=1h",
	)
	// Spawner: start the daemon detached exactly like ensureRegistryDaemon, then exit.
	spawner := exec.Command(bin, "daemon", "serve")
	spawner.Env = env
	logf, _ := os.Create(filepath.Join(home, "d.log"))
	spawner.Stdout, spawner.Stderr = logf, logf
	spawner.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := spawner.Start(); err != nil {
		t.Fatal(err)
	}
	pid := spawner.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	waitForPort(t, "127.0.0.1:6299", 5*time.Second)
	_ = logf.Close() // drop the only non-daemon reference to the log
	// Force the daemon to keep logging (a real daemon logs on gateway retries);
	// then confirm it is still alive and answering after the parent's handle is gone.
	time.Sleep(1 * time.Second)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("daemon died after its spawner's log handle closed (SIGPIPE regression)")
	}
	if _, ok := httpGet(t, "http://127.0.0.1:6299/health"); !ok {
		t.Fatal("daemon stopped answering /health")
	}
}
```
Add helpers `buildCspaceForTest`, `waitForPort`, `httpGet` in the test file. (This drives the real binary and overridden ports; it fails on the pre-fix code because that path wires a parent-held pipe.)

- [ ] **Step 8: Full test + build + commit**

```bash
go test ./internal/cli/ -run 'TestNewDaemonCommand|TestDaemonSurvives' -v && make build && make vet
git add internal/cli/daemon_spawn.go internal/cli/daemon_spawn_test.go internal/cli/cmd_up.go internal/cli/cmd_daemon.go
git commit -m "fix(daemon): detach the registry daemon so it survives cspace up exiting

Setsid + ~/.cspace/daemon.log (not a parent-held pipe); reap via goroutine
Wait. Previously the daemon took SIGPIPE on its next log write after cspace up
exited, silently killing in-container .cspace.test resolution mid-session. DNS
listen addrs are now env-overridable so the survival test can't collide with a
real daemon.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Version handshake + race-safe stop; re-ensure from long-running commands

**Files:** Modify `internal/cli/cmd_daemon.go` (`/health`, extract `stopRegistryDaemon`), `internal/cli/cmd_up.go`, `internal/cli/cmd_send.go`, `internal/cli/cmd_attach.go`; Test `internal/cli/cmd_daemon_test.go`.

**Interfaces:**
- `/health` → `{"ok":true,"version":"<Version>"}`.
- `daemonHealthVersion(timeout) (string, bool)`.
- `stopRegistryDaemon() error` — `pkill -f "daemon serve"` **then blocks until `:6280` and `daemonDNSAddrs()` listen addr are free** (or a 3s timeout), so a respawn can't race the dying daemon for the fatal loopback bind.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run → FAIL** `undefined: healthHandler`.

- [ ] **Step 3: Named, versioned health handler** — replace the inline `GET /health` closure (`cmd_daemon.go:120-123`) with:
```go
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": Version})
}
```
Register: `mux.HandleFunc("GET /health", healthHandler)`.

- [ ] **Step 4: Run → PASS.**

- [ ] **Step 5: Extract a race-safe `stopRegistryDaemon`** from the pkill body currently inline in `newDaemonStopCmd` (`cmd_daemon.go:400-421`):
```go
func stopRegistryDaemon() error {
	out, err := exec.Command("pkill", "-f", "daemon serve").CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil // pkill exit 1 == no matches
		}
		return fmt.Errorf("pkill: %w (%s)", err, out)
	}
	// Block until the daemon's fatal-to-rebind ports are actually free, so a
	// respawn doesn't lose the loopback DNS/HTTP bind to the dying process.
	listen, _ := daemonDNSAddrs()
	waitPortFree("127.0.0.1:"+daemonHTTPPort, 3*time.Second)
	waitPortFree(listen, 3*time.Second)
	return nil
}

func waitPortFree(addr string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil { // refused == free
			return
		}
		_ = c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}
```
Have `newDaemonStopCmd`'s RunE call `stopRegistryDaemon()` (DRY). Add `daemonHealthVersion` (GET `/health`, decode `version`). In `ensureRegistryDaemon` (Task 1), the reuse check is already `if v, ok := daemonHealthVersion(...); ok && v == Version { return nil }`; add the stale branch just after it:
```go
	if v, ok := daemonHealthVersion(time.Second); ok && v != Version {
		_ = stopRegistryDaemon() // waits for ports to free, then fall through to respawn
	}
```

- [ ] **Step 6: Test the replace-on-mismatch behavior** (spec §5)

```go
func TestStopThenEnsureReplacesStaleDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	// Start a fake "old" daemon binding :6280 that reports an old version,
	// then assert ensureRegistryDaemon stops it and a current one comes up.
	// (Bind :6280 + a /health returning {"version":"old"} via a tiny httptest
	// server on that port; run ensureRegistryDaemon; poll /health for Version.)
}
```
Implement with an in-process `http.Server` on `127.0.0.1:6280` returning an old version, plus overridden DNS ports, then call `ensureRegistryDaemon()` and assert `/health` version becomes the test `Version`.

- [ ] **Step 7: Re-ensure the daemon from long-running host commands**

Agents inside containers cannot restart the daemon; host entry points must. In the RunE of **`cmd_send.go`** and **`cmd_attach.go`** (these exist; `coordinate` does **not** — do not reference it), add before the main work:
```go
	if err := ensureRegistryDaemon(); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: cspace daemon not reachable: %v\n", err)
	}
```

- [ ] **Step 8: Build + test + commit**

```bash
go test ./internal/cli/ -run 'TestHealth|TestStopThenEnsure' -v && make build && make vet
git add internal/cli/cmd_daemon.go internal/cli/cmd_up.go internal/cli/cmd_send.go internal/cli/cmd_attach.go internal/cli/cmd_daemon_test.go
git commit -m "fix(daemon): /health version handshake; race-safe stop; re-ensure from send/attach

Replace a stale-version daemon on reuse, waiting for its loopback ports to free
before respawning so the new daemon doesn't lose the fatal DNS bind. send/attach
re-ensure the daemon so long sessions recover if it died.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: In-container resolution gate at boot (with retries)

**Files:** Create `internal/cli/resolve_gate.go`, `internal/cli/resolve_gate_test.go`; Modify `internal/cli/cmd_up.go`.

**Interfaces:** `verifyInContainerResolution(ctx, exec containerExecFn, container, host string) error`, `containerExecFn func(ctx, container string, argv ...string) ([]byte, error)`. The exec closure wraps the substrate `Adapter.Exec` already used in `cmd_up` (see `adapter.go:249`, `substrateRunner` at `cmd_up.go:1680`).

- [ ] **Step 1: Write the failing test** — asserts behavior AND the argv:

```go
package cli

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestVerifyInContainerResolution(t *testing.T) {
	host := "mercury.p.cspace.test"
	t.Run("resolves and calls getent hosts <host>", func(t *testing.T) {
		var gotArgv []string
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) {
			gotArgv = argv
			return []byte("192.168.64.5 " + host + "\n"), nil
		}
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if want := []string{"getent", "hosts", host}; !reflect.DeepEqual(gotArgv, want) {
			t.Errorf("argv = %v, want %v", gotArgv, want)
		}
	})
	t.Run("empty output fails after retries", func(t *testing.T) {
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) { return []byte(""), nil }
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err == nil {
			t.Fatal("want error on empty resolution")
		}
	})
	t.Run("exec error fails", func(t *testing.T) {
		exec := func(_ context.Context, _ string, argv ...string) ([]byte, error) { return nil, errors.New("boom") }
		if err := verifyInContainerResolution(context.Background(), exec, "c", host); err == nil {
			t.Fatal("want error when exec fails")
		}
	})
}
```

- [ ] **Step 2: Run → FAIL** `undefined: verifyInContainerResolution`.

- [ ] **Step 3: Implement with retries** (cold-boot: in-container dnsmasq + the daemon's gateway bind are both racy for a few seconds):

```go
package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type containerExecFn func(ctx context.Context, container string, argv ...string) ([]byte, error)

func verifyInContainerResolution(ctx context.Context, exec containerExecFn, container, host string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		out, err := exec(ctx, container, "getent", "hosts", host)
		if err != nil {
			lastErr = fmt.Errorf("resolve %s in %s: %w", host, container, err)
			continue
		}
		if strings.TrimSpace(string(out)) != "" {
			return nil
		}
		lastErr = fmt.Errorf("%s did not resolve inside %s (cspace daemon DNS may be down)", host, container)
	}
	return lastErr
}
```

- [ ] **Step 4: Run → PASS.**

- [ ] **Step 5: Wire the gate into `cmd_up`** — after the sandbox is ready (and again for the browser sidecar container when `browserSidecar != nil`), warn-not-fail:
```go
	if wh := workspaceFriendlyHost(name, project); wh != "" {
		if err := verifyInContainerResolution(ctx, containerExecAdapter, containerName, wh); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: %v — sidecar browser and host will fall back to raw IPs; run `cspace doctor`\n", err)
		}
	}
```
`containerExecAdapter` is a 4-line closure over the in-scope substrate adapter: `func(ctx, name string, argv ...string) ([]byte, error) { r, err := adapter.Exec(ctx, name, strings.Join(argv, " "), ExecOpts{}); return []byte(r.Stdout), err }` (match the real `ExecOpts`/`ExecResult` fields).

- [ ] **Step 6: Build + commit** — `go test ./internal/cli/ -run TestVerify -v && make build && make vet`
```bash
git add internal/cli/resolve_gate.go internal/cli/resolve_gate_test.go internal/cli/cmd_up.go
git commit -m "feat(up): verify in-container .cspace.test resolution at boot (with retries); warn loudly

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Doctor probes for the container-facing path

**Files:** Modify `internal/cli/probes.go` (add two `ProbeCheck`s inside `ProbeDaemon`); Test `internal/cli/probes_test.go`.

**Context:** doctor is hardcoded subsystem functions returning `ProbeResult` with `[]ProbeCheck` (`cmd_doctor.go:42-49`, `probes.go:19-39`); statuses are `ProbePass`/`ProbeWarn`/`ProbeFail`. There is **no** `Probe`/`StatusFail` type. Add checks to `ProbeDaemon`, don't invent a new probe registry.

**Interfaces:** `probeGatewayDNS() ProbeCheck`, `probeInContainerDNS() ProbeCheck` (fulfils the spec-1b "gateway **and** in-container" promise). Refactor the existing loopback DNS check to share `probeDnsAt(addr, label string, failIsWarn bool) ProbeCheck` (DRY with `probeDnsDaemon`, `cmd_dns.go:299`).

- [ ] **Step 1: Write the failing test**

```go
func TestGatewayDNSProbeDegradesGracefully(t *testing.T) {
	c := probeGatewayDNS()
	if c.Title == "" {
		t.Fatal("probe check needs a title")
	}
	// No vmnet bridge in CI => must warn, never hard-fail (which would fail doctor).
	if c.Status == ProbeFail {
		t.Errorf("gateway DNS should degrade to warn, got fail: %+v", c)
	}
}
```

- [ ] **Step 2: Run → FAIL** `undefined: probeGatewayDNS`.

- [ ] **Step 3: Implement**

```go
func probeGatewayDNS() ProbeCheck {
	return probeDnsAt("192.168.64.1:"+dnsLocalPort, "container-facing DNS (gateway)", true /*failIsWarn*/)
}

func probeInContainerDNS() ProbeCheck {
	// Best-effort: pick the first alive registry entry, exec getent inside it.
	// If no sandbox is up, return an informational skip (not fail).
	// ... look up one alive entry via the registry, run
	// verifyInContainerResolution against it, map err->ProbeWarn, nil->ProbePass,
	// no-sandbox->ProbePass with "no sandbox to test".
}
```
Refactor the existing loopback DNS check into `probeDnsAt(...)`, and append `probeGatewayDNS()` + `probeInContainerDNS()` to the checks `ProbeDaemon` returns.

- [ ] **Step 4: Run → PASS.**

- [ ] **Step 5: Manual + build** — `make build && ./bin/cspace-go doctor` (sandbox up): confirm loopback, gateway, and in-container DNS lines. `make vet`.

- [ ] **Step 6: Commit**
```bash
git add internal/cli/probes.go internal/cli/probes_test.go
git commit -m "feat(doctor): probe gateway (192.168.64.1:5354) and in-container .cspace.test resolution

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Refresh stale sandbox IPs on DNS lookup (bounded + memoized)

**Files:** Modify `internal/cli/cmd_daemon.go` (single-match non-service branch, `:319-320`; give `lookupSidecarIP`/the inspect a timeout); Test `internal/cli/cmd_daemon_test.go`.

**Interfaces:** `liveSandboxIP(project, name, registryIP string) string` — the live inspected IP, else `registryIP`. Backed by `inspectContainerIP` (package seam) with a **context timeout** and a **short single-flight memo** so it isn't an unbounded subprocess per DNS query.

- [ ] **Step 1: Write the failing test**

```go
func TestLiveSandboxIP(t *testing.T) {
	orig := inspectContainerIP
	t.Cleanup(func() { inspectContainerIP = orig })

	inspectContainerIP = func(string) (string, error) { return "192.168.64.42", nil }
	if got := liveSandboxIP("p", "mercury", "192.168.64.9"); got != "192.168.64.42" {
		t.Errorf("want live 192.168.64.42, got %s", got)
	}
	inspectContainerIP = func(string) (string, error) { return "", errContainerGone }
	if got := liveSandboxIP("p", "mercury", "192.168.64.9"); got != "192.168.64.9" {
		t.Errorf("want registry fallback 192.168.64.9, got %s", got)
	}
}
```

- [ ] **Step 2: Run → FAIL** `undefined: liveSandboxIP`.

- [ ] **Step 3: Implement (timeout + memo)** — first give the existing inspect a deadline: change `lookupSidecarIP` (`cmd_daemon.go:428`) from `exec.Command` to `exec.CommandContext(ctx, ...)` with a 2s `context.WithTimeout`. Then:

```go
var errContainerGone = errors.New("container not found")

var inspectContainerIP = func(container string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return lookupSidecarIPCtx(ctx, container) // ctx-aware variant of lookupSidecarIP
}

type ipMemo struct {
	mu   sync.Mutex
	ip   string
	when time.Time
}

var sandboxIPMemo sync.Map // container -> *ipMemo

// liveSandboxIP prefers the container's currently-inspected IP over the
// registry value (which goes stale when a sandbox restarts onto a new vmnet
// IP). Memoized for the DNS TTL so it's at most one inspect per name per 5s.
// CAVEAT: when inspect can't answer (timeout/apiserver hung) it falls back to
// the registry IP, which may still be stale — the reassigned-IP hazard is
// reduced, not eliminated, in that failure window.
func liveSandboxIP(project, name, registryIP string) string {
	container := fmt.Sprintf("cspace-%s-%s", project, name)
	v, _ := sandboxIPMemo.LoadOrStore(container, &ipMemo{})
	m := v.(*ipMemo)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ip != "" && time.Since(m.when) < daemonDNSTTL*time.Second {
		return m.ip
	}
	if ip, err := inspectContainerIP(container); err == nil && ip != "" {
		m.ip, m.when = ip, time.Now()
		return ip
	}
	return registryIP
}
```
In the `case 1:` non-service branch (`cmd_daemon.go:319-320`) replace `ip = matches[0].IP` with `ip = liveSandboxIP(matches[0].Project, matches[0].Name, matches[0].IP)`. Leave the `service != ""` sub-branch unchanged.

- [ ] **Step 4: Run → PASS.** `go test ./internal/cli/ -run TestLiveSandboxIP -v`.

- [ ] **Step 5: Build + vet + commit**
```bash
make build && make vet
git add internal/cli/cmd_daemon.go internal/cli/cmd_daemon_test.go
git commit -m "fix(daemon): resolve sandbox names to the live container IP (bounded inspect + TTL memo)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Delete the dead `ServiceIPs` field (KEEP `InjectHosts`)

**Files:** Modify `internal/orchestrator/types.go`, `internal/orchestrator/lifecycle.go`, `internal/cli/browser.go` (docstring only).

**Reality check (do not skip):** `InjectHosts` (`browser.go:225`) is **live** — `InjectWorkspaceHost` (`browser.go:205`, called at `cmd_up.go:742`) returns `InjectHosts(...)`. **Do not delete `InjectHosts`.** What's dead is only `ServiceIPs()` (`types.go:75`) and the `serviceIPs` field it exposes, plus the field's write at `lifecycle.go:205`.

- [ ] **Step 1: Confirm the exact dead set**

Run: `grep -rn "ServiceIPs\|serviceIPs" internal/`
Expected: `ServiceIPs()` method + `serviceIPs` field def + one write at `lifecycle.go:205`, and **no read** of either. (If a read exists, stop — the field isn't dead.)

- [ ] **Step 2: Delete `ServiceIPs()`, the `serviceIPs` field, and its write**

Remove the `ServiceIPs()` method and the `serviceIPs` struct field from `types.go`; remove the assignment at `lifecycle.go:205` (and any now-unused local it built). Leaving the field with a write and no read fails `golangci-lint`'s `unused`.

- [ ] **Step 3: Fix the misleading docstring on `InjectHosts`**

In `browser.go`, rewrite the `InjectHosts`/`InjectWorkspaceHost` docstrings so they describe what actually happens (inject a `workspace` alias into the sidecar) and drop the "second pass giving the browser the full sibling set" claim, which never runs.

- [ ] **Step 4: Build + vet + test + commit**

```bash
make build && make vet && go test ./internal/...
git add internal/orchestrator/types.go internal/orchestrator/lifecycle.go internal/cli/browser.go
git commit -m "refactor(orchestrator): remove the dead ServiceIPs field; correct InjectHosts docstrings

The browser sidecar relies on daemon DNS, not /etc/hosts sibling injection; the
never-read serviceIPs field and its docstrings implied a feature that does not
exist. InjectHosts stays (InjectWorkspaceHost calls it). Per spec, static
/etc/hosts insurance is intentionally not adopted (stale-IP hazard, write race,
dnsmasq no-hosts mismatch).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Set `CSPACE_WORKSPACE_HOST` unconditionally

**Files:** Modify `internal/cli/cmd_up.go:636`; Test `internal/cli/cmd_up_test.go`.

- [ ] **Step 1: Write the failing test**

```go
func TestWorkspaceHostSetWithoutBrowser(t *testing.T) {
	env := map[string]string{}
	applyWorkspaceHostEnv(env, "mercury", "resume-redux")
	if env["CSPACE_WORKSPACE_HOST"] != "mercury.resume-redux.cspace.test" {
		t.Fatalf("got %q", env["CSPACE_WORKSPACE_HOST"])
	}
}
```

- [ ] **Step 2: Run → FAIL** `undefined: applyWorkspaceHostEnv`.

- [ ] **Step 3: Extract + hoist** — add `func applyWorkspaceHostEnv(env map[string]string, name, project string) { env["CSPACE_WORKSPACE_HOST"] = workspaceFriendlyHost(name, project) }`; call it **outside** the browser block; delete the assignment inside the browser block at `:636`.

- [ ] **Step 4: Run → PASS.**

- [ ] **Step 5: Build + commit**
```bash
make build && make vet
git add internal/cli/cmd_up.go internal/cli/cmd_up_test.go
git commit -m "feat(up): always set CSPACE_WORKSPACE_HOST, even under --no-browser

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: `.env.cspace` convention + docs

**Files:** Create `docs/env-cspace.md`; Modify the agent-facing guidance doc(s) that say `$(hostname)`; link from the docs index.

**Test waiver:** the spec §5 "non-login shell sees the blanked var" check is **project-side compose behavior** (compose-go `env_file` ordering), not cspace Go code — explicitly waived here; verified once manually in Task-8 Step 3.

- [ ] **Step 1: Write `docs/env-cspace.md`** — with the copy-pasteable compose block:
```yaml
env_file:
  - path: ../.env          # required: false
  - path: ../.env.cspace   # required: false — later file wins
```
Cover: `.env.cspace` is project-owned/static/committed; cspace never writes per-sandbox values into it (dynamic values ride `/sessions/extracted.env`); example `CONVEX_DEPLOYMENT=` to neutralize the cloud var; **precedence** = highest among `env_file`s only (compose `environment:`, devcontainer `containerEnv`, `.cspace/secrets.env`, `--env` all win); **naming caveat** — matches Vite/Nuxt `.env.<mode>`, so never run the app with `--mode cspace`; relationship to `.cspace/secrets.env` (secrets = cspace-delivered; `.env.cspace` = project-declared container overrides); inert on the local box.

- [ ] **Step 2: Fix the `$(hostname)` guidance + add the e2e `baseURL` convention** — in the agent-facing guidance, replace `http://$(hostname):<port>` with `http://$CSPACE_WORKSPACE_HOST:<port>`, and document that a project's Playwright `baseURL` should fall back to `http://${CSPACE_WORKSPACE_HOST}:<port>` when set (the `run-server` e2e browser is remote in the sidecar). Confirm `lib/runtime/scripts/statusline.sh:241` already surfaces the FQDN; if not, add it.

- [ ] **Step 3: Manual verification of the env override** — build a throwaway devcontainer with `.env` carrying `CONVEX_DEPLOYMENT=dev:x` and `.env.cspace` blanking it; `container exec … sh -c 'printenv CONVEX_DEPLOYMENT'` in a **non-login** shell shows empty. Note the result in the PR description.

- [ ] **Step 4: Link + commit**
```bash
git add docs/env-cspace.md docs/ lib/runtime/scripts/statusline.sh
git commit -m "docs: .env.cspace convention; point agents at CSPACE_WORKSPACE_HOST (site + e2e baseURL)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Final verification

- [ ] `make sync-embedded && make build && make vet && make test` — all green.
- [ ] `go test ./internal/cli/ -run 'Daemon|Health|Verify|LiveSandboxIP|WorkspaceHost'` (drop `-short`) — the survival + replace tests actually run.
- [ ] Manual: `cspace up <name> --no-attach`; after it returns, `pgrep -fl "cspace daemon"` shows a live daemon and `getent hosts <name>.<project>.cspace.test` resolves **inside** both the devcontainer and the browser sidecar. Restart the sandbox and confirm the name resolves to the **new** IP (Task 5).
- [ ] `cspace doctor` shows loopback, gateway, and in-container DNS healthy.
- [ ] One PR with all commits on `spec/sandbox-dns-env-isolation`.
