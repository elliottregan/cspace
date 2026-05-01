# Phase 0 Prototype Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a minimum viable prototype proving the five Phase 0 dealbreakers from the [sandbox architecture spec](../specs/2026-04-30-sandbox-architecture-design.md): Apple Container boot, Claude Code installs and runs inside, direct user-turn messaging from host to sandbox, cross-sandbox messaging, and Apple Container's networking model — plus a Linux/containerd feasibility spike.

**Architecture:** Apple Container backend behind a `Substrate` Go interface; one container = one sandbox = one minimum viable Bun TS supervisor managing one session; host-side `~/.cspace/sandbox-registry.json` resolves sandbox names to control ports; in-sandbox `cspace` binary uses a small host registry-daemon for sibling lookups. All prototype code is namespaced (`prototype-*` commands, `Dockerfile.prototype`, `lib/agent-supervisor-bun/`) so it sits beside the existing cspace without disturbing it.

**Tech Stack:** Go (CLI, substrate adapter, registry daemon), Bun + TypeScript (supervisor, single binary via `bun build --compile`), Apple Container (macOS 26+), `@anthropic-ai/claude-agent-sdk`.

---

## Working assumptions (verified up front)

- macOS 26.4 (Darwin 25.4) — confirmed.
- `container` CLI not yet installed — Task 1 installs it.
- Apple Silicon (Linux microVMs run `linux/arm64`) — assumed.
- `bun` is installed on the host (or installed as part of Task 4).
- Existing `lib/agent-supervisor/` (Node/pnpm) is untouched during P0; the new Bun supervisor lives at `lib/agent-supervisor-bun/`.

## File map

**New files:**

- `internal/substrate/substrate.go` — `Substrate` interface + shared types.
- `internal/substrate/applecontainer/adapter.go` — adapter implementation.
- `internal/substrate/applecontainer/adapter_test.go` — integration tests (require `container` CLI).
- `internal/registry/registry.go` — read/write `~/.cspace/sandbox-registry.json`.
- `internal/registry/registry_test.go`.
- `internal/cli/cmd_prototype_up.go` — `cspace prototype-up <name>`.
- `internal/cli/cmd_prototype_send.go` — `cspace prototype-send <name> <text>`.
- `internal/cli/cmd_prototype_down.go` — `cspace prototype-down <name>` (cleanup).
- `cmd/cspace-registry-daemon/main.go` — host registry HTTP daemon.
- `lib/agent-supervisor-bun/package.json`.
- `lib/agent-supervisor-bun/tsconfig.json`.
- `lib/agent-supervisor-bun/src/main.ts` — entry point, HTTP server.
- `lib/agent-supervisor-bun/src/prompt-stream.ts` — async-queue prompt stream.
- `lib/agent-supervisor-bun/src/prompt-stream.test.ts`.
- `lib/agent-supervisor-bun/src/claude-runner.ts` — wraps SDK `query()`.
- `lib/agent-supervisor-bun/build.ts` — invokes `Bun.build` with `--compile`.
- `lib/templates/Dockerfile.prototype` — sandbox image (Bun + Claude Code + cspace + supervisor binary).
- `docs/superpowers/spikes/2026-04-30-apple-container-spike.md` — Task 1 findings.
- `docs/superpowers/spikes/2026-04-30-containerd-spike.md` — Task 10 findings.
- `docs/superpowers/reports/2026-05-XX-phase-0-prototype-report.md` — final P0 report.

**Modified files:**

- `internal/cli/root.go` — register the three new commands.
- `Makefile` — targets for cross-compiling `cspace` to `linux/arm64` and building the Bun supervisor binary.

**Untouched during P0:** existing `lib/agent-supervisor/`, `internal/docker/`, `internal/compose/`, `internal/instance/`, `internal/provision/`, `lib/templates/Dockerfile`. P1 will replace these.

---

## Task 1: Apple Container exploration spike

**Files:**

- Create: `docs/superpowers/spikes/2026-04-30-apple-container-spike.md`

**Goal:** Get `container` installed, build a hello-world image, run it, exec into it, document the networking model. Output is a written spike report — no production code in this task.

- [ ] **Step 1: Install Apple Container CLI**

Apple ships `container` via [github.com/apple/container/releases](https://github.com/apple/container/releases). Install the latest release:

```bash
# Either: download the .pkg from the latest release and install via GUI/installer.
# Or via Homebrew if a tap exists:
brew install --cask container || true
# Verify:
container --version
container system start
```

Expected: `container` prints a version. `container system start` initializes the runtime VM.

If install fails, document the failure mode in the spike report and stop the prototype until resolved.

- [ ] **Step 2: Pull and run a hello-world image**

```bash
container image pull docker.io/library/alpine:latest
container run --rm docker.io/library/alpine:latest echo hello-from-microvm
```

Expected: `hello-from-microvm` printed. Note the boot time using `time`.

Record in the spike report: cold-pull time, cold-run time, warm-run time.

- [ ] **Step 3: Probe the networking model**

```bash
# Start a long-running container in the background:
container run -d --name probe1 docker.io/library/alpine:latest sleep 600
container run -d --name probe2 docker.io/library/alpine:latest sleep 600

# Get IPs:
container ls
container inspect probe1
container inspect probe2

# From host, ping each container's IP:
ping -c1 <probe1-ip>
ping -c1 <probe2-ip>

# From inside probe1, ping host's gateway and probe2:
container exec probe1 sh -c 'ip route; ip addr; ping -c1 <probe2-ip>; ping -c1 <host-gateway>'

# Cleanup:
container stop probe1 probe2 && container rm probe1 probe2
```

Record in the spike report: container IP scheme, host-to-container reachability, container-to-container reachability, container-to-host reachability (gateway IP), whether `--publish` / hostfwd is needed for control ports given the answers.

- [ ] **Step 4: Probe exec, logs, mounts, and stop semantics**

```bash
container run -d --name probe3 -v "$PWD:/workspace" docker.io/library/alpine:latest sleep 600
container exec probe3 ls /workspace             # bind mount visible?
container exec probe3 sh -c 'echo hi > /tmp/x'  # writes survive?
container exec probe3 cat /tmp/x
container logs probe3                            # logs work?
container stop probe3                            # graceful stop?
container rm probe3
```

Record in the spike report: bind mount semantics (RO vs RW, propagation), `exec` stdout/stderr stream behavior, `logs` follow vs one-shot, stop signal behavior (SIGTERM grace period).

- [ ] **Step 5: Write the spike report**

Create `docs/superpowers/spikes/2026-04-30-apple-container-spike.md` with sections:

```markdown
# Apple Container Spike — 2026-04-30

## Environment
- macOS 26.4 (Darwin 25.4)
- container version: <fill from step 1>
- Apple Silicon: <yes/no>

## Boot times
- Image pull (alpine): <fill>
- Cold run: <fill>
- Warm run: <fill>

## Networking
- IP scheme: <fill> (e.g. each container gets 192.168.64.X)
- Host → container: <reachable / not>
- Container → container: <reachable / not>
- Container → host gateway: <IP / mechanism>
- Conclusion: <hostfwd needed / direct IP works>

## Exec / logs / mounts / stop
- Bind mounts: <RO / RW>
- exec stdout/stderr: <interleaved / separated>
- logs: <follow supported / one-shot>
- stop: <SIGTERM grace / immediate>

## Surprises and gotchas
<fill from probing>

## Implication for cspace P0
<one-paragraph synthesis: simpler messaging via direct IP, or hostfwd registry as designed>
```

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/spikes/2026-04-30-apple-container-spike.md
git commit -m "Spike Apple Container CLI: boot, network, exec, mount semantics"
```

---

## Task 2: Substrate interface + Apple Container adapter (Build/Run/Exec/Stop)

**Files:**

- Create: `internal/substrate/substrate.go`
- Create: `internal/substrate/applecontainer/adapter.go`
- Create: `internal/substrate/applecontainer/adapter_test.go`

**Goal:** Wrap `container` CLI in a Go interface, with the minimal surface P0 needs: `Build`, `Run`, `Exec`, `Stop`.

- [ ] **Step 1: Write the failing interface test**

Create `internal/substrate/applecontainer/adapter_test.go`:

```go
package applecontainer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// requireContainerCLI skips the test if `container` is not on PATH.
func requireContainerCLI(t *testing.T) {
	t.Helper()
	a := New()
	if !a.Available() {
		t.Skip("Apple Container CLI not installed; skipping integration test")
	}
}

func TestRunAndExecAlpine(t *testing.T) {
	requireContainerCLI(t)
	a := New()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := "cspace-test-" + randSuffix()
	t.Cleanup(func() { _ = a.Stop(context.Background(), name) })

	if err := a.Run(ctx, RunSpec{
		Name:    name,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	res, err := a.Exec(ctx, name, []string{"echo", "hello"}, ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("expected stdout to contain 'hello', got %q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", res.ExitCode)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/substrate/applecontainer/... -run TestRunAndExecAlpine -v
```

Expected: compile error referencing undefined `New`, `RunSpec`, `Run`, `Exec`, `Stop`, `ExecOpts`, `Available`, `randSuffix`.

- [ ] **Step 3: Define the substrate interface**

Create `internal/substrate/substrate.go`:

```go
// Package substrate is the cspace runtime abstraction for OCI-compatible
// sandbox backends. Adapters live in subpackages (applecontainer,
// containerd, ...). The interface is intentionally minimal during P0;
// it grows in P1 as more capabilities are needed.
package substrate

import "context"

// RunSpec is everything an adapter needs to start a sandbox.
type RunSpec struct {
	Name        string            // unique container name
	Image       string            // OCI image reference
	Command     []string          // entrypoint override (empty = use image default)
	Env         map[string]string // environment variables
	Mounts      []Mount           // host-to-container bind mounts
	PublishPort []PortMap         // ports to publish on the host
}

// Mount is a host-to-container bind mount.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// PortMap maps a host port to a container port.
type PortMap struct {
	HostPort      int
	ContainerPort int
}

// ExecOpts controls a one-shot exec inside a running sandbox.
type ExecOpts struct {
	WorkDir string
	Env     map[string]string
}

// ExecResult captures the result of an Exec call.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Substrate is implemented by each backend (Apple Container, containerd, ...).
type Substrate interface {
	Available() bool
	Run(ctx context.Context, spec RunSpec) error
	Exec(ctx context.Context, name string, cmd []string, opts ExecOpts) (ExecResult, error)
	Stop(ctx context.Context, name string) error
	IP(ctx context.Context, name string) (string, error)
}
```

- [ ] **Step 4: Implement the Apple Container adapter**

Create `internal/substrate/applecontainer/adapter.go`:

```go
// Package applecontainer implements the substrate.Substrate interface
// against Apple's `container` CLI (github.com/apple/container).
package applecontainer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/elliottregan/cspace/internal/substrate"
)

// Adapter wraps the `container` CLI.
type Adapter struct{}

// New returns a fresh Adapter.
func New() *Adapter { return &Adapter{} }

// Available returns true if `container` is on PATH.
func (a *Adapter) Available() bool {
	_, err := exec.LookPath("container")
	return err == nil
}

// Run starts a detached sandbox.
func (a *Adapter) Run(ctx context.Context, spec substrate.RunSpec) error {
	args := []string{"run", "-d", "--name", spec.Name}
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for _, m := range spec.Mounts {
		mount := fmt.Sprintf("%s:%s", m.HostPath, m.ContainerPath)
		if m.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}
	for _, p := range spec.PublishPort {
		args = append(args, "--publish",
			fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)

	cmd := exec.CommandContext(ctx, "container", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container run %s: %w (stderr: %s)",
			spec.Name, err, stderr.String())
	}
	return nil
}

// Exec runs a command in a running sandbox and captures stdout/stderr.
func (a *Adapter) Exec(ctx context.Context, name string, cmdLine []string, opts substrate.ExecOpts) (substrate.ExecResult, error) {
	args := []string{"exec"}
	for k, v := range opts.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	if opts.WorkDir != "" {
		args = append(args, "-w", opts.WorkDir)
	}
	args = append(args, name)
	args = append(args, cmdLine...)

	cmd := exec.CommandContext(ctx, "container", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exit := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit = exitErr.ExitCode()
		err = nil
	}
	return substrate.ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
	}, err
}

// Stop stops and removes a sandbox. Idempotent (no error if not present).
func (a *Adapter) Stop(ctx context.Context, name string) error {
	_ = exec.CommandContext(ctx, "container", "stop", name).Run()
	_ = exec.CommandContext(ctx, "container", "rm", name).Run()
	return nil
}

// IP returns the sandbox's primary IPv4 address (parsed from `container inspect`).
func (a *Adapter) IP(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, "container", "inspect", "--format", "{{.NetworkSettings.IPAddress}}", name).Output()
	if err != nil {
		return "", fmt.Errorf("container inspect %s: %w", name, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("container %s has no IP", name)
	}
	return ip, nil
}

// randSuffix returns a short hex suffix for unique test names.
func randSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// allocPort returns a random ephemeral host port (used by callers
// that need to publish a port and then read it back).
func allocPort() (int, error) {
	// Use net.Listen on :0 to grab a free port from the kernel.
	// Returned as int so callers can pass it to PublishPort.
	l, err := exec.Command("sh", "-c", "echo -n").Output()
	_ = l
	_ = strconv.Itoa
	// Real implementation:
	return 0, errors.New("allocPort: implemented in Task 6")
}
```

(Note: `allocPort` is a stub here; Task 6 implements it for real with `net.Listen`.)

- [ ] **Step 5: Run the test to verify it passes**

```bash
go test ./internal/substrate/applecontainer/... -run TestRunAndExecAlpine -v
```

Expected: PASS. (If `container` isn't installed, test SKIPs — that's acceptable but means Task 1 wasn't completed.)

- [ ] **Step 6: Add a Stop+IP test for the same path**

Append to `adapter_test.go`:

```go
func TestStopIsIdempotent(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx := context.Background()
	// Stopping a nonexistent sandbox must not error.
	if err := a.Stop(ctx, "cspace-nonexistent-"+randSuffix()); err != nil {
		t.Fatalf("Stop on missing should be no-op, got: %v", err)
	}
}

func TestIP(t *testing.T) {
	requireContainerCLI(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "cspace-test-" + randSuffix()
	t.Cleanup(func() { _ = a.Stop(context.Background(), name) })

	if err := a.Run(ctx, RunSpec{
		Name:    name,
		Image:   "docker.io/library/alpine:latest",
		Command: []string{"sleep", "30"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ip, err := a.IP(ctx, name)
	if err != nil {
		t.Fatalf("IP: %v", err)
	}
	if ip == "" || !strings.Contains(ip, ".") {
		t.Fatalf("unexpected IP: %q", ip)
	}
}
```

Plus the import shim:

```go
import (
	"github.com/elliottregan/cspace/internal/substrate"
)

// Type alias for shorter test code:
type RunSpec = substrate.RunSpec
```

- [ ] **Step 7: Run all tests**

```bash
go test ./internal/substrate/applecontainer/... -v
```

Expected: PASS for all three tests.

- [ ] **Step 8: Commit**

```bash
git add internal/substrate/
git commit -m "Add Substrate interface and Apple Container adapter (P0 minimal)"
```

---

## Task 3: Sandbox image — Bun + Claude Code + cspace (`Dockerfile.prototype`)

**Files:**

- Create: `lib/templates/Dockerfile.prototype`
- Modify: `Makefile` (add `prototype-image` and `cspace-linux` targets)

**Goal:** A minimal sandbox OCI image that has Bun, Claude Code, a Linux build of `cspace`, and (later) the Bun supervisor binary baked in.

- [ ] **Step 1: Add Makefile targets**

Append to `Makefile`:

```make
# P0: cross-compile cspace for the Linux microVM.
.PHONY: cspace-linux
cspace-linux:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
		-o bin/cspace-linux-arm64 \
		./cmd/cspace

# P0: build the prototype sandbox image.
.PHONY: prototype-image
prototype-image: cspace-linux
	container build \
		--tag cspace-prototype:latest \
		--file lib/templates/Dockerfile.prototype \
		.
```

- [ ] **Step 2: Write `Dockerfile.prototype`**

Create `lib/templates/Dockerfile.prototype`:

```dockerfile
# Phase 0 prototype sandbox image. Minimal: Bun + Claude Code + cspace + supervisor.
# Production image will replace lib/templates/Dockerfile in P1.
FROM docker.io/library/debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl unzip git tini \
 && rm -rf /var/lib/apt/lists/*

# Bun (single binary install).
RUN curl -fsSL https://bun.sh/install | bash \
 && mv /root/.bun/bin/bun /usr/local/bin/bun \
 && bun --version

# Claude Code CLI. The published npm package ships a Node-side CLI; we run it
# via Bun's Node compatibility layer to keep one runtime.
RUN bun install --global @anthropic-ai/claude-code \
 && ln -s /root/.bun/install/global/node_modules/@anthropic-ai/claude-code/cli.js /usr/local/bin/claude \
 && claude --version

# cspace (linux/arm64 host CLI) for in-sandbox `cspace send`/`cspace exec`.
COPY bin/cspace-linux-arm64 /usr/local/bin/cspace

# Bun supervisor single binary, dropped in by Task 4's build.
COPY lib/agent-supervisor-bun/dist/cspace-supervisor /usr/local/bin/cspace-supervisor

# Default workspace.
RUN mkdir -p /workspace /sessions
WORKDIR /workspace

# tini handles PID 1 + signal forwarding so the supervisor receives SIGTERM
# from `container stop`.
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/cspace-supervisor"]
```

- [ ] **Step 3: Build the cspace Linux binary**

```bash
make cspace-linux
ls -lh bin/cspace-linux-arm64
file bin/cspace-linux-arm64
```

Expected: file exists, `file` reports "ELF 64-bit LSB executable, ARM aarch64".

- [ ] **Step 4: Stub the supervisor binary so the image build doesn't fail**

Until Task 4 produces the real binary, drop a placeholder:

```bash
mkdir -p lib/agent-supervisor-bun/dist
echo '#!/bin/sh
echo "supervisor stub; replaced in Task 4"
sleep infinity' > lib/agent-supervisor-bun/dist/cspace-supervisor
chmod +x lib/agent-supervisor-bun/dist/cspace-supervisor
```

- [ ] **Step 5: Build the image**

```bash
make prototype-image
container images | grep cspace-prototype
```

Expected: image listed, tagged `cspace-prototype:latest`.

- [ ] **Step 6: Smoke test the image**

```bash
container run --rm cspace-prototype:latest bun --version
container run --rm cspace-prototype:latest claude --version
container run --rm cspace-prototype:latest cspace --help
```

Expected: each command prints its version/help. If Claude Code requires interactive auth and refuses to run without it, document that — auth handling is a Task 4 concern.

- [ ] **Step 7: Commit**

```bash
git add Makefile lib/templates/Dockerfile.prototype lib/agent-supervisor-bun/dist/cspace-supervisor
git commit -m "Add prototype sandbox image (Bun + Claude Code + cspace) and Makefile targets"
```

---

## Task 4: Bun TS supervisor — `POST /send` + spawn Claude Code child

**Files:**

- Create: `lib/agent-supervisor-bun/package.json`
- Create: `lib/agent-supervisor-bun/tsconfig.json`
- Create: `lib/agent-supervisor-bun/src/main.ts`
- Create: `lib/agent-supervisor-bun/src/prompt-stream.ts`
- Create: `lib/agent-supervisor-bun/src/prompt-stream.test.ts`
- Create: `lib/agent-supervisor-bun/src/claude-runner.ts`
- Create: `lib/agent-supervisor-bun/build.ts`

**Goal:** Minimum viable supervisor: spawns one Claude Code session, exposes `POST /send` for direct user-turn injection, writes events to `/sessions/primary/events.ndjson`.

- [ ] **Step 1: Initialize the package**

Create `lib/agent-supervisor-bun/package.json`:

```json
{
  "name": "cspace-supervisor",
  "private": true,
  "type": "module",
  "scripts": {
    "test": "bun test",
    "build": "bun build.ts"
  },
  "dependencies": {
    "@anthropic-ai/claude-agent-sdk": "^0.2.100"
  },
  "devDependencies": {
    "@types/bun": "latest",
    "typescript": "^5.6.0"
  }
}
```

Create `lib/agent-supervisor-bun/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "skipLibCheck": true,
    "types": ["bun-types"]
  },
  "include": ["src/**/*", "build.ts"]
}
```

Install:

```bash
cd lib/agent-supervisor-bun && bun install
```

- [ ] **Step 2: Write the failing prompt-stream test**

Create `lib/agent-supervisor-bun/src/prompt-stream.test.ts`:

```ts
import { describe, expect, test } from "bun:test";
import { PromptStream } from "./prompt-stream";

describe("PromptStream", () => {
  test("yields pushed user turns in order", async () => {
    const ps = new PromptStream();
    ps.push("first");
    ps.push("second");
    ps.close();

    const seen: string[] = [];
    for await (const turn of ps) {
      seen.push(turn);
    }
    expect(seen).toEqual(["first", "second"]);
  });

  test("blocks until a turn is pushed", async () => {
    const ps = new PromptStream();
    const it = ps[Symbol.asyncIterator]();
    const pending = it.next();

    setTimeout(() => ps.push("delayed"), 10);
    const result = await pending;

    expect(result.done).toBe(false);
    expect(result.value).toBe("delayed");
    ps.close();
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
cd lib/agent-supervisor-bun && bun test src/prompt-stream.test.ts
```

Expected: error — `Cannot find module "./prompt-stream"`.

- [ ] **Step 4: Implement `PromptStream`**

Create `lib/agent-supervisor-bun/src/prompt-stream.ts`:

```ts
// Async-iterable queue used to feed user turns into the SDK's prompt input.
// `push()` is called by the HTTP handler when a `POST /send` arrives;
// the for-await loop in claude-runner consumes turns as Claude requests them.
export class PromptStream implements AsyncIterable<string> {
  private queue: string[] = [];
  private waiters: Array<(turn: IteratorResult<string>) => void> = [];
  private closed = false;

  push(turn: string): void {
    if (this.closed) throw new Error("PromptStream is closed");
    const w = this.waiters.shift();
    if (w) {
      w({ value: turn, done: false });
    } else {
      this.queue.push(turn);
    }
  }

  close(): void {
    this.closed = true;
    for (const w of this.waiters) w({ value: undefined, done: true });
    this.waiters.length = 0;
  }

  [Symbol.asyncIterator](): AsyncIterator<string> {
    return {
      next: (): Promise<IteratorResult<string>> => {
        const turn = this.queue.shift();
        if (turn !== undefined) {
          return Promise.resolve({ value: turn, done: false });
        }
        if (this.closed) {
          return Promise.resolve({ value: undefined as unknown as string, done: true });
        }
        return new Promise((resolve) => this.waiters.push(resolve));
      },
    };
  }
}
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
bun test src/prompt-stream.test.ts
```

Expected: 2 tests pass.

- [ ] **Step 6: Implement `claude-runner.ts`**

Create `lib/agent-supervisor-bun/src/claude-runner.ts`:

```ts
import { query, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import type { PromptStream } from "./prompt-stream";

export type EventSink = (event: SDKMessage) => void;

// Spawns a Claude Code session driven by `prompts`. Streams every SDK event
// through `onEvent`. Returns a promise that resolves when the session ends.
export async function runClaude(
  prompts: PromptStream,
  cwd: string,
  onEvent: EventSink
): Promise<void> {
  const stream = query({
    prompt: prompts,
    options: {
      cwd,
      // P0 doesn't restrict tools or set system prompts;
      // those land in P1 once the agent playbooks are wired in.
    },
  });

  for await (const event of stream) {
    onEvent(event);
  }
}
```

- [ ] **Step 7: Implement `main.ts` (HTTP server + glue)**

Create `lib/agent-supervisor-bun/src/main.ts`:

```ts
import { mkdirSync, appendFileSync } from "node:fs";
import { join } from "node:path";
import { PromptStream } from "./prompt-stream";
import { runClaude } from "./claude-runner";

const SESSION_ID = "primary";
const SESSIONS_DIR = "/sessions";
const WORKSPACE = "/workspace";
const CONTROL_PORT = Number(process.env.CSPACE_CONTROL_PORT ?? 6201);
const CONTROL_TOKEN = process.env.CSPACE_CONTROL_TOKEN ?? "";

const sessionDir = join(SESSIONS_DIR, SESSION_ID);
mkdirSync(sessionDir, { recursive: true });
const eventLog = join(sessionDir, "events.ndjson");

const prompts = new PromptStream();

function logEvent(kind: string, data: unknown): void {
  const line = JSON.stringify({
    ts: new Date().toISOString(),
    session: SESSION_ID,
    kind,
    data,
  });
  appendFileSync(eventLog, line + "\n");
}

// Start Claude as a background task; do NOT await — it runs for the supervisor's lifetime.
runClaude(prompts, WORKSPACE, (event) => {
  logEvent("sdk-event", event);
}).catch((err: unknown) => {
  logEvent("sdk-error", { error: String(err) });
});

const server = Bun.serve({
  port: CONTROL_PORT,
  async fetch(req) {
    const url = new URL(req.url);

    // Auth: every request must carry the control token if one is configured.
    if (CONTROL_TOKEN) {
      const auth = req.headers.get("authorization") ?? "";
      if (auth !== `Bearer ${CONTROL_TOKEN}`) {
        return new Response("unauthorized", { status: 401 });
      }
    }

    if (req.method === "POST" && url.pathname === "/send") {
      const body = (await req.json()) as { session?: string; text?: string };
      if (typeof body.text !== "string" || body.text.length === 0) {
        return Response.json({ error: "text required" }, { status: 400 });
      }
      if (body.session && body.session !== SESSION_ID) {
        return Response.json({ error: `unknown session ${body.session}` }, { status: 404 });
      }
      prompts.push(body.text);
      logEvent("user-turn", { source: "control-port", text: body.text });
      return Response.json({ ok: true });
    }

    if (req.method === "GET" && url.pathname === "/health") {
      return Response.json({ ok: true, session: SESSION_ID });
    }

    return new Response("not found", { status: 404 });
  },
});

logEvent("supervisor-start", { port: server.port, session: SESSION_ID });
console.log(`cspace-supervisor: listening on ${server.port}, session=${SESSION_ID}`);
```

- [ ] **Step 8: Implement the build script**

Create `lib/agent-supervisor-bun/build.ts`:

```ts
// Compiles the supervisor to a single-file Linux ARM64 binary
// for embedding in the prototype sandbox image.
import { $ } from "bun";

await Bun.build({
  entrypoints: ["./src/main.ts"],
  outdir: "./dist",
  target: "bun",
  // Bun's --compile target produces a single executable.
  // We use the CLI flag form via $`bun build --compile` because
  // Bun.build doesn't yet expose --compile programmatically (1.1.x).
});

// Run the actual --compile via the CLI for the real binary.
await $`bun build src/main.ts --compile --target=bun-linux-arm64 --outfile=dist/cspace-supervisor`;

console.log("Built dist/cspace-supervisor");
```

- [ ] **Step 9: Build and smoke-test the supervisor binary**

```bash
cd lib/agent-supervisor-bun
bun run build
file dist/cspace-supervisor
```

Expected: file exists, `file` reports a Linux ARM64 ELF executable.

- [ ] **Step 10: Rebuild the sandbox image with the real supervisor binary**

```bash
cd ../..
make prototype-image
```

- [ ] **Step 11: Manual end-to-end smoke test (no Claude yet — just /health)**

```bash
container run -d --name p4-test -p 16201:6201 cspace-prototype:latest \
  /usr/local/bin/cspace-supervisor
sleep 2
curl http://127.0.0.1:16201/health
container logs p4-test
container stop p4-test && container rm p4-test
```

Expected: `/health` returns `{"ok":true,"session":"primary"}`. Logs show "cspace-supervisor: listening on 6201".

- [ ] **Step 12: Manual end-to-end test with Claude (interactive auth required)**

If Claude Code needs auth, run `container exec p4-test claude /login` first, follow the prompts, then:

```bash
container run -d --name p4-claude \
  -p 16201:6201 \
  -v "$HOME/.claude:/root/.claude" \
  cspace-prototype:latest

sleep 3
curl -X POST http://127.0.0.1:16201/send \
  -H 'Content-Type: application/json' \
  -d '{"text":"Reply with exactly the word PONG and nothing else"}'

sleep 10
container exec p4-claude tail -n 20 /sessions/primary/events.ndjson
container stop p4-claude && container rm p4-claude
```

Expected: events.ndjson contains `user-turn`, `sdk-event` lines including an assistant message containing "PONG".

If interactive auth is impossible inside the sandbox, document the workaround (mount `~/.claude` host directory; `/login` on host; run agent inside) in the spike report.

- [ ] **Step 13: Commit**

```bash
git add lib/agent-supervisor-bun/
git commit -m "Add Bun TS supervisor: POST /send injects user turns into Claude session"
```

---

## Task 5: Sandbox registry (host-side `~/.cspace/sandbox-registry.json`)

**Files:**

- Create: `internal/registry/registry.go`
- Create: `internal/registry/registry_test.go`

**Goal:** Read/write a JSON file mapping `<project>:<sandbox>` → `{control_url, control_token, ip, started_at}`. Race-safe via `O_EXCL` rename, like the context server's pattern.

- [ ] **Step 1: Write the failing test**

Create `internal/registry/registry_test.go`:

```go
package registry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterAndLookup(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	entry := Entry{
		Project:    "myproj",
		Name:       "test1",
		ControlURL: "http://127.0.0.1:16201",
		Token:      "tok123",
		IP:         "192.168.64.5",
		StartedAt:  time.Now().UTC(),
	}
	if err := r.Register(entry); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Lookup("myproj", "test1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.ControlURL != entry.ControlURL || got.Token != entry.Token {
		t.Fatalf("got %+v, want %+v", got, entry)
	}
}

func TestLookupMissing(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	if _, err := r.Lookup("none", "none"); err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestUnregister(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	_ = r.Register(Entry{Project: "p", Name: "n", ControlURL: "http://x", StartedAt: time.Now()})
	if err := r.Unregister("p", "n"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := r.Lookup("p", "n"); err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	r := &Registry{Path: filepath.Join(dir, "sandbox-registry.json")}

	_ = r.Register(Entry{Project: "p", Name: "a", ControlURL: "http://a", StartedAt: time.Now()})
	_ = r.Register(Entry{Project: "p", Name: "b", ControlURL: "http://b", StartedAt: time.Now()})

	entries, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/registry/... -v
```

Expected: compile error referencing undefined `Registry`, `Entry`, `Register`, `Lookup`, `Unregister`, `List`.

- [ ] **Step 3: Implement the registry**

Create `internal/registry/registry.go`:

```go
// Package registry tracks active cspace sandboxes and how to reach them.
// File format: ~/.cspace/sandbox-registry.json
//
//	{
//	  "myproj:coordinator": {"control_url":"...","token":"...","ip":"...","started_at":"..."},
//	  ...
//	}
//
// Concurrency: writes go through a tmp-file + atomic rename. The file is
// re-read on every operation; we never hold an in-memory snapshot across calls.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is a registered sandbox.
type Entry struct {
	Project    string    `json:"-"`
	Name       string    `json:"-"`
	ControlURL string    `json:"control_url"`
	Token      string    `json:"token,omitempty"`
	IP         string    `json:"ip,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// Registry is a JSON-backed sandbox registry.
type Registry struct {
	Path string // absolute path to the JSON file
}

// DefaultPath returns the standard registry location: ~/.cspace/sandbox-registry.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cspace", "sandbox-registry.json"), nil
}

func (r *Registry) load() (map[string]Entry, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return nil, err
	}
	m := map[string]Entry{}
	if len(data) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return m, nil
}

func (r *Registry) save(m map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}

func key(project, name string) string { return project + ":" + name }

// Register adds or replaces an entry.
func (r *Registry) Register(e Entry) error {
	m, err := r.load()
	if err != nil {
		return err
	}
	m[key(e.Project, e.Name)] = e
	return r.save(m)
}

// Unregister removes an entry. No error if absent.
func (r *Registry) Unregister(project, name string) error {
	m, err := r.load()
	if err != nil {
		return err
	}
	delete(m, key(project, name))
	return r.save(m)
}

// Lookup returns the entry for a sandbox.
func (r *Registry) Lookup(project, name string) (Entry, error) {
	m, err := r.load()
	if err != nil {
		return Entry{}, err
	}
	e, ok := m[key(project, name)]
	if !ok {
		return Entry{}, fmt.Errorf("sandbox %s:%s not registered", project, name)
	}
	e.Project, e.Name = project, name
	return e, nil
}

// List returns all entries.
func (r *Registry) List() ([]Entry, error) {
	m, err := r.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for k, v := range m {
		// k is "project:name"; split.
		var project, name string
		for i := 0; i < len(k); i++ {
			if k[i] == ':' {
				project, name = k[:i], k[i+1:]
				break
			}
		}
		v.Project, v.Name = project, name
		out = append(out, v)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/registry/... -v
```

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/
git commit -m "Add sandbox registry: JSON-backed name → control URL lookup"
```

---

## Task 6: `cspace prototype-up` and `cspace prototype-down` commands

**Files:**

- Create: `internal/cli/cmd_prototype_up.go`
- Create: `internal/cli/cmd_prototype_down.go`
- Modify: `internal/cli/root.go`

**Goal:** `cspace prototype-up <name>` builds the prototype image (if missing), runs a sandbox, picks a free host port, registers it. `cspace prototype-down <name>` reverses it.

- [ ] **Step 1: Add a free-port helper**

Append to `internal/registry/registry.go` (or create `internal/registry/port.go`):

```go
import "net"

// FreePort asks the kernel for an unused TCP port on 127.0.0.1.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
```

- [ ] **Step 2: Implement `cmd_prototype_up.go`**

Create `internal/cli/cmd_prototype_up.go`:

```go
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

func newPrototypeUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-up <name>",
		Short: "P0: launch a prototype sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			a := applecontainer.New()
			if !a.Available() {
				return fmt.Errorf("apple `container` CLI not on PATH; install per Task 1")
			}

			hostPort, err := registry.FreePort()
			if err != nil {
				return err
			}
			token := randHex(16)

			spec := substrate.RunSpec{
				Name:  fmt.Sprintf("cspace-%s-%s", project, name),
				Image: "cspace-prototype:latest",
				Env: map[string]string{
					"CSPACE_CONTROL_PORT":  "6201",
					"CSPACE_CONTROL_TOKEN": token,
					"CSPACE_PROJECT":       project,
					"CSPACE_SANDBOX_NAME":  name,
				},
				PublishPort: []substrate.PortMap{{HostPort: hostPort, ContainerPort: 6201}},
			}
			if err := a.Run(ctx, spec); err != nil {
				return fmt.Errorf("substrate run: %w", err)
			}

			ip, _ := a.IP(ctx, spec.Name)

			path, err := registry.DefaultPath()
			if err != nil {
				return err
			}
			r := &registry.Registry{Path: path}
			if err := r.Register(registry.Entry{
				Project:    project,
				Name:       name,
				ControlURL: fmt.Sprintf("http://127.0.0.1:%d", hostPort),
				Token:      token,
				IP:         ip,
				StartedAt:  time.Now().UTC(),
			}); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"sandbox %s up: control http://127.0.0.1:%d  ip %s  token %s\n",
				name, hostPort, ip, token[:8]+"…")
			return nil
		},
	}
}

func projectName() string {
	if cfg != nil && cfg.ProjectName != "" {
		return cfg.ProjectName
	}
	return "default"
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

(`cfg.ProjectName` may not exist with that exact field name — adjust to whatever the config exposes; `projectName()` is the single point to fix.)

- [ ] **Step 3: Implement `cmd_prototype_down.go`**

Create `internal/cli/cmd_prototype_down.go`:

```go
package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/spf13/cobra"
)

func newPrototypeDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-down <name>",
		Short: "P0: stop and remove a prototype sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			project := projectName()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			a := applecontainer.New()
			_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

			path, _ := registry.DefaultPath()
			r := &registry.Registry{Path: path}
			_ = r.Unregister(project, name)

			fmt.Fprintf(cmd.OutOrStdout(), "sandbox %s down\n", name)
			return nil
		},
	}
}
```

- [ ] **Step 4: Register both commands in `root.go`**

Find the `AddCommand(...)` block in `internal/cli/root.go` and append:

```go
root.AddCommand(newPrototypeUpCmd())
root.AddCommand(newPrototypeDownCmd())
```

- [ ] **Step 5: Build and run end-to-end**

```bash
make build
./bin/cspace prototype-up p1
./bin/cspace prototype-down p1
```

Expected: prototype-up prints control URL + IP + truncated token; prototype-down prints "sandbox p1 down". `~/.cspace/sandbox-registry.json` shows the entry between calls.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cmd_prototype_up.go internal/cli/cmd_prototype_down.go internal/cli/root.go internal/registry/
git commit -m "Add cspace prototype-up/-down: build, register, tear down sandboxes"
```

---

## Task 7: `cspace prototype-send` command

**Files:**

- Create: `internal/cli/cmd_prototype_send.go`
- Modify: `internal/cli/root.go`

**Goal:** `cspace prototype-send <name> <text>` resolves the sandbox in the registry and POSTs to its control port. Verifies dealbreaker #3 (direct user-turn messaging from host).

- [ ] **Step 1: Implement the command**

Create `internal/cli/cmd_prototype_send.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/spf13/cobra"
)

func newPrototypeSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prototype-send <name>[:<session>] <text>",
		Short: "P0: inject a user turn into a sandbox session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, text := args[0], args[1]

			name, session := target, "primary"
			if i := strings.Index(target, ":"); i >= 0 {
				name, session = target[:i], target[i+1:]
			}

			project := projectName()
			path, err := registry.DefaultPath()
			if err != nil {
				return err
			}
			entry, err := (&registry.Registry{Path: path}).Lookup(project, name)
			if err != nil {
				return err
			}

			body, _ := json.Marshal(map[string]string{
				"session": session,
				"text":    text,
			})
			req, _ := http.NewRequestWithContext(cmd.Context(),
				"POST", entry.ControlURL+"/send", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if entry.Token != "" {
				req.Header.Set("Authorization", "Bearer "+entry.Token)
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("post /send: %w", err)
			}
			defer resp.Body.Close()
			out, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				return fmt.Errorf("status %d: %s", resp.StatusCode, out)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}
```

- [ ] **Step 2: Register in root.go**

```go
root.AddCommand(newPrototypeSendCmd())
```

- [ ] **Step 3: End-to-end smoke test**

```bash
make build && make prototype-image
./bin/cspace prototype-up p1
sleep 3
./bin/cspace prototype-send p1 "Reply with exactly the word PONG"
sleep 10
container exec cspace-default-p1 tail -n 20 /sessions/primary/events.ndjson
./bin/cspace prototype-down p1
```

Expected: events.ndjson contains a user-turn line and an assistant SDK event mentioning "PONG".

- [ ] **Step 4: Commit**

```bash
git add internal/cli/cmd_prototype_send.go internal/cli/root.go
git commit -m "Add cspace prototype-send: HTTP POST to sandbox control port"
```

---

## Task 8: Host registry-daemon (so in-sandbox `cspace` can resolve siblings)

**Files:**

- Create: `cmd/cspace-registry-daemon/main.go`
- Modify: `internal/cli/cmd_prototype_up.go` — auto-spawn the daemon, inject `CSPACE_REGISTRY_URL` env into the sandbox.

**Goal:** A small HTTP daemon on the host exposing `GET /lookup/:project/:name` so the in-sandbox `cspace` binary can resolve sibling sandboxes without parsing `~/.cspace/sandbox-registry.json` directly.

- [ ] **Step 1: Implement the daemon**

Create `cmd/cspace-registry-daemon/main.go`:

```go
// cspace-registry-daemon: HTTP endpoint over the sandbox registry, intended
// to be reached from inside sandboxes via the host gateway IP.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/elliottregan/cspace/internal/registry"
)

func main() {
	port := os.Getenv("CSPACE_REGISTRY_DAEMON_PORT")
	if port == "" {
		port = "6280"
	}
	path, err := registry.DefaultPath()
	if err != nil {
		log.Fatalf("registry path: %v", err)
	}
	r := &registry.Registry{Path: path}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /lookup/", func(w http.ResponseWriter, req *http.Request) {
		// /lookup/<project>/<name>
		rest := strings.TrimPrefix(req.URL.Path, "/lookup/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			http.Error(w, "expected /lookup/<project>/<name>", 400)
			return
		}
		entry, err := r.Lookup(parts[0], parts[1])
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		_ = json.NewEncoder(w).Encode(entry)
	})
	mux.HandleFunc("GET /list", func(w http.ResponseWriter, req *http.Request) {
		entries, err := r.List()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(entries)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"ok":true}`)
	})

	addr := "127.0.0.1:" + port
	log.Printf("cspace-registry-daemon: listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 2: Add a Makefile target**

Append to `Makefile`:

```make
.PHONY: registry-daemon
registry-daemon:
	go build -o bin/cspace-registry-daemon ./cmd/cspace-registry-daemon
```

- [ ] **Step 3: Smoke test the daemon manually**

```bash
make registry-daemon
./bin/cspace prototype-up p1
./bin/cspace-registry-daemon &  # foreground: switch terminals or background it
sleep 1
curl http://127.0.0.1:6280/health
curl http://127.0.0.1:6280/list
curl http://127.0.0.1:6280/lookup/default/p1
kill %1
./bin/cspace prototype-down p1
```

Expected: `/health` ok, `/list` returns array including p1, `/lookup/default/p1` returns the entry.

- [ ] **Step 4: Auto-spawn the daemon from `prototype-up` and inject env**

Edit `internal/cli/cmd_prototype_up.go`. Inside the `RunE` body, after `applecontainer.New()` and before building `spec`:

```go
// Ensure the registry daemon is running on the host gateway.
if err := ensureRegistryDaemon(); err != nil {
	return fmt.Errorf("registry daemon: %w", err)
}
```

In the `Env` map, add:

```go
"CSPACE_REGISTRY_URL": "http://192.168.64.1:6280", // gateway IP from Task 1 spike
"CSPACE_PROJECT":      project,
```

(Use whatever gateway IP the spike documented; if Apple Container exposes a stable host alias like `host.containers.internal`, prefer that.)

Add a helper to the same file:

```go
import (
	"net"
	"os/exec"
)

func ensureRegistryDaemon() error {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6280", time.Second)
	if err == nil {
		conn.Close()
		return nil
	}
	bin, err := exec.LookPath("cspace-registry-daemon")
	if err != nil {
		// Fall back to the local build output.
		bin = "./bin/cspace-registry-daemon"
	}
	c := exec.Command(bin)
	c.Stdout, c.Stderr = nil, nil
	if err := c.Start(); err != nil {
		return err
	}
	// Give it a moment to bind.
	time.Sleep(250 * time.Millisecond)
	return nil
}
```

- [ ] **Step 5: Verify auto-spawn**

```bash
pkill cspace-registry-daemon || true
make build registry-daemon
./bin/cspace prototype-up p1
pgrep cspace-registry-daemon
curl http://127.0.0.1:6280/lookup/default/p1
./bin/cspace prototype-down p1
```

Expected: registry-daemon process exists after prototype-up, lookup succeeds.

- [ ] **Step 6: Commit**

```bash
git add cmd/cspace-registry-daemon/ internal/cli/cmd_prototype_up.go Makefile
git commit -m "Add cspace-registry-daemon: HTTP lookup for in-sandbox cspace"
```

---

## Task 9: Cross-sandbox messaging from inside a sandbox

**Files:**

- Modify: `internal/cli/cmd_prototype_send.go` — fall back to `CSPACE_REGISTRY_URL` when the registry file isn't on disk.

**Goal:** Verify dealbreaker #4 — a sandbox can `cspace prototype-send` to a sibling sandbox.

- [ ] **Step 1: Make `prototype-send` registry-daemon-aware**

Replace the registry lookup section in `internal/cli/cmd_prototype_send.go` with:

```go
project := projectName()

var entry registry.Entry
if rURL := os.Getenv("CSPACE_REGISTRY_URL"); rURL != "" {
	// In-sandbox path: ask the host registry-daemon over HTTP.
	resp, err := http.Get(fmt.Sprintf("%s/lookup/%s/%s", rURL, project, name))
	if err != nil {
		return fmt.Errorf("registry daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry lookup status %d: %s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return err
	}
} else {
	// Host path: read the registry file directly.
	path, err := registry.DefaultPath()
	if err != nil {
		return err
	}
	entry, err = (&registry.Registry{Path: path}).Lookup(project, name)
	if err != nil {
		return err
	}
}
```

(Add `"os"` to imports.)

- [ ] **Step 2: Adjust ControlURL for in-sandbox use**

When called from inside a sandbox, `entry.ControlURL` will be `http://127.0.0.1:<host-port>` — that loops back to the calling sandbox, not the host. Two options depending on Task 1's networking findings:

- **If host-routable IPs work:** rewrite the URL to `http://<gateway-ip>:<host-port>`, since the host published the port on its loopback.
- **If containers can't reach each other but can reach the host gateway:** same — the published port lives on the host gateway from the sandbox's POV.

Add a helper in `cmd_prototype_send.go`:

```go
func rewriteForGateway(ctlURL string) string {
	if os.Getenv("CSPACE_REGISTRY_URL") == "" {
		return ctlURL // host call, leave alone
	}
	gateway := os.Getenv("CSPACE_HOST_GATEWAY")
	if gateway == "" {
		gateway = "192.168.64.1" // documented in Task 1 spike
	}
	return strings.Replace(ctlURL, "http://127.0.0.1:", "http://"+gateway+":", 1)
}
```

And use it: `entry.ControlURL = rewriteForGateway(entry.ControlURL)` before building `req`.

- [ ] **Step 3: Inject `CSPACE_HOST_GATEWAY` from prototype-up**

Edit `cmd_prototype_up.go` `Env` map and add:

```go
"CSPACE_HOST_GATEWAY": "192.168.64.1", // adjust if Task 1 spike found different
```

- [ ] **Step 4: End-to-end cross-sandbox test**

```bash
./bin/cspace prototype-up A
./bin/cspace prototype-up B
sleep 5
container exec cspace-default-A cspace prototype-send B "Reply with the word PONG"
sleep 10
container exec cspace-default-B tail -n 20 /sessions/primary/events.ndjson
./bin/cspace prototype-down A
./bin/cspace prototype-down B
```

Expected: B's events.ndjson contains a user-turn from A and a Claude reply containing PONG.

If this fails (e.g. networking model is different than Task 1 documented), capture the failure mode in the prototype report and adjust the messaging strategy.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_prototype_send.go internal/cli/cmd_prototype_up.go
git commit -m "Cross-sandbox prototype-send via registry-daemon + host gateway"
```

---

## Task 10: Linux containerd feasibility spike (read-only)

**Files:**

- Create: `docs/superpowers/spikes/2026-04-30-containerd-spike.md`

**Goal:** Confirm that the same OCI image can run on Linux/containerd without modification, and that the Substrate interface is implementable. No production code in this task.

- [ ] **Step 1: Identify a Linux test surface**

Options (pick whatever's accessible):
- A Linux VM (Lima, OrbStack Linux machine, an actual Linux box).
- A GitHub Actions Ubuntu runner spike.

If no Linux is reachable in your dev loop, document that in the spike and proceed — Phase 5 will revisit with hardware.

- [ ] **Step 2: Run the prototype image under containerd**

On the Linux host:

```bash
# Install containerd + nerdctl (nerdctl is a containerd-native CLI close in shape to docker).
sudo apt-get install -y containerd
# nerdctl from https://github.com/containerd/nerdctl/releases (download tarball)

# Pull or sideload the cspace-prototype image. Easiest: rebuild on the Linux host.
nerdctl build -t cspace-prototype:latest -f lib/templates/Dockerfile.prototype .
nerdctl run --rm cspace-prototype:latest bun --version
nerdctl run --rm cspace-prototype:latest cspace --help
```

Record findings:
- Does the build complete?
- Does the supervisor start under containerd?
- Networking model — IPs reachable host ↔ container, container ↔ container?
- Cost of writing a containerd adapter (CLI shell-out vs gRPC client)?

- [ ] **Step 3: Write the spike report**

```markdown
# containerd / Linux Spike — 2026-04-30

## Environment
- Host: <distro / version>
- containerd: <version>
- nerdctl: <version>

## Build + run results
<fill in>

## Networking
<fill in>

## Substrate adapter feasibility
- Adapter surface needed (Run/Exec/Stop/IP).
- Recommended approach: nerdctl shell-out vs containerd Go gRPC client.
- Estimate to implement: <Xd>.

## Recommendation
<one paragraph: ship in P5 as planned / unknown blockers / etc.>
```

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/spikes/2026-04-30-containerd-spike.md
git commit -m "Spike Linux containerd feasibility for cspace P5"
```

---

## Task 11: Phase 0 prototype report

**Files:**

- Create: `docs/superpowers/reports/2026-05-XX-phase-0-prototype-report.md` (replace `XX` with the actual completion date).

**Goal:** Write the verdict on each of the five dealbreakers, with evidence.

- [ ] **Step 1: Re-run the full end-to-end as a "demo run" and capture output**

```bash
make build && make prototype-image && make registry-daemon
./bin/cspace prototype-up A 2>&1 | tee /tmp/p0-up-A.log
./bin/cspace prototype-up B 2>&1 | tee /tmp/p0-up-B.log
sleep 5
./bin/cspace prototype-send A "Say PONG" 2>&1 | tee /tmp/p0-host-to-A.log
sleep 10
container exec cspace-default-A cspace prototype-send B "Say PING" 2>&1 | tee /tmp/p0-A-to-B.log
sleep 10
container exec cspace-default-A tail -n 30 /sessions/primary/events.ndjson > /tmp/p0-A-events.ndjson
container exec cspace-default-B tail -n 30 /sessions/primary/events.ndjson > /tmp/p0-B-events.ndjson
./bin/cspace prototype-down A
./bin/cspace prototype-down B
```

- [ ] **Step 2: Write the report**

Create `docs/superpowers/reports/2026-05-XX-phase-0-prototype-report.md` with these sections:

```markdown
# Phase 0 Prototype Report

**Date:** <fill>
**Spec:** docs/superpowers/specs/2026-04-30-sandbox-architecture-design.md
**Plan:** docs/superpowers/plans/2026-04-30-phase-0-prototype.md

## Dealbreakers

### 1. Boot a sandbox on Apple Container — <PASS / FAIL>
Evidence: <link to spike report; cold/warm boot times>

### 2. Install + run Claude Code inside — <PASS / FAIL>
Evidence: events.ndjson excerpts; auth flow notes.

### 3. Direct user-turn messaging from host to sandbox — <PASS / FAIL>
Evidence: /tmp/p0-host-to-A.log + /tmp/p0-A-events.ndjson.

### 4. Cross-sandbox messaging — <PASS / FAIL>
Evidence: /tmp/p0-A-to-B.log + /tmp/p0-B-events.ndjson.

### 5. Apple Container networking model — <DOCUMENTED>
Decision: <hostfwd registry / direct IP / hybrid>. Reason: <evidence from spike>.

## Linux containerd feasibility — <NO BLOCKERS / RISKS LISTED>
Summary from spike: <one paragraph>

## P1 risks surfaced
- <bullet list of issues encountered, e.g. Bun ARM64 incompat, Claude auth UX, exec stream ordering>

## Recommendation
<one paragraph: proceed to P1 / revisit specific spec sections / blocked by X>
```

- [ ] **Step 3: Commit and close out**

```bash
git add docs/superpowers/reports/
git commit -m "P0 prototype report: dealbreakers verdicts and P1 risks"
```

---

## Self-review notes

- Every spec dealbreaker (boot Apple Container, run Claude inside, host→sandbox messaging, sandbox→sandbox messaging, networking model) has a corresponding task or task-step.
- Spec deliverable `internal/substrate/applecontainer/` → Task 2.
- Spec deliverable Bun supervisor → Task 4.
- Spec deliverable `cspace prototype-up`/`prototype-send` → Tasks 6, 7.
- Spec deliverable Linux build of cspace embedded in image → Task 3.
- Spec deliverable written prototype report → Task 11.
- Linux containerd spike (spec called for "feasibility check") → Task 10.
- Networking model verification (spec open question #1) → Task 1 + Task 9 step 4.
- The plan does not implement: activity hub, context daemon, multi-session, postgres, Playwright, dev-server publishing, friendly URLs, advisors, coordinator, ssh/exec/cp endpoints, firewall, teleport. All explicitly out of P0.
- Type / signature consistency: `Substrate.Run/Exec/Stop/IP` defined in Task 2; same names used in Tasks 6, 7. `Registry.Register/Lookup/Unregister/List/FreePort` defined in Tasks 5–6; same names used in Tasks 6–9. `PromptStream.push/close` defined in Task 4 step 4; consistent across `main.ts` (step 7).
