# Phase 1 Implementation Plan — `cspace2-*` canonical sandboxes

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote the Phase 0 prototype into canonical `cspace2-up` / `cspace2-down` / `cspace2-send` commands, with single-session parity to today's `cspace up`. Old Docker-based commands stay untouched and continue to work; users opt into the new path explicitly until cutover.

**Architecture:** Apple Container substrate (already proven in P0), Bun TS supervisor as canonical (single binary in image), host registry-daemon with auto-spawn + idle shutdown, secrets resolved from macOS Keychain → `.cspace/secrets.env` → host env. New "in-sandbox mode" lets the cspace binary skip project-root/git discovery when running inside a sandbox.

**Tech Stack:** Go (CLI, registry-daemon, substrate adapter), Bun TS (supervisor), Apple Container, macOS Keychain (`security` CLI), `gh` CLI (sandbox-side).

**Out of scope for P1 (deferred to later phases):**
- Multi-session per sandbox (P2)
- Push-based activity hub (P3)
- Context daemon, firewall removal (P4)
- Linux containerd backend (P5)
- Removing legacy Docker code (post-cutover cleanup PR)
- Playwright / chromium integration (handled by image — verified by P0 extension spike, but agents driving Playwright is P2+ work)

**Pre-P1 verification (Phase 0 extension spikes — must complete before P1 Task 1):**
- Spike: GitHub access (gh CLI + git push/pull) from inside a sandbox.
- Spike: Playwright browser session works under Apple Container.

These are documented in a separate plan addendum and committed to `phase-0-prototype` before P1 starts.

---

## File map

**New files:**

- `internal/sandboxmode/sandboxmode.go` — detects in-sandbox execution via env vars, exposes `IsInSandbox()`, `Project()`, `Name()`, `RegistryURL()` helpers. Replaces the skip-list hack in `internal/cli/root.go`.
- `internal/sandboxmode/sandboxmode_test.go`.
- `internal/secrets/keychain_darwin.go` — macOS Keychain reader/writer wrapping `security find-generic-password` and `security add-generic-password`.
- `internal/secrets/keychain_other.go` — build-tagged stub for non-darwin (returns "unsupported").
- `internal/secrets/keychain_darwin_test.go` — integration test (skipped if Keychain item not present).
- `internal/cli/cmd_cspace2_up.go` — replaces `cmd_prototype_up.go`.
- `internal/cli/cmd_cspace2_down.go` — replaces `cmd_prototype_down.go`.
- `internal/cli/cmd_cspace2_send.go` — replaces `cmd_prototype_send.go`.
- `internal/cli/cmd_registry_daemon.go` — `cspace registry-daemon {stop,status}` escape-hatch commands.
- `internal/cli/cmd_init_keychain.go` — `cspace init` flow that prompts for Anthropic key and writes to Keychain (macOS).
- `lib/templates/Dockerfile.cspace2` — renamed from `Dockerfile.prototype`. Adds `gh` CLI install.

**Modified files:**

- `internal/cli/root.go` — register `cspace2-*` commands; remove the prototype-* skip-list hack now that `internal/sandboxmode` handles in-sandbox detection cleanly.
- `internal/secrets/secrets.go` — gain a Keychain layer in the resolver (Keychain → file → host env).
- `cmd/cspace-registry-daemon/main.go` — add idle-shutdown loop.
- `Makefile` — rename `prototype-image` target to `cspace2-image`; rename `Dockerfile.prototype` references to `Dockerfile.cspace2`.

**Deleted files:**

- `internal/cli/cmd_prototype_up.go`, `cmd_prototype_down.go`, `cmd_prototype_send.go` — replaced by `cspace2-*` equivalents.
- `lib/templates/Dockerfile.prototype` — replaced by `Dockerfile.cspace2`.

**Untouched (legacy Docker path):** `internal/compose/`, `internal/docker/`, `internal/instance/`, `internal/provision/`, `lib/templates/Dockerfile`, `lib/templates/docker-compose*.yml`, `lib/agent-supervisor/` (Node), `internal/cli/cmd_up.go`, `cmd_down.go`. All deferred to a post-cutover cleanup PR.

---

## Task 1: `internal/sandboxmode` package

**Files:**

- Create: `internal/sandboxmode/sandboxmode.go`
- Create: `internal/sandboxmode/sandboxmode_test.go`

**Goal:** Centralize "am I running inside a cspace sandbox?" logic. Exports a small API that other packages (cli, secrets) consult instead of each calling `os.Getenv("CSPACE_*")` directly.

- [ ] **Step 1: Write the failing test**

Create `internal/sandboxmode/sandboxmode_test.go`:

```go
package sandboxmode

import (
	"testing"
)

func TestIsInSandboxFalseByDefault(t *testing.T) {
	t.Setenv("CSPACE_SANDBOX_NAME", "")
	if IsInSandbox() {
		t.Fatal("expected false when CSPACE_SANDBOX_NAME is unset")
	}
}

func TestIsInSandboxTrueWhenSet(t *testing.T) {
	t.Setenv("CSPACE_SANDBOX_NAME", "p1")
	if !IsInSandbox() {
		t.Fatal("expected true when CSPACE_SANDBOX_NAME is set")
	}
}

func TestProjectFromEnv(t *testing.T) {
	t.Setenv("CSPACE_PROJECT", "myproj")
	t.Setenv("CSPACE_SANDBOX_NAME", "p1")
	if got := Project(); got != "myproj" {
		t.Fatalf("Project: got %q, want %q", got, "myproj")
	}
}

func TestRegistryURL(t *testing.T) {
	t.Setenv("CSPACE_REGISTRY_URL", "http://192.168.64.1:6280")
	if got := RegistryURL(); got != "http://192.168.64.1:6280" {
		t.Fatalf("RegistryURL: got %q", got)
	}
}
```

- [ ] **Step 2: Verify the tests fail**

```bash
go test ./internal/sandboxmode/... -v
```

Expected: compile errors for undefined `IsInSandbox`, `Project`, `RegistryURL`.

- [ ] **Step 3: Implement the package**

Create `internal/sandboxmode/sandboxmode.go`:

```go
// Package sandboxmode detects whether the running cspace process is
// executing inside a sandbox. When in-sandbox, the binary should skip
// project-root / git-repo discovery and read its context from env vars
// injected by the host's cspace2-up command.
package sandboxmode

import "os"

// IsInSandbox returns true when the process is running inside a cspace sandbox.
func IsInSandbox() bool {
	return os.Getenv("CSPACE_SANDBOX_NAME") != ""
}

// Project returns the project name set by the host at sandbox-create time.
// Empty when not in a sandbox.
func Project() string {
	return os.Getenv("CSPACE_PROJECT")
}

// Name returns the sandbox's own name.
func Name() string {
	return os.Getenv("CSPACE_SANDBOX_NAME")
}

// RegistryURL returns the host registry-daemon URL injected at sandbox-create.
// Empty when not in a sandbox.
func RegistryURL() string {
	return os.Getenv("CSPACE_REGISTRY_URL")
}
```

- [ ] **Step 4: Verify tests pass**

- [ ] **Step 5: Commit**

```bash
git add internal/sandboxmode/
git commit -m "Add internal/sandboxmode: detect in-sandbox execution from env"
```

---

## Task 2: Replace skip-list hack in `root.go` with sandboxmode check

**Files:**

- Modify: `internal/cli/root.go`

**Goal:** Currently the in-sandbox cspace fails `loadConfig()` because `/workspace` isn't a git repo, and `prototype-*` commands are listed in a hardcoded skip block. Replace that with a clean `sandboxmode.IsInSandbox()` check.

- [ ] **Step 1: Read root.go to find the current skip-list and config-load block**

```bash
grep -n "PersistentPreRunE\|loadConfig\|prototype-" internal/cli/root.go
```

- [ ] **Step 2: Modify the PersistentPreRunE to skip config load when in-sandbox**

Replace the existing skip-list switch with:

```go
import (
	"github.com/elliottregan/cspace/internal/sandboxmode"
	// ... existing imports
)

// In PersistentPreRunE:
if sandboxmode.IsInSandbox() {
	return nil
}
// then existing per-command skip switch for version/help/etc.
```

The existing per-command switch for `version`, `help`, `completion`, `init`, etc. stays — it predates this change and serves a different purpose (commands that don't need a project at all, regardless of sandbox-ness).

- [ ] **Step 3: Build and verify the existing prototype tests still work**

```bash
make build
./bin/cspace-go prototype-up p1
container exec cspace-cspace-p1 cspace prototype-send p1 "test"
./bin/cspace-go prototype-down p1
```

Expected: same behavior as P0. The sandbox-mode check replaces the old skip-list with a more general primitive.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/root.go
git commit -m "Use sandboxmode for in-sandbox config-load skip"
```

---

## Task 3: Rename `prototype-*` commands to `cspace2-*`

**Files:**

- Rename: `internal/cli/cmd_prototype_up.go` → `cmd_cspace2_up.go`
- Rename: `internal/cli/cmd_prototype_down.go` → `cmd_cspace2_down.go`
- Rename: `internal/cli/cmd_prototype_send.go` → `cmd_cspace2_send.go`
- Modify: `internal/cli/root.go` — register new names, drop old.
- Rename: `lib/templates/Dockerfile.prototype` → `lib/templates/Dockerfile.cspace2`
- Modify: `Makefile` — rename `prototype-image` target → `cspace2-image`.

**Goal:** Drop the "prototype" framing now that the work has graduated. `cspace2-*` is the staging name through P1–P2 testing; cutover renames `cspace2-*` → `cspace *` (and removes the Docker path).

- [ ] **Step 1: Rename the three command files via git mv**

```bash
git mv internal/cli/cmd_prototype_up.go internal/cli/cmd_cspace2_up.go
git mv internal/cli/cmd_prototype_down.go internal/cli/cmd_cspace2_down.go
git mv internal/cli/cmd_prototype_send.go internal/cli/cmd_cspace2_send.go
```

- [ ] **Step 2: In each file, rename the command and constructor**

For `cmd_cspace2_up.go`:
- Function `newPrototypeUpCmd` → `newCspace2UpCmd`
- `Use: "prototype-up <name>"` → `Use: "cspace2-up <name>"`
- `Short: "P0: launch a prototype sandbox"` → `Short: "Launch a sandbox (Apple Container substrate)"`

Same shape for down and send.

- [ ] **Step 3: Update `root.go`**

Replace the three `root.AddCommand(newPrototype*Cmd())` calls with `root.AddCommand(newCspace2*Cmd())`.

- [ ] **Step 4: Rename Dockerfile and update Makefile**

```bash
git mv lib/templates/Dockerfile.prototype lib/templates/Dockerfile.cspace2
```

In `Makefile`, find:

```make
.PHONY: prototype-image
prototype-image: cspace-linux
	container build \
		--tag cspace-prototype:latest \
		--file lib/templates/Dockerfile.prototype \
		--platform linux/arm64 \
		.
```

Replace with:

```make
.PHONY: cspace2-image
cspace2-image: cspace-linux
	container build \
		--tag cspace2:latest \
		--file lib/templates/Dockerfile.cspace2 \
		--platform linux/arm64 \
		.
```

In each renamed `cmd_cspace2_*.go`, change `Image: "cspace-prototype:latest"` → `Image: "cspace2:latest"`.

- [ ] **Step 5: Update container name prefix**

In `cmd_cspace2_up.go` and `cmd_cspace2_down.go`, change container name template from:

```go
containerName := fmt.Sprintf("cspace-%s-%s", project, name)
```

to:

```go
containerName := fmt.Sprintf("cspace2-%s-%s", project, name)
```

(Different prefix avoids collision with old `cspace up` containers and with stale prototype containers from P0 testing.)

- [ ] **Step 6: Build, smoke test**

```bash
make build && make cspace2-image
./bin/cspace-go cspace2-up s1
container ls | grep cspace2-cspace-s1
./bin/cspace-go cspace2-send s1 "hello"
./bin/cspace-go cspace2-down s1
```

Expected: full lifecycle works under the new names.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "Rename prototype-* commands to cspace2-*; image to cspace2:latest"
```

---

## Task 4: Add `gh` CLI to the sandbox image

**Files:**

- Modify: `lib/templates/Dockerfile.cspace2`

**Goal:** Agents inside sandboxes need GitHub access (gh CLI for issue/PR management, git for clone/push). git is already installed by Task 3's Dockerfile; add gh.

- [ ] **Step 1: Append gh install to the Dockerfile**

Find the `apt-get install` block in `lib/templates/Dockerfile.cspace2`. Append:

```dockerfile
# GitHub CLI. Apt repo install (the static binary release is also fine; this
# matches the pattern in the existing cspace Dockerfile).
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | tee /usr/share/keyrings/githubcli-archive-keyring.gpg > /dev/null \
 && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
 && apt-get update && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/* \
 && gh --version
```

- [ ] **Step 2: Rebuild image and verify**

```bash
make cspace2-image
container run --rm cspace2:latest gh --version
container run --rm cspace2:latest git --version
```

- [ ] **Step 3: End-to-end test with GH_TOKEN**

If you have a `GH_TOKEN` you can use:

```bash
echo "GH_TOKEN=$YOUR_TOKEN" >> .cspace/secrets.env
./bin/cspace-go cspace2-up gh-test
container exec cspace2-cspace-gh-test sh -c 'echo "GH_TOKEN set: $(test -n "$GH_TOKEN" && echo yes || echo no)"; gh auth status; gh repo view elliottregan/cspace --json name,description'
./bin/cspace-go cspace2-down gh-test
sed -i.bak '/GH_TOKEN=/d' .cspace/secrets.env && rm .cspace/secrets.env.bak
```

Expected: `gh auth status` reports authenticated; `gh repo view` returns repo metadata.

- [ ] **Step 4: Commit**

```bash
git add lib/templates/Dockerfile.cspace2
git commit -m "Add gh CLI to sandbox image; GH_TOKEN flows via .cspace/secrets.env"
```

---

## Task 5: Registry-daemon idle shutdown + escape-hatch commands

**Files:**

- Modify: `cmd/cspace-registry-daemon/main.go` — add idle shutdown after 30 min of no requests AND no active registry entries.
- Create: `internal/cli/cmd_registry_daemon.go` — `cspace registry-daemon {stop,status}`.
- Modify: `internal/cli/root.go` — register the new subcommand.

**Goal:** Daemon "just works." Auto-spawn unchanged. Add idle-shutdown so the daemon doesn't accumulate. Stop/status are escape hatches for debugging only.

- [ ] **Step 1: Add idle-shutdown loop to the daemon**

In `cmd/cspace-registry-daemon/main.go`, before `http.ListenAndServe`, add:

```go
// Idle shutdown: exit when no requests have arrived for IDLE_TIMEOUT
// AND the registry has no active entries. Both conditions must be true
// to avoid shutting down while sandboxes are running but quiet.
const idleTimeout = 30 * time.Minute

var lastActivity atomic.Int64
lastActivity.Store(time.Now().Unix())

bumpActivity := func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		lastActivity.Store(time.Now().Unix())
		next.ServeHTTP(w, req)
	})
}

go func() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for range tick.C {
		idleSince := time.Since(time.Unix(lastActivity.Load(), 0))
		if idleSince < idleTimeout {
			continue
		}
		entries, err := r.List()
		if err != nil || len(entries) > 0 {
			continue
		}
		log.Printf("cspace-registry-daemon: idle %s with no entries; exiting", idleSince)
		os.Exit(0)
	}
}()

addr := bindAddr + ":" + port
log.Printf("cspace-registry-daemon: listening on %s", addr)
if err := http.ListenAndServe(addr, bumpActivity(mux)); err != nil {
	log.Fatal(err)
}
```

(Imports: add `sync/atomic`, `time`. The `time` import is likely already present.)

- [ ] **Step 2: Implement `cspace registry-daemon` parent command + subcommands**

Create `internal/cli/cmd_registry_daemon.go`:

```go
package cli

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRegistryDaemonCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "registry-daemon",
		Short: "Manage the host cspace registry daemon (debugging / cleanup)",
	}
	parent.AddCommand(newRegistryDaemonStatusCmd())
	parent.AddCommand(newRegistryDaemonStopCmd())
	return parent
}

func newRegistryDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print whether the registry daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get("http://127.0.0.1:6280/health")
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: not running")
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: running on 127.0.0.1:6280")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "registry-daemon: unexpected status %d\n", resp.StatusCode)
			}
			return nil
		},
	}
}

func newRegistryDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the registry daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("pkill", "-f", "cspace-registry-daemon").CombinedOutput()
			if err != nil && !strings.Contains(string(out), "no process") {
				return fmt.Errorf("pkill: %w (%s)", err, out)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: stopped")
			return nil
		},
	}
}
```

- [ ] **Step 3: Register in `root.go`**

```go
root.AddCommand(newRegistryDaemonCmd())
```

- [ ] **Step 4: Build and smoke test**

```bash
make build registry-daemon
./bin/cspace-go registry-daemon status     # not running
./bin/cspace-go cspace2-up p1               # auto-spawns daemon
./bin/cspace-go registry-daemon status     # running
./bin/cspace-go cspace2-down p1
./bin/cspace-go registry-daemon stop       # explicit stop
./bin/cspace-go registry-daemon status     # not running
```

Expected: status reflects state correctly; stop kills the daemon.

- [ ] **Step 5: Commit**

```bash
git add cmd/cspace-registry-daemon/ internal/cli/cmd_registry_daemon.go internal/cli/root.go
git commit -m "Registry-daemon: idle shutdown + cspace registry-daemon stop/status"
```

---

## Task 6: macOS Keychain integration in secrets resolver

**Files:**

- Create: `internal/secrets/keychain_darwin.go`
- Create: `internal/secrets/keychain_other.go`
- Modify: `internal/secrets/secrets.go` — add Keychain layer.
- Create: `internal/secrets/keychain_darwin_test.go` (skipped if Keychain item not present).

**Goal:** On macOS, the loader checks Keychain first. Resolver order: Keychain → `~/.cspace/secrets.env` → `<project>/.cspace/secrets.env` → host env. The file format gains a `keychain:<service-name>` value-prefix so users can put keychain references in plain text.

- [ ] **Step 1: Implement the macOS Keychain helper**

Create `internal/secrets/keychain_darwin.go`:

```go
//go:build darwin

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// readKeychain reads a generic password from the macOS Keychain.
// Service name convention: "cspace-<key>" so e.g. ANTHROPIC_API_KEY
// resolves to service "cspace-ANTHROPIC_API_KEY".
// Returns "" with no error if the item is not present.
func readKeychain(serviceName string) (string, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", serviceName, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(stderr.String(), "could not be found") {
			return "", nil
		}
		return "", fmt.Errorf("security find-generic-password %s: %w (%s)",
			serviceName, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// writeKeychain writes a generic password to the macOS Keychain. Idempotent —
// updates the existing item if one already exists.
func writeKeychain(serviceName, password string) error {
	cmd := exec.Command("security", "add-generic-password",
		"-s", serviceName, "-a", "cspace", "-w", password, "-U")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("security add-generic-password %s: %w (%s)",
			serviceName, err, out)
	}
	return nil
}
```

- [ ] **Step 2: Build-tagged stub for non-darwin**

Create `internal/secrets/keychain_other.go`:

```go
//go:build !darwin

package secrets

func readKeychain(serviceName string) (string, error)         { return "", nil }
func writeKeychain(serviceName, password string) error        { return nil }
```

- [ ] **Step 3: Wire Keychain into the resolver**

In `internal/secrets/secrets.go`, modify `Load` to check Keychain for each declared key. Two integration points:

A. **Keychain prefix in file values.** When parsing, if a value is `keychain:<service-name>`, resolve via `readKeychain(<service-name>)` at load time.

B. **Keychain-first lookup for known cspace keys.** `Load` always tries Keychain for `ANTHROPIC_API_KEY` (and other declared cspace keys) under service name `cspace-<KEY>`. File and env override.

Implementation:

```go
// At the top of secrets.go, declare the canonical key list:
var cspaceKeys = []string{"ANTHROPIC_API_KEY", "GH_TOKEN", "GITHUB_TOKEN"}

func Load(projectRoot string) (map[string]string, error) {
	out := map[string]string{}

	// Layer 1: Keychain for known cspace keys.
	for _, key := range cspaceKeys {
		val, err := readKeychain("cspace-" + key)
		if err != nil {
			return nil, err
		}
		if val != "" {
			out[key] = val
		}
	}

	// Layer 2: ~/.cspace/secrets.env, then <project>/.cspace/secrets.env.
	home, _ := os.UserHomeDir()
	if home != "" {
		if err := mergeFile(out, filepath.Join(home, ".cspace", "secrets.env")); err != nil {
			return nil, err
		}
	}
	if projectRoot != "" && projectRoot != home {
		if err := mergeFile(out, filepath.Join(projectRoot, ".cspace", "secrets.env")); err != nil {
			return nil, err
		}
	}

	// Layer 3: resolve `keychain:<service>` value-prefix references.
	for k, v := range out {
		if strings.HasPrefix(v, "keychain:") {
			service := strings.TrimPrefix(v, "keychain:")
			resolved, err := readKeychain(service)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
	}

	return out, nil
}
```

(Update the existing `mergeFile` and `parse` to accept the same map; merge in place. Add `strings` to imports.)

- [ ] **Step 4: Add a Keychain integration test**

Create `internal/secrets/keychain_darwin_test.go`:

```go
//go:build darwin

package secrets

import "testing"

func TestKeychainRoundtrip(t *testing.T) {
	const service = "cspace-test-roundtrip"
	const value = "test-value-123"

	t.Cleanup(func() {
		// Clean up the keychain item to keep the test idempotent.
		_ = exec.Command("security", "delete-generic-password", "-s", service).Run()
	})

	if err := writeKeychain(service, value); err != nil {
		t.Skip("Keychain write failed (likely no auth in test env):", err)
	}

	got, err := readKeychain(service)
	if err != nil {
		t.Fatalf("readKeychain: %v", err)
	}
	if got != value {
		t.Fatalf("got %q, want %q", got, value)
	}
}
```

(Add `os/exec` to imports.)

- [ ] **Step 5: Build, run all tests**

```bash
go test ./internal/secrets/... -v
```

Expected: all P0 tests still pass; new Keychain test passes (or skips if Keychain unauthorized).

- [ ] **Step 6: Smoke test end-to-end**

```bash
security add-generic-password -s "cspace-ANTHROPIC_API_KEY" -a cspace -w "test-key-from-keychain" -U
./bin/cspace-go cspace2-up keytest
container exec cspace2-cspace-keytest sh -c 'echo "API: $ANTHROPIC_API_KEY"'
./bin/cspace-go cspace2-down keytest
security delete-generic-password -s "cspace-ANTHROPIC_API_KEY"
```

Expected: container env shows `API: test-key-from-keychain`.

- [ ] **Step 7: Commit**

```bash
git add internal/secrets/
git commit -m "Add macOS Keychain layer to secrets resolver (cspace-<KEY> services)"
```

---

## Task 7: `cspace init` — Keychain prompt for Anthropic key

**Files:**

- Create: `internal/cli/cmd_init_keychain.go` (NOT `cmd_init.go` — that exists for the legacy init flow).
- Modify: `internal/cli/cmd_init.go` (or wherever the existing `cspace init` lives) to invoke the new prompt OR add a new `cspace init-keychain` subcommand. Pick whichever is less invasive.

**Goal:** First-run UX: ask the user for `ANTHROPIC_API_KEY`, offer to store it in Keychain. macOS only — Linux gets a "skipped on this platform" message and a doc link.

- [ ] **Step 1: Read the existing `cspace init` flow**

```bash
cat internal/cli/init_cmd.go
```

Decide whether to extend it or add a sibling subcommand. If `init` already does substantial work, add a sibling `cspace init-keychain` subcommand for clarity.

- [ ] **Step 2: Implement the prompt**

Create `internal/cli/cmd_init_keychain.go`:

```go
package cli

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/elliottregan/cspace/internal/secrets"
	"github.com/spf13/cobra"
)

func newInitKeychainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init-keychain",
		Short: "Prompt for cspace secrets and store them in macOS Keychain",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime.GOOS != "darwin" {
				fmt.Fprintln(cmd.OutOrStdout(),
					"Keychain integration is macOS-only. On Linux, use ~/.cspace/secrets.env.")
				return nil
			}
			reader := bufio.NewReader(os.Stdin)

			fmt.Fprint(cmd.OutOrStdout(), "Enter ANTHROPIC_API_KEY (leave blank to skip): ")
			line, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			val := strings.TrimSpace(line)
			if val == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "skipped.")
				return nil
			}
			if err := secrets.WriteKeychain("cspace-ANTHROPIC_API_KEY", val); err != nil {
				return fmt.Errorf("write keychain: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "stored in Keychain (service: cspace-ANTHROPIC_API_KEY).")
			return nil
		},
	}
}
```

- [ ] **Step 3: Export `WriteKeychain` from the secrets package**

In `internal/secrets/keychain_darwin.go` and `internal/secrets/keychain_other.go`, rename `writeKeychain` → `WriteKeychain` (export). Same for `readKeychain` → `ReadKeychain` if external callers want it (keep private otherwise).

- [ ] **Step 4: Register subcommand in `root.go`**

```go
root.AddCommand(newInitKeychainCmd())
```

- [ ] **Step 5: Smoke test**

```bash
echo "test-key-from-prompt" | ./bin/cspace-go init-keychain
security find-generic-password -s "cspace-ANTHROPIC_API_KEY" -w
# should print: test-key-from-prompt
security delete-generic-password -s "cspace-ANTHROPIC_API_KEY"
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cmd_init_keychain.go internal/secrets/ internal/cli/root.go
git commit -m "Add cspace init-keychain: prompt for ANTHROPIC_API_KEY into Keychain"
```

---

## Task 8: Substrate adapter hardening

**Files:**

- Modify: `internal/substrate/applecontainer/adapter.go`

**Goal:** Address concerns flagged by P0's Task 2 implementer: apiserver health preflight + run-then-exec race timing.

- [ ] **Step 1: Add `HealthCheck` to the Substrate interface**

In `internal/substrate/substrate.go`:

```go
type Substrate interface {
	Available() bool
	HealthCheck(ctx context.Context) error // new
	Run(...) error
	// ...
}
```

- [ ] **Step 2: Implement `HealthCheck` for Apple Container**

In `internal/substrate/applecontainer/adapter.go`:

```go
// HealthCheck verifies the apiserver is running. Returns a clear error if
// `container system status` reports the runtime as unavailable.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "container", "system", "status").CombinedOutput()
	if err != nil {
		return fmt.Errorf("container system status: %w (%s)", err, out)
	}
	if !strings.Contains(string(out), "running") {
		return fmt.Errorf("apiserver not running: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 3: Call `HealthCheck` in `cspace2-up` before `Run`**

In `cmd_cspace2_up.go`, after `Available()` check:

```go
if err := a.HealthCheck(ctx); err != nil {
	return fmt.Errorf("apple container apiserver: %w. Run `container system start` and try again.", err)
}
```

- [ ] **Step 4: Tests**

Add `TestHealthCheck` to `internal/substrate/applecontainer/adapter_test.go`:

```go
func TestHealthCheck(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	if err := a.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}
```

- [ ] **Step 5: Build, test, smoke**

```bash
go test ./internal/substrate/applecontainer/...
make build
./bin/cspace-go cspace2-up p1
./bin/cspace-go cspace2-down p1
```

- [ ] **Step 6: Commit**

```bash
git add internal/substrate/
git commit -m "Substrate: add HealthCheck (apiserver preflight) and call from cspace2-up"
```

---

## Task 9: Integration test — full cspace2 lifecycle

**Files:**

- Create: `internal/cli/cmd_cspace2_integration_test.go`

**Goal:** End-to-end test in CI covering: image build precondition, cspace2-up, cspace2-send (host → sandbox), cspace2-send (sandbox → sandbox), cspace2-down. Skipped if `container` CLI absent.

- [ ] **Step 1: Implement the test**

Create `internal/cli/cmd_cspace2_integration_test.go`:

```go
//go:build darwin

package cli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

func TestCspace2Lifecycle(t *testing.T) {
	a := applecontainer.New()
	if !a.Available() {
		t.Skip("container CLI not available")
	}
	if err := a.HealthCheck(context.Background()); err != nil {
		t.Skip("apiserver not running:", err)
	}

	// Verify image exists; if not, skip rather than building it in-test.
	if out, err := exec.Command("container", "images").CombinedOutput(); err != nil ||
		!bytes.Contains(out, []byte("cspace2:latest")) {
		t.Skip("cspace2:latest image not built; run `make cspace2-image` first")
	}

	cspace, err := exec.LookPath("cspace-go")
	if err != nil {
		// Fall back to local build output.
		cspace = "../../bin/cspace-go"
	}

	name := "cspace2-int-" + randSuffix()
	t.Cleanup(func() { _ = exec.Command(cspace, "cspace2-down", name).Run() })

	if out, err := exec.Command(cspace, "cspace2-up", name).CombinedOutput(); err != nil {
		t.Fatalf("cspace2-up: %v (%s)", err, out)
	}
	time.Sleep(3 * time.Second)

	if out, err := exec.Command(cspace, "cspace2-send", name, "ping").CombinedOutput(); err != nil {
		t.Fatalf("cspace2-send: %v (%s)", err, out)
	}
	if !strings.Contains(string(mustExec(t, "container", "exec", "cspace2-cspace-"+name,
		"tail", "-n", "20", "/sessions/primary/events.ndjson")), "user-turn") {
		t.Fatal("expected user-turn line in events.ndjson")
	}
}

func mustExec(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("exec %v: %v (%s)", args, err, out)
	}
	return string(out)
}

func randSuffix() string {
	// reuse the helper from another test if available; otherwise inline.
	return time.Now().Format("150405")
}
```

- [ ] **Step 2: Run the test**

```bash
make cspace2-image
go test ./internal/cli/... -v -run TestCspace2Lifecycle
```

- [ ] **Step 3: Commit**

```bash
git add internal/cli/cmd_cspace2_integration_test.go
git commit -m "Add cspace2 lifecycle integration test"
```

---

## Task 10: P1 demo run + commit verification doc

**Files:**

- Create: `docs/superpowers/reports/2026-05-XX-phase-1-verification.md` (replace XX with completion date)

**Goal:** Run the same end-to-end demo as the P0 report but on the new `cspace2-*` commands, showing parity. Capture evidence. Mark P1 done.

- [ ] **Step 1: Run the demo**

```bash
make build && make cspace2-image && make registry-daemon
./bin/cspace-go cspace2-up A 2>&1 | tee /tmp/p1-up-A.log
./bin/cspace-go cspace2-up B 2>&1 | tee /tmp/p1-up-B.log
sleep 5
./bin/cspace-go cspace2-send A "host says hello" 2>&1 | tee /tmp/p1-host-A.log
container exec cspace2-cspace-A cspace cspace2-send B "A says hi to B" 2>&1 | tee /tmp/p1-A-B.log
sleep 5
container exec cspace2-cspace-A tail -n 20 /sessions/primary/events.ndjson > /tmp/p1-A-events.ndjson
container exec cspace2-cspace-B tail -n 20 /sessions/primary/events.ndjson > /tmp/p1-B-events.ndjson
./bin/cspace-go cspace2-down A
./bin/cspace-go cspace2-down B
./bin/cspace-go registry-daemon stop
```

- [ ] **Step 2: Write the verification report**

Mirror the P0 report format. Five sections:
- Lifecycle parity (cspace2-up/-send/-down works end-to-end)
- Cross-sandbox messaging still works under new names
- Keychain integration verified (cspace init-keychain → cspace2-up reads from Keychain)
- gh CLI accessible inside sandbox with GH_TOKEN flow
- Registry-daemon idle shutdown verified (start, leave idle 30 min, observe exit)

Cite commits and `/tmp/p1-*` log files.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/reports/
git commit -m "P1 verification report: cspace2-* parity + Keychain + gh + idle daemon"
```

---

## Self-review notes

- All decisions captured: simple secrets delivery (`-e` retained), invisible registry-daemon (auto-spawn + idle shutdown), Keychain-first secrets resolution, single 100MB binary in sandbox, sandboxmode package replacing skip-list hack, cspace2-* naming for the cutover testing period, Docker code untouched.
- gh CLI added to image (Task 4) — answers user's GitHub access concern.
- ANTHROPIC_API_KEY can come from Keychain (Task 6) or `.cspace/secrets.env` (already from P0) or host shell env. Loader hierarchy is explicit.
- All P0 risks addressed in P1 except #4 (vminitd env leak — explicitly deferred per user direction).
- Type / signature consistency checked: `IsInSandbox`, `Project`, `Name`, `RegistryURL` defined Task 1, used Task 2. `ReadKeychain`/`WriteKeychain` exported Task 6/7. `HealthCheck` added to Substrate interface Task 8, called from `cspace2-up` Task 8 step 3.
- Commit cadence: ~10 commits.
- Estimated total: 1.5–2 weeks for an experienced engineer; longer if Keychain prompt UX needs iteration.
