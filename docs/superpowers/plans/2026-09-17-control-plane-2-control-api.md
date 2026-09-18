# Control API (`internal/control`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract cspace's control queries and actions out of the old dashboard into the terminal-free `internal/control` package the sandbox-side plan started, and repoint the old dashboard at it so `cspace tui` keeps working exactly as it does today.

**Architecture:** `internal/control` is plain Go over the registry, the Apple Container adapter, the supervisor's HTTP control port, and the host session/clone directories. Dependency direction is strictly one-way: `internal/cli` and `internal/tui` may import `internal/control`; `internal/control` imports neither. Host work whose implementation still lives in `internal/cli` (sandbox teardown, the browser restart ladder) arrives through a `Host` interface that `internal/cli` implements and injects — the same consumer-defined-interface pattern `internal/tui`'s `Actor` already uses. `internal/tui` keeps its Bubble Tea v1 model/view untouched by re-exporting control's domain types as Go type aliases.

**Tech Stack:** Go 1.26, stdlib `net/http` / `encoding/json` / `os/exec`, `github.com/elliottregan/cspace/internal/{registry,substrate,substrate/applecontainer,devcontainer}`, Cobra (unchanged), Bubble Tea v1 (unchanged).

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — this plan implements **rollout step 2, "Control API"** only.

## Global Constraints

- **Scope is rollout step 2 only.** No bubbletea v2, no `internal/pane`, no `internal/controlplane`, no deletion of `internal/tui` or `internal/cli/tui_actor.go`. Those are steps 3 and 4.
- **`cspace tui` behavior must be unchanged.** Every existing `internal/tui` test keeps passing (repointed where the code moved). Do not "fix" anything the move surfaces.
- **Three open findings describe deliberate current behavior. Preserve it, do not fix it:**
  - `.cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md` — `down` reports any `warning:` text as a failed action. Keep that rule.
  - `.cspace/context/findings/2026-07-20-tui-browser-row-orphaned-when-project-has-no-registry-entry.md` — `Correlate` derives projects from registry entries only. Keep that.
  - `.cspace/context/findings/2026-07-20-tui-row-list-has-no-viewport-scrolling.md` — view concern, untouched here.
- **Dependency direction:** `internal/control` must not import `internal/cli` or `internal/tui`. Verify with `go list -deps` (Task 1 step 8).
- **Always build through `make`.** `internal/assets/embedded/` is gitignored and populated by `make sync-embedded`; `make vet`, `make lint` and `make test` all run it first. A bare `go build` on a clean checkout embeds an empty asset tree.
- **`make check` must be green after every task**: `make vet`, `make lint`, `make test` (plus `make fmt-check` and `make test-scripts`, which this plan never touches).
- **A sibling plan (rollout step 1) owns the attach surface.** Assume `internal/control` already contains `control.go` (`SessionClaude`, `SessionShell`, `TmuxConf`, `Workspace`, `ControlPlaneDir`), `argv.go` (`AttachSpec` — a struct carrying `Container`, `Session`, `Command`, `TERM`, `COLORTERM` — plus `ClaudeAttach`, `func AttachArgv(spec AttachSpec) (bin string, argv []string, err error)`, `TerminalEnv`, `TerminalEnvArgs`, `ApplyTerminalEnv`), `tmux.go` (`Execer`, `CLIExecer`, `Tmux`, `NewTmux`, `Present`, `ListClients`, `DetachClient`) and `attach.go` (`ClientRecord`, `BeginAttach`, `Attachment`). **Do not define, move, or modify any of them** — consume them. That plan lands first, so every task here adds files to an existing package rather than creating one.
- **Module path:** `github.com/elliottregan/cspace`. Commit messages: short imperative sentences, e.g. `Move the dashboard poller into internal/control`.
- **Supervisor `GET /status` JSON** (`lib/agent-supervisor-bun/src/main.ts:162`): `{ok, session, state, lastEventTs, lastEventType, lastEventSubtype, queueDepth}`. `state` is `"working" | "idle"`.
- **Default supervisor session id is `"primary"`** (`SESSION_ID` in `main.ts`; also the `/sessions/primary/` path segment). It is not configurable.

---

## File Structure

Package `internal/control` (all files `package control`). The sibling step-1 plan already created it and owns `control.go`, `argv.go`, `tmux.go`, `attach.go` and their tests; the files below are this plan's additions to it:

| File | Responsibility | Task |
|---|---|---|
| `client.go` | `Client`, `Options`, `New`, the `ContainerCLI` / `EntryStore` seams, HTTP clients, timeouts, plus `containerExecer` (the `ContainerCLI`→`Execer` adapter) and `Options.Tmux` | 1 |
| `types.go` | Domain types moved from `internal/tui/types.go`: `Row`, `RowKind`, `RowState`, `AgentStatus`, `DaemonHealth`, `BrowserHealth`, `Snapshot`, container-name helpers | 1 |
| `correlate.go` | `Correlate` — the pure grouping/nesting fold, moved verbatim | 1 |
| `snapshot.go` | `Snapshotter`, `Client.Snapshot` and its probe fan-out, moved from `internal/tui/poll.go` | 1 |
| `agent.go` | `Client.AgentStatus`, `Client.Send`, `Client.Interrupt`, `ErrorText` | 2 |
| `host.go` | The `Host` interface and `ErrNoHost` | 3 |
| `actions.go` | `Client.Down`, `Client.RestartBrowser` | 3 |
| `paths.go` | Host path layout: `SessionDir`, `SessionEventsPath`, `AgentStatePath`, `CloneDir` | 4, 5 |
| `events.go` | `EventLine`, `TailEvents`, `Client.Events`, moved from `internal/tui/events.go` | 4 |
| `interactive.go` | `InteractiveState`, `ReadInteractiveState`, `Client.InteractiveState` | 4 |
| `ports.go` | `Port`, `Client.Ports` and the statusline's label/curation/URL rules ported to Go | 5 |
| `tmux.go` (sibling plan's file) | Append only: `Client.ListClients` / `Client.DetachClient` delegating to the `Tmux` driver | 6 |
| `up.go` | `Client.Up` | 6 |

Modified:

| File | Change | Task |
|---|---|---|
| `internal/tui/types.go` | Becomes a type-alias re-export of `internal/control` | 1 |
| `internal/tui/poll.go`, `correlate.go`, `poll_test.go`, `correlate_test.go` | Deleted (moved to `internal/control`) | 1 |
| `internal/tui/events.go`, `events_test.go` | Deleted (moved to `internal/control`) | 4 |
| `internal/tui/model.go` | `Poll(ctx)` → `Snapshot(ctx)`; event reads go through `control` | 1, 4 |
| `internal/tui/model_test.go` | `fakePoller` method renamed | 1 |
| `internal/cli/cmd_tui.go` | Builds one `*control.Client` and passes it to both the model and the actor | 1, 3 |
| `internal/cli/tui_actor.go` | Every action but `Attach` delegates to `*control.Client` | 2, 3 |
| `internal/cli/tui_actor_test.go` | Repointed onto the control-backed actor | 2, 3 |
| `internal/cli/control_host.go` | **New.** `cliHost` implements `control.Host` over `teardownSandbox` and `restartBrowserFn` | 3 |
| `internal/cli/cmd_agent.go` | `agentErrorText` delegates to `control.ErrorText` | 2 |
| `internal/cli/cmd_dns.go` | `dnsResolverFile` / `dnsDomain` alias the control constants | 5 |

---

### Task 1: Package skeleton, domain types, correlation fold, and `Snapshot`

Moves the dashboard's data layer — types, the pure correlation fold, and the poller — into `internal/control`, and repoints `internal/tui` at it through type aliases so the Bubble Tea model and view are untouched.

**Files:**
- Create: `internal/control/client.go`
- Create: `internal/control/types.go` (from `internal/tui/types.go`)
- Create: `internal/control/correlate.go` (from `internal/tui/correlate.go`)
- Create: `internal/control/snapshot.go` (from `internal/tui/poll.go`)
- Test: `internal/control/correlate_test.go` (from `internal/tui/correlate_test.go`)
- Test: `internal/control/snapshot_test.go` (from `internal/tui/poll_test.go`)
- Delete: `internal/tui/poll.go`, `internal/tui/correlate.go`, `internal/tui/poll_test.go`, `internal/tui/correlate_test.go`
- Rewrite: `internal/tui/types.go`
- Modify: `internal/control/control.go` (one stale sentence in the sibling plan's package doc; nothing else in that file)
- Modify: `internal/tui/model.go:104`, `internal/tui/model_test.go:16`, `internal/cli/cmd_tui.go:41-43`

**Interfaces:**
- Consumes: nothing from earlier tasks in this plan. `internal/control` already exists — the sibling step-1 plan landed `control.go`, `argv.go` (including `func AttachArgv(spec AttachSpec) (bin string, argv []string, err error)`), `tmux.go` (the `Execer` seam and the `Tmux` driver) and `attach.go` — so add files to it rather than creating it, and re-declare nothing it already holds.
- Produces:
  - `control.ContainerCLI` — `List(ctx) ([]applecontainer.ContainerSummary, error)`, `Stats(ctx) ([]applecontainer.ContainerStats, error)`, `Exec(ctx, name string, cmd []string, opts substrate.ExecOpts) (substrate.ExecResult, error)`
  - `control.EntryStore` — `List() ([]registry.Entry, error)`, `Lookup(project, name string) (registry.Entry, error)`
  - `control.Options{Containers ContainerCLI; Entries EntryStore; Tmux *Tmux; DaemonURL, Home string; Now func() time.Time}`
  - `control.New(Options) *Client`
  - `containerExecer` — the unexported `ContainerCLI`→`Execer` adapter, so the package has one exec transport and `Client.tmux` is the sibling plan's `*Tmux` driver over it
  - `control.Snapshotter` — `Snapshot(ctx context.Context) Snapshot`
  - `func (c *Client) Snapshot(ctx context.Context) Snapshot`
  - `func Correlate(now time.Time, containers []applecontainer.ContainerSummary, entries []registry.Entry, statuses map[string]AgentStatus, browserHealth map[string]BrowserHealth, stats map[string]applecontainer.ContainerStats, daemon DaemonHealth, listErr error) Snapshot`
  - Types `Row`, `RowKind`, `RowState`, `AgentStatus`, `DaemonHealth`, `BrowserHealth`, `Snapshot`; constants `RowProject`, `RowSandbox`, `RowSidecar`, `RowBrowser`, `RowSystem`, `StateStopped`, `StateBooting`, `StateRunning`, `StateDegraded`
  - Unexported helpers later tasks use: `containerName(project, name string) string`, `(c *Client) probeStatus(ctx, e registry.Entry) (AgentStatus, bool)`, `c.actionClient`, `c.entries`, `c.containers`, `c.home`, `c.tmux`

- [ ] **Step 1: Move the tests in first**

The sandbox-side plan already created `internal/control`; `mkdir -p` is there only so this step is safe if it somehow has not. Nothing already in that directory is touched.

```bash
mkdir -p internal/control
git mv internal/tui/correlate_test.go internal/control/correlate_test.go
git mv internal/tui/poll_test.go internal/control/snapshot_test.go
```

Change the package clause of both files from `package tui` to `package control` (first line of each).

- [ ] **Step 2: Rewrite the moved poller test onto the Client seam**

Replace the whole of `internal/control/snapshot_test.go` with this. It is the old `poll_test.go` with `fakeLister` widened into the `ContainerCLI` seam and `NewPoller(...).Poll(ctx)` replaced by `New(Options{...}).Snapshot(ctx)`.

```go
package control

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
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// fakeContainers is the ContainerCLI seam: canned results so control's tests
// never shell out to the real `container` CLI.
type fakeContainers struct {
	out      []applecontainer.ContainerSummary
	err      error
	stats    []applecontainer.ContainerStats
	statsErr error
}

func (f *fakeContainers) List(context.Context) ([]applecontainer.ContainerSummary, error) {
	return f.out, f.err
}

func (f *fakeContainers) Stats(context.Context) ([]applecontainer.ContainerStats, error) {
	return f.stats, f.statsErr
}

func (f *fakeContainers) Exec(context.Context, string, []string, substrate.ExecOpts) (substrate.ExecResult, error) {
	return substrate.ExecResult{}, nil
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

func TestSnapshotFansOutStatusAndCorrelates(t *testing.T) {
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
	c := New(Options{
		Containers: &fakeContainers{out: []applecontainer.ContainerSummary{
			{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
		}},
		Entries:   reg,
		DaemonURL: daemon.URL,
		Now:       func() time.Time { return time.Unix(1_000_000, 0) },
	})

	snap := c.Snapshot(context.Background())

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

func TestSnapshotListErrorCarriedAndDaemonUnreachable(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	c := New(Options{
		Containers: &fakeContainers{err: os.ErrPermission},
		Entries:    reg,
		DaemonURL:  "http://127.0.0.1:1", // unreachable daemon
		Now:        func() time.Time { return time.Unix(0, 0) },
	})
	snap := c.Snapshot(context.Background())
	if snap.Err == nil {
		t.Error("want Err carried from the container lister's failure")
	}
	if snap.Daemon.Reachable {
		t.Error("daemon should be unreachable")
	}
}

func TestSnapshotProbesBrowserHealth(t *testing.T) {
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
	c := New(Options{
		Containers: &fakeContainers{},
		Entries:    reg,
		DaemonURL:  "http://127.0.0.1:1",
		Now:        func() time.Time { return time.Unix(0, 0) },
	})
	// Redirect the CDP probe at the stub (production uses the fixed :9222 port,
	// which httptest can't bind — the browserCDPURL seam exists for exactly this).
	c.browserCDPURL = func(ip string) string { return cdp.URL + "/json/version" }

	// A running "-browser" container is probed and mapped by container name;
	// a non-browser container and a stopped browser are skipped.
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-browser", State: "running", IP: "10.0.0.9"},
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
		{Name: "cspace-beta-browser", State: "stopped", IP: ""},
	}
	m := c.fetchBrowserHealth(context.Background(), containers)
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
```

- [ ] **Step 3: Run the moved tests to verify they fail**

Run: `go test ./internal/control/... -v`

Expected: build failure, e.g.

```
# github.com/elliottregan/cspace/internal/control [github.com/elliottregan/cspace/internal/control.test]
internal/control/correlate_test.go:29:10: undefined: Correlate
internal/control/snapshot_test.go:57:7: undefined: New
FAIL	github.com/elliottregan/cspace/internal/control [build failed]
```

- [ ] **Step 4: Move the domain types**

```bash
git mv internal/tui/types.go internal/control/types.go
```

Then replace the package clause and doc comment at the top of `internal/control/types.go` (its first 4 lines) with a plain file comment — the package doc already lives in the sibling plan's `control.go`, and a second one would give `go doc` two concatenated, disagreeing descriptions:

```go
// This file holds the pure domain types shared by the snapshot query, the
// correlation fold, and the dashboard's model/view. The package doc lives in
// control.go.
package control
```

While here, correct one now-stale sentence in that package doc. In `internal/control/control.go`, change:

```go
// This is step 1's slice of it: the attach argv, the tmux plumbing, and the
// per-sandbox client bookkeeping. The Snapshot / AgentStatus / Ports / Events
// queries and the Down / Send / Interrupt / RestartBrowser / Up actions move
// here in later steps.
```

to:

```go
// This is step 1's slice of it: the attach argv, the tmux plumbing, and the
// per-sandbox client bookkeeping. The Snapshot / AgentStatus / Ports / Events
// queries and the Down / Send / Interrupt / RestartBrowser / Up actions live
// on Client (client.go).
```

Nothing else in `control.go` changes.

The rest of `types.go` (the `RowKind`/`RowState` constants, `AgentStatus`, `Row`, `DaemonHealth`, `BrowserHealth`, `Snapshot`, `containerName`, `browserContainerName`) is unchanged.

- [ ] **Step 5: Move the correlation fold**

```bash
git mv internal/tui/correlate.go internal/control/correlate.go
```

Change its first line from `package tui` to `package control`. Nothing else changes — `Correlate` and `sidecarState` are already pure.

- [ ] **Step 6: Write the Client**

Create `internal/control/client.go`:

```go
package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// probeTimeout bounds each control-port / daemon HTTP probe inside a
// Snapshot. Short so a wedged supervisor degrades one row rather than
// stalling the whole query.
const probeTimeout = 800 * time.Millisecond

// actionTimeout bounds one action's HTTP round trip (send, interrupt).
// Actions are user-initiated and may legitimately take a moment, so they do
// not share the snapshot's aggressive probe budget.
const actionTimeout = 10 * time.Second

// maxProbeConcurrency caps the status fan-out so a host with many sandboxes
// doesn't open an unbounded burst of sockets per snapshot.
const maxProbeConcurrency = 8

// browserCDPPort is the Chrome DevTools HTTP port the browser sidecar
// exposes; the snapshot's health probe is GET http://<ip>:9222/json/version.
const browserCDPPort = 9222

// ContainerCLI is the slice of *applecontainer.Adapter control needs. An
// interface so tests inject canned results without the `container` CLI.
type ContainerCLI interface {
	List(ctx context.Context) ([]applecontainer.ContainerSummary, error)
	Stats(ctx context.Context) ([]applecontainer.ContainerStats, error)
	Exec(ctx context.Context, name string, cmd []string, opts substrate.ExecOpts) (substrate.ExecResult, error)
}

// containerExecer adapts a ContainerCLI to the Execer that tmux.go's driver
// takes, so this package has one exec transport. CLIExecer stays the default
// only for callers that build no Client (the `cspace attach` path).
type containerExecer struct{ cli ContainerCLI }

var _ Execer = containerExecer{}

// Exec implements Execer. A non-zero exit is not an error — tmux says
// ordinary things like "no server running" that way — and the adapter
// already folds *exec.ExitError into ExitCode, so the code passes straight
// through.
func (e containerExecer) Exec(ctx context.Context, container string, cmdline []string) (string, int, error) {
	res, err := e.cli.Exec(ctx, container, cmdline, substrate.ExecOpts{})
	if err != nil {
		return res.Stdout, -1, err
	}
	return res.Stdout, res.ExitCode, nil
}

// EntryStore is the slice of *registry.Registry control needs. An interface
// so an in-sandbox caller can supply a daemon-backed lookup instead of the
// host's registry file.
type EntryStore interface {
	List() ([]registry.Entry, error)
	Lookup(project, name string) (registry.Entry, error)
}

// Options configures a Client. Containers and Entries are required for every
// query; DaemonURL is the host daemon's HTTP base (e.g.
// "http://127.0.0.1:6280"); Home is the host home directory that the session
// and clone trees hang off; Now is injected for testable timestamps and
// defaults to time.Now.
type Options struct {
	Containers ContainerCLI
	Entries    EntryStore

	// Tmux is the in-sandbox tmux driver (tmux.go). Nil builds one over
	// Containers, so the package shares one exec transport and one memoized
	// per-sandbox presence probe.
	Tmux *Tmux

	DaemonURL string
	Home      string
	Now       func() time.Time
}

// Client answers cspace's control queries and runs its control actions. It is
// safe for concurrent use: every method is either read-only over injected
// dependencies or delegates to one that is.
type Client struct {
	containers ContainerCLI
	tmux       *Tmux
	entries    EntryStore
	daemonURL  string
	home       string
	now        func() time.Time

	probeClient  *http.Client
	actionClient *http.Client

	// browserCDPURL builds the CDP version-probe URL from a sidecar IP. A
	// field (not a hardcoded string) so tests can point it at an httptest
	// server; production uses the fixed DevTools port.
	browserCDPURL func(ip string) string
}

// New builds a Client from Options, filling in the defaults.
func New(o Options) *Client {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	tm := o.Tmux
	if tm == nil {
		tm = NewTmux()
		if o.Containers != nil {
			tm.Exec = containerExecer{cli: o.Containers}
		}
	}
	return &Client{
		containers:    o.Containers,
		tmux:          tm,
		entries:       o.Entries,
		daemonURL:     o.DaemonURL,
		home:          o.Home,
		now:           now,
		probeClient:   &http.Client{Timeout: probeTimeout},
		actionClient:  &http.Client{Timeout: actionTimeout},
		browserCDPURL: func(ip string) string { return fmt.Sprintf("http://%s:%d/json/version", ip, browserCDPPort) },
	}
}
```

- [ ] **Step 7: Move the poller onto the Client**

```bash
git mv internal/tui/poll.go internal/control/snapshot.go
```

Replace the whole of `internal/control/snapshot.go` with the following. This is the old `poll.go` with the `realPoller` struct removed (its fields now live on `Client`), `p` renamed to `c`, `p.lister` → `c.containers`, `p.registry` → `c.entries`, `p.client` → `c.probeClient`, and the constants moved to `client.go`.

```go
package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// Snapshotter collects one Snapshot of host state. The dashboard takes this
// interface rather than *Client so its model tests can inject a canned
// snapshot.
type Snapshotter interface {
	Snapshot(ctx context.Context) Snapshot
}

var _ Snapshotter = (*Client)(nil)

// Snapshot reports every sandbox on the host grouped by project: lifecycle,
// memory cap and usage, uptime, nested compose sidecars, the project's
// browser sidecar and its health, and daemon health.
func (c *Client) Snapshot(ctx context.Context) Snapshot {
	containers, listErr := c.containers.List(ctx)
	entries, _ := c.entries.List() // missing file => empty slice, nil

	// `container stats` costs ~2s against Apple Container 1.3 — two orders of
	// magnitude more than `container ls` (~0.03s) — so it runs concurrently
	// with the HTTP probes instead of adding its cost to theirs. Run
	// sequentially it would push a snapshot toward the caller's context
	// ceiling and start timing the whole thing out.
	var (
		stats   map[string]applecontainer.ContainerStats
		statsWG sync.WaitGroup
	)
	statsWG.Add(1)
	go func() {
		defer statsWG.Done()
		stats = c.fetchStats(ctx)
	}()

	statuses := c.fetchStatuses(ctx, entries)
	browserHealth := c.fetchBrowserHealth(ctx, containers)
	daemon := c.fetchDaemon(ctx)
	statsWG.Wait()

	return Correlate(c.now(), containers, entries, statuses, browserHealth, stats, daemon, listErr)
}

// fetchStats samples live per-container resource usage. A stats failure is
// swallowed to an empty map rather than surfaced: usage is decoration on rows
// that are already correct without it, so a wedged stats call must not blank
// the dashboard the way a failed `container ls` legitimately does.
func (c *Client) fetchStats(ctx context.Context) map[string]applecontainer.ContainerStats {
	out := map[string]applecontainer.ContainerStats{}
	samples, err := c.containers.Stats(ctx)
	if err != nil {
		return out
	}
	for _, s := range samples {
		out[s.Name] = s
	}
	return out
}

// fetchBrowserHealth probes each running browser sidecar's Chrome DevTools
// endpoint (GET http://<ip>:9222/json/version) concurrently (bounded).
// Chrome's CDP HTTP endpoint accepts an IP-literal Host, so a host-side probe
// by the sidecar's vmnet IP works. Only successful probes land in the map;
// absence => unreachable.
func (c *Client) fetchBrowserHealth(ctx context.Context, containers []applecontainer.ContainerSummary) map[string]BrowserHealth {
	out := make(map[string]BrowserHealth)
	var mu sync.Mutex
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for _, ct := range containers {
		if !strings.HasSuffix(ct.Name, "-browser") || ct.State != "running" || ct.IP == "" {
			continue
		}
		wg.Add(1)
		go func(ct applecontainer.ContainerSummary) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, ok := c.probeBrowser(ctx, ct.IP)
			if !ok {
				return
			}
			mu.Lock()
			out[ct.Name] = h
			mu.Unlock()
		}(ct)
	}
	wg.Wait()
	return out
}

func (c *Client) probeBrowser(ctx context.Context, ip string) (BrowserHealth, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.browserCDPURL(ip), nil)
	if err != nil {
		return BrowserHealth{}, false
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return BrowserHealth{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return BrowserHealth{}, false
	}
	var body struct {
		Browser string `json:"Browser"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return BrowserHealth{Reachable: true, Version: body.Browser}, true
}

// fetchStatuses probes each entry's GET /status concurrently (bounded). Only
// successful probes land in the map; absence => unreachable (Correlate reads
// that as degraded when the container is running, stopped otherwise).
func (c *Client) fetchStatuses(ctx context.Context, entries []registry.Entry) map[string]AgentStatus {
	out := make(map[string]AgentStatus, len(entries))
	var mu sync.Mutex
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for _, e := range entries {
		if e.ControlURL == "" {
			continue
		}
		wg.Add(1)
		go func(e registry.Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, ok := c.probeStatus(ctx, e)
			if !ok {
				return
			}
			mu.Lock()
			out[containerName(e.Project, e.Name)] = st
			mu.Unlock()
		}(e)
	}
	wg.Wait()
	return out
}

// probeStatus is one authenticated GET /status against a sandbox's control
// port. ok is false when the probe failed (timeout, refused, non-2xx,
// undecodable body) — the caller decides what that means.
func (c *Client) probeStatus(ctx context.Context, e registry.Entry) (AgentStatus, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ControlURL+"/status", nil)
	if err != nil {
		return AgentStatus{}, false
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return AgentStatus{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return AgentStatus{}, false
	}
	var body struct {
		State            string `json:"state"`
		Session          string `json:"session"`
		QueueDepth       int    `json:"queueDepth"`
		LastEventType    string `json:"lastEventType"`
		LastEventSubtype string `json:"lastEventSubtype"`
		LastEventTs      string `json:"lastEventTs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return AgentStatus{}, false
	}
	return AgentStatus{
		Reachable:        true,
		State:            body.State,
		Session:          body.Session,
		QueueDepth:       body.QueueDepth,
		LastEventType:    body.LastEventType,
		LastEventSubtype: body.LastEventSubtype,
		LastEventTs:      body.LastEventTs,
	}, true
}

func (c *Client) fetchDaemon(ctx context.Context) DaemonHealth {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.daemonURL+"/health", nil)
	if err != nil {
		return DaemonHealth{}
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return DaemonHealth{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return DaemonHealth{}
	}
	body, _ := io.ReadAll(resp.Body)
	var h struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(body, &h)
	return DaemonHealth{Reachable: true, Version: h.Version}
}
```

- [ ] **Step 8: Run the control tests and confirm the dependency direction**

Run: `go test ./internal/control/... -v`

Expected: `--- PASS` for `TestCorrelateGroupsSortsAndNests`, `TestCorrelateDegradedWhenSupervisorUnreachable`, `TestCorrelateStoppedWhenNoContainer`, `TestCorrelateBootingFromRegistryState`, `TestCorrelateCarriesListErr`, `TestSnapshotFansOutStatusAndCorrelates`, `TestSnapshotListErrorCarriedAndDaemonUnreachable`, `TestSnapshotProbesBrowserHealth`, then `ok github.com/elliottregan/cspace/internal/control`.

Run: `go list -deps ./internal/control | grep -E 'cspace/internal/(cli|tui)' || echo "one-way dependency ok"`

Expected: `one-way dependency ok`

- [ ] **Step 9: Repoint `internal/tui` with type aliases**

Create `internal/tui/types.go` (the old file was moved away in Step 4) with exactly:

```go
// Package tui implements the `cspace tui` dashboard: a read-and-act view over
// the host's cspace containers.
//
// Its domain types and every data query now live in internal/control, so the
// CLI and this dashboard share one implementation. This file re-exports, as
// type aliases, the names the Bubble Tea model and view use — the dashboard's
// own code is unchanged by the move, and a Row built here is the same type as
// a Row built by internal/control.
package tui

import "github.com/elliottregan/cspace/internal/control"

type (
	RowKind       = control.RowKind
	RowState      = control.RowState
	AgentStatus   = control.AgentStatus
	Row           = control.Row
	DaemonHealth  = control.DaemonHealth
	BrowserHealth = control.BrowserHealth
	Snapshot      = control.Snapshot

	// Poller is the snapshot seam NewModel takes. Named for the dashboard's
	// poll loop; satisfied by *control.Client.
	Poller = control.Snapshotter
)

const (
	RowProject = control.RowProject
	RowSandbox = control.RowSandbox
	RowSidecar = control.RowSidecar
	RowBrowser = control.RowBrowser
	RowSystem  = control.RowSystem
)

const (
	StateStopped  = control.StateStopped
	StateBooting  = control.StateBooting
	StateRunning  = control.StateRunning
	StateDegraded = control.StateDegraded
)
```

- [ ] **Step 10: Rename the snapshot call in the model and its fake**

In `internal/tui/model.go`, inside `pollNowCmd` (line 104), change:

```go
		return snapshotMsg{snap: p.Poll(ctx)}
```

to:

```go
		return snapshotMsg{snap: p.Snapshot(ctx)}
```

In `internal/tui/model_test.go` (line 16), change:

```go
func (f fakePoller) Poll(context.Context) Snapshot { return f.snap }
```

to:

```go
func (f fakePoller) Snapshot(context.Context) Snapshot { return f.snap }
```

- [ ] **Step 11: Repoint `cmd_tui.go` at the control client**

In `internal/cli/cmd_tui.go`, replace lines 41-43:

```go
			poller := tui.NewPoller(adapter, reg, daemonBaseURL, time.Now)
			actor := newTUIActor(adapter, reg, home)
			model := tui.NewModel(poller, actor, home, interval, time.Now)
```

with:

```go
			ctrl := control.New(control.Options{
				Containers: adapter,
				Entries:    reg,
				DaemonURL:  daemonBaseURL,
				Home:       home,
				Now:        time.Now,
			})
			actor := newTUIActor(adapter, reg, home)
			model := tui.NewModel(ctrl, actor, home, interval, time.Now)
```

and add `"github.com/elliottregan/cspace/internal/control"` to its import block.

- [ ] **Step 12: Run the full suite**

Run: `make vet && make lint && make test`

Expected: no vet or lint findings; `ok` for `github.com/elliottregan/cspace/internal/control`, `.../internal/tui`, `.../internal/cli`, and every other package (some report `no test files`).

- [ ] **Step 13: Commit**

```bash
git add internal/control internal/tui internal/cli/cmd_tui.go
git commit -m "Move the dashboard's types, correlation fold and poller into internal/control"
```

---

### Task 2: `AgentStatus`, `Send` and `Interrupt`

Gives the control API the supervisor control-port surface, and repoints the dashboard's actor at it. The HTTP bodies come from `internal/cli/tui_actor.go` and `internal/cli/cmd_agent.go`; the probe comes from Task 1's `probeStatus`.

**Files:**
- Create: `internal/control/agent.go`
- Test: `internal/control/agent_test.go`
- Modify: `internal/cli/tui_actor.go` (Send, Interrupt, the struct, delete `doExpect2xx`)
- Modify: `internal/cli/tui_actor_test.go`
- Modify: `internal/cli/cmd_tui.go` (pass the client to the actor)
- Modify: `internal/cli/cmd_agent.go:163-171` (`agentErrorText` delegates)

**Interfaces:**
- Consumes: `control.Client`, `c.entries`, `c.actionClient`, `c.probeStatus` (Task 1); `tui.Result` (unchanged).
- Produces:
  - `const control.DefaultSession = "primary"`
  - `func (c *Client) AgentStatus(ctx context.Context, project, sandbox string) (AgentStatus, error)`
  - `func (c *Client) Send(ctx context.Context, project, sandbox, session, text string) error`
  - `func (c *Client) Interrupt(ctx context.Context, project, sandbox string) error`
  - `func control.ErrorText(body []byte) string`
  - `func newTUIActor(ctrl *control.Client, a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor`

- [ ] **Step 1: Write the failing tests**

Create `internal/control/agent_test.go`:

```go
package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentStatusReportsSupervisorFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/status" || req.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "session": "primary", "state": "working",
			"lastEventTs": "2026-09-17T18:40:12Z", "lastEventType": "assistant",
			"lastEventSubtype": "", "queueDepth": 2,
		})
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	got, err := c.AgentStatus(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("AgentStatus: %v", err)
	}
	if !got.Reachable || got.State != "working" || got.Session != "primary" || got.QueueDepth != 2 {
		t.Errorf("status = %+v", got)
	}
	if got.LastEventType != "assistant" || got.LastEventTs != "2026-09-17T18:40:12Z" {
		t.Errorf("last event = %+v", got)
	}
}

func TestAgentStatusUnreachableIsNotAnError(t *testing.T) {
	// Port 1 refuses instantly: an unreachable supervisor degrades the value,
	// it does not fail the query — the caller renders "degraded", not an error.
	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", "http://127.0.0.1:1", "tok")})
	got, err := c.AgentStatus(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("an unreachable supervisor must not be an error, got %v", err)
	}
	if got.Reachable {
		t.Errorf("status = %+v, want Reachable false", got)
	}
}

func TestAgentStatusUnknownSandboxErrors(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", "http://127.0.0.1:1", "")})
	if _, err := c.AgentStatus(context.Background(), "alpha", "nope"); err == nil {
		t.Error("an unregistered sandbox must error")
	}
}

func TestSendPostsSessionAndText(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotAuth, gotCT = req.URL.Path, req.Header.Get("Authorization"), req.Header.Get("Content-Type")
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("queued"))
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	if err := c.Send(context.Background(), "alpha", "mercury", "", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Errorf("request: path=%q auth=%q ct=%q", gotPath, gotAuth, gotCT)
	}
	// An empty session argument means the supervisor's own session id.
	if gotBody["session"] != DefaultSession || gotBody["text"] != "hello" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestSendSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "queue closed"})
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	err := c.Send(context.Background(), "alpha", "mercury", "primary", "hello")
	if err == nil || !strings.Contains(err.Error(), "queue closed") {
		t.Errorf("err = %v, want it to carry the server's error text", err)
	}
}

func TestInterruptStatusHandling(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string // "" means the call must succeed
	}{
		{"2xx is success", 200, `{"ok":true}`, ""},
		{"409 no active task is benign", 409, `{"ok":false,"error":"no active task"}`, ""},
		{"500 surfaces the server's error text", 500, `{"ok":false,"error":"boom"}`, "boom"},
		{"non-JSON body falls back to raw text", 503, "upstream gone", "upstream gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotPath = req.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
			err := c.Interrupt(context.Background(), "alpha", "mercury")
			if gotPath != "/interrupt" {
				t.Errorf("path = %q, want /interrupt", gotPath)
			}
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("want success, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestErrorText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"supervisor error envelope", `{"ok":false,"error":"no active task"}`, "no active task"},
		{"plain text body", "  not found\n", "not found"},
		{"json without an error field", `{"ok":true}`, `{"ok":true}`},
	}
	for _, tc := range cases {
		if got := ErrorText([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: ErrorText(%q) = %q, want %q", tc.name, tc.body, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/control/... -run 'TestAgentStatus|TestSend|TestInterrupt|TestErrorText' -v`

Expected: build failure listing `undefined: DefaultSession`, `c.AgentStatus undefined`, `c.Send undefined`, `c.Interrupt undefined`, `undefined: ErrorText`, ending in `FAIL github.com/elliottregan/cspace/internal/control [build failed]`.

- [ ] **Step 3: Implement the agent surface**

Create `internal/control/agent.go`:

```go
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elliottregan/cspace/internal/registry"
)

// DefaultSession is the supervisor's own session id (SESSION_ID in
// lib/agent-supervisor-bun/src/main.ts). It is hardcoded there — do not make
// it configurable here.
//
// Unrelated to SessionClaude / SessionShell in control.go: those are tmux
// session names inside the sandbox, this is the headless supervisor's session
// id on disk and over its control port.
const DefaultSession = "primary"

// AgentStatus reports the sandbox supervisor's authenticated GET /status.
//
// An unreachable supervisor is deliberately NOT an error: it yields
// AgentStatus{Reachable: false} with a nil error, matching how Snapshot
// treats a failed probe — the row degrades, the query does not fail. A
// non-nil error means the sandbox could not be resolved at all.
func (c *Client) AgentStatus(ctx context.Context, project, sandbox string) (AgentStatus, error) {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return AgentStatus{}, err
	}
	st, _ := c.probeStatus(ctx, e)
	return st, nil
}

// Send injects a user turn into the sandbox supervisor's prompt queue. An
// empty session means DefaultSession.
func (c *Client) Send(ctx context.Context, project, sandbox, session, text string) error {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return err
	}
	if session == "" {
		session = DefaultSession
	}
	payload, err := json.Marshal(map[string]string{"session": session, "text": text})
	if err != nil {
		return fmt.Errorf("encode send payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ControlURL+"/send", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req, e)

	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, ErrorText(body))
	}
	return nil
}

// Interrupt cancels the sandbox agent's in-flight task.
//
// A 409 is not a failure: main.ts answers {"ok":false,"error":"no active
// task"} when nothing is running, which means the agent was simply idle. The
// dashboard has always reported that as a benign outcome, and so does this.
func (c *Client) Interrupt(ctx context.Context, project, sandbox string) error {
	e, err := c.lookup(project, sandbox)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ControlURL+"/interrupt", nil)
	if err != nil {
		return err
	}
	authorize(req, e)

	resp, body, err := c.do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, ErrorText(body))
	}
	return nil
}

// lookup resolves a sandbox's registry entry, wrapping the failure so the
// caller's error names the sandbox rather than a bare map miss.
func (c *Client) lookup(project, sandbox string) (registry.Entry, error) {
	e, err := c.entries.Lookup(project, sandbox)
	if err != nil {
		return registry.Entry{}, fmt.Errorf("look up sandbox %s/%s: %w", project, sandbox, err)
	}
	return e, nil
}

// authorize attaches the sandbox's bearer token. The supervisor enforces it
// on every route, so an entry with no token can only be a pre-auth sandbox.
func authorize(req *http.Request, e registry.Entry) {
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
}

// do runs an action request and reads its whole body, which every caller
// needs for ErrorText.
func (c *Client) do(req *http.Request) (*http.Response, []byte, error) {
	resp, err := c.actionClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body, nil
}

// ErrorText extracts the meaningful error text from a non-2xx supervisor
// response body. main.ts's envelope is {"ok":false,"error":"…"}; when the
// body parses as that shape, return just the error field so callers don't
// print a raw JSON envelope at the user. Anything else (e.g. a plain-text
// http.Error body) falls back to the trimmed raw body.
func ErrorText(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != "" {
		return parsed.Error
	}
	return strings.TrimSpace(string(body))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/control/... -run 'TestAgentStatus|TestSend|TestInterrupt|TestErrorText' -v`

Expected: `--- PASS` for `TestAgentStatusReportsSupervisorFields`, `TestAgentStatusUnreachableIsNotAnError`, `TestAgentStatusUnknownSandboxErrors`, `TestSendPostsSessionAndText`, `TestSendSurfacesServerError`, `TestInterruptStatusHandling` (with its four subtests) and `TestErrorText`, then `ok github.com/elliottregan/cspace/internal/control`.

- [ ] **Step 5: Repoint the dashboard actor's Send and Interrupt**

In `internal/cli/tui_actor.go`:

Replace the struct's HTTP field with the control client, keep `home`, and rewrite `Send` and `Interrupt` to delegate; delete `doExpect2xx` (it becomes unused and the `unused` linter will reject it). `Attach`, `Down` and `RestartBrowser` keep the bodies they already have — `Attach` is the control-plane version the sibling step-1 plan installed, reproduced below so the whole file is unambiguous. The result:

```go
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor against the real host. Supervisor traffic
// goes through internal/control so the CLI and the dashboard share one
// implementation; attach stays here because it needs tea.ExecProcess to hand
// the terminal to the child, and down still calls teardownSandbox directly.
// home is kept for the attach lock and client records under
// ~/.cspace/controlplane/. Constructed by cmd_tui.go.
type tuiActor struct {
	ctrl     *control.Client
	adapter  *applecontainer.Adapter
	registry *registry.Registry
	home     string
}

func newTUIActor(ctrl *control.Client, a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor {
	return &tuiActor{ctrl: ctrl, adapter: a, registry: r, home: home}
}

// Attach joins the sandbox's tmux session through the same control-plane path
// `cspace attach` uses, so the two share one session rather than running two
// claudes against one workspace. tea.ExecProcess suspends the dashboard and
// runs the exec in the foreground; its callback is where the detach happens.
//
// Installed verbatim by the sibling step-1 plan (its Task 8). Do not replace
// it with the old `attachArgs` version — that function no longer exists.
func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	ctx := context.Background()
	spec := control.ClaudeAttach(row.Container, defaultTmux.Present(ctx, row.Container))
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	att, err := control.BeginAttach(ctx, defaultTmux, row.Container,
		control.ControlPlaneDir(t.home, row.Project, row.Name), spec.Session)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if closeErr := att.Close(closeCtx); closeErr != nil && execErr == nil {
			execErr = closeErr
		}
		return tui.Result("attach", execErr)
	})
}

func (t *tuiActor) Down(row tui.Row) tea.Cmd {
	adapter, reg, project, name := t.adapter, t.registry, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var buf bytes.Buffer
		teardownSandbox(ctx, adapter, reg, project, name, &buf, true /* wipeState */)
		// teardownSandbox has no return value and swallows the container Stop
		// error; its only failure signal is warning text written to the
		// captured writer (prefix "[cspace] warning:"). Surface those warnings
		// instead of a false "down ok".
		if strings.Contains(buf.String(), "warning:") {
			return tui.Result("down", fmt.Errorf("%s", strings.TrimSpace(buf.String())))
		}
		return tui.Result("down", nil)
	}
}

func (t *tuiActor) Send(row tui.Row, text string) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("send", ctrl.Send(ctx, project, name, "", text))
	}
}

func (t *tuiActor) Interrupt(row tui.Row) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("interrupt", ctrl.Interrupt(ctx, project, name))
	}
}

// RestartBrowser restarts the project's shared browser sidecar via the same
// seam the daemon's restart handler uses. Empty plVersion lets the ladder pin
// the version from the running sidecar (sidecarVersion) or fall back to
// defaultPlaywrightVersion. Uses restartBrowserFn (var-seam) so tests can fake it.
func (t *tuiActor) RestartBrowser(row tui.Row) tea.Cmd {
	project := row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, err := restartBrowserFn(ctx, project, "")
		return tui.Result("browser restart", err)
	}
}
```

The outer 30s context plus the client's own 10s `actionTimeout` keeps the effective bound at the 10s the actor used before.

- [ ] **Step 6: Pass the client to the actor**

In `internal/cli/cmd_tui.go`, change the actor line to:

```go
			actor := newTUIActor(ctrl, adapter, reg, home)
```

- [ ] **Step 7: Repoint the actor tests**

Replace `internal/cli/tui_actor_test.go` with:

```go
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/tui"
)

func drain(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// actorAgainst builds an actor whose control client resolves alpha/mercury to
// the given stub supervisor, so the actor's HTTP path is exercised end to end
// through the same registry lookup production uses.
func actorAgainst(t *testing.T, controlURL string) *tuiActor {
	t.Helper()
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{
		Project: "alpha", Name: "mercury", ControlURL: controlURL, Token: "tok", State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return newTUIActor(control.New(control.Options{Entries: reg}), nil, reg, t.TempDir())
}

func TestTUIActorSendPostsToControlURL(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotCT = req.Header.Get("Content-Type")
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		gotBody = body["text"]
		w.WriteHeader(200)
		_, _ = w.Write([]byte("queued"))
	}))
	defer srv.Close()

	a := actorAgainst(t, srv.URL)
	row := tui.Row{Kind: tui.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drain(a.Send(row, "hello"))

	if err := tui.ResultErr(msg); err != nil {
		t.Errorf("send should succeed, got %v", err)
	}
	if l, _ := tui.ResultLabel(msg); l != "send" {
		t.Errorf("label = %q, want \"send\"", l)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" || gotBody != "hello" {
		t.Errorf("send request: path=%q auth=%q ct=%q body=%q", gotPath, gotAuth, gotCT, gotBody)
	}
}

func TestTUIActorInterrupt409IsBenign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no active task"})
	}))
	defer srv.Close()

	a := actorAgainst(t, srv.URL)
	row := tui.Row{Kind: tui.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drain(a.Interrupt(row))
	// A 409 "no active task" is not an error state — the agent was simply idle.
	if err := tui.ResultErr(msg); err != nil {
		t.Errorf("interrupt 409 should be benign, got error: %v", err)
	}
	if l, _ := tui.ResultLabel(msg); l != "interrupt" {
		t.Errorf("label = %q, want \"interrupt\"", l)
	}
}

func TestTUIActorInterrupt500Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "boom"})
	}))
	defer srv.Close()

	a := actorAgainst(t, srv.URL)
	row := tui.Row{Kind: tui.RowSandbox, Project: "alpha", Name: "mercury"}
	msg := drain(a.Interrupt(row))
	if !msgHasError(msg, "boom") {
		t.Errorf("interrupt 500 should surface an error: %#v", msg)
	}
}

func msgHasError(msg tea.Msg, want string) bool {
	err := tui.ResultErr(msg)
	return err != nil && strings.Contains(err.Error(), want)
}
```

- [ ] **Step 8: Make `agentErrorText` delegate instead of duplicating**

In `internal/cli/cmd_agent.go`, replace the body of `agentErrorText` (lines 163-171) so the CLI and control share one parser:

```go
func agentErrorText(body []byte) string {
	return control.ErrorText(body)
}
```

Keep the existing doc comment above it and add `"github.com/elliottregan/cspace/internal/control"` to that file's imports. `"strings"` is now unused in `cmd_agent.go` (it was only there for `agentErrorText`) — remove it. `"encoding/json"` stays: `runAgentStatus` still unmarshals into `agentStatusResponse`.

- [ ] **Step 9: Run the full suite**

Run: `make vet && make lint && make test`

Expected: no findings; `ok` for `internal/control`, `internal/cli` and `internal/tui`.

- [ ] **Step 10: Commit**

```bash
git add internal/control internal/cli
git commit -m "Move the supervisor status, send and interrupt calls into internal/control"
```

---

### Task 3: `Down` and `RestartBrowser` behind a `Host` seam

`teardownSandbox` and the browser restart ladder are entangled with `internal/cli` (the `cfg` global, `internal/devcontainer`, `internal/sidecars`, the `restartBrowserFn` var-seam). Moving their bodies is a separate refactor and would risk the "behavior unchanged" constraint. Instead `internal/control` declares the narrow interface it needs and `internal/cli` implements it — the same consumer-defined-interface direction `tui.Actor` already uses.

**Files:**
- Create: `internal/control/host.go`
- Create: `internal/control/actions.go`
- Test: `internal/control/actions_test.go`
- Create: `internal/cli/control_host.go`
- Modify: `internal/control/client.go` (add `Host` to `Options` and `Client`)
- Modify: `internal/cli/cmd_tui.go`, `internal/cli/tui_actor.go`, `internal/cli/tui_actor_test.go`

**Interfaces:**
- Consumes: `control.Client`, `control.Options` (Task 1); `teardownSandbox` (`internal/cli/cmd_down.go:166`), `restartBrowserFn` (`internal/cli/browser.go:228`).
- Produces:
  - `control.Host` — `Teardown(ctx context.Context, project, sandbox string, out io.Writer, wipeState bool)`, `RestartBrowser(ctx context.Context, project string) error`
  - `var control.ErrNoHost error`
  - `func (c *Client) Down(ctx context.Context, project, sandbox string) error`
  - `func (c *Client) RestartBrowser(ctx context.Context, project string) error`
  - `Options.Host Host`
  - `func newTUIActor(ctrl *control.Client, home string) *tuiActor`
  - `func newCLIHost(a *applecontainer.Adapter, r *registry.Registry) *cliHost` in `internal/cli`

- [ ] **Step 1: Write the failing tests**

Create `internal/control/actions_test.go`:

```go
package control

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type teardownCall struct {
	project   string
	sandbox   string
	wipeState bool
}

// fakeHost records what control asked the host layer to do and replays a
// canned outcome.
type fakeHost struct {
	teardownCalls []teardownCall
	teardownOut   string

	restartCalls []string
	restartErr   error
}

func (h *fakeHost) Teardown(_ context.Context, project, sandbox string, out io.Writer, wipeState bool) {
	h.teardownCalls = append(h.teardownCalls, teardownCall{project, sandbox, wipeState})
	_, _ = io.WriteString(out, h.teardownOut)
}

func (h *fakeHost) RestartBrowser(_ context.Context, project string) error {
	h.restartCalls = append(h.restartCalls, project)
	return h.restartErr
}

func TestDownReportsTeardownWarningsAsFailure(t *testing.T) {
	// teardownSandbox has no error return: its only failure signal is
	// "[cspace] warning: …" text on the writer. The dashboard has always
	// treated any warning as a failed action (cs-finding
	// 2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure tracks
	// that this over-reports); the move must not change that.
	cases := []struct {
		name    string
		out     string
		wantErr string // "" means the call must succeed
	}{
		{"quiet teardown succeeds", "sandbox mercury down\n", ""},
		{"warning text fails the action", "[cspace] warning: remove volume cspace-alpha-mercury-data: busy\nsandbox mercury down\n", "remove volume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{teardownOut: tc.out}
			c := New(Options{Containers: &fakeContainers{}, Host: h})
			err := c.Down(context.Background(), "alpha", "mercury")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("want success, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			want := teardownCall{project: "alpha", sandbox: "mercury", wipeState: true}
			if len(h.teardownCalls) != 1 || h.teardownCalls[0] != want {
				t.Errorf("teardown calls = %+v, want exactly %+v", h.teardownCalls, want)
			}
		})
	}
}

func TestRestartBrowserDelegatesToHost(t *testing.T) {
	h := &fakeHost{}
	c := New(Options{Containers: &fakeContainers{}, Host: h})
	if err := c.RestartBrowser(context.Background(), "alpha"); err != nil {
		t.Fatalf("RestartBrowser: %v", err)
	}
	if len(h.restartCalls) != 1 || h.restartCalls[0] != "alpha" {
		t.Errorf("restart calls = %v, want [alpha]", h.restartCalls)
	}

	h.restartErr = errors.New("ladder gave up")
	if err := c.RestartBrowser(context.Background(), "alpha"); err == nil || !strings.Contains(err.Error(), "ladder gave up") {
		t.Errorf("err = %v, want the host's error carried through", err)
	}
}

func TestHostBackedActionsFailClosedWithoutAHost(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	if err := c.Down(context.Background(), "alpha", "mercury"); !errors.Is(err, ErrNoHost) {
		t.Errorf("Down err = %v, want ErrNoHost", err)
	}
	if err := c.RestartBrowser(context.Background(), "alpha"); !errors.Is(err, ErrNoHost) {
		t.Errorf("RestartBrowser err = %v, want ErrNoHost", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/control/... -run 'TestDown|TestRestartBrowser|TestHostBacked' -v`

Expected: build failure with `unknown field Host in struct literal of type Options`, `c.Down undefined`, `c.RestartBrowser undefined`, `undefined: ErrNoHost`, ending in `FAIL github.com/elliottregan/cspace/internal/control [build failed]`.

- [ ] **Step 3: Declare the Host seam**

Create `internal/control/host.go`:

```go
package control

import (
	"context"
	"errors"
	"io"
)

// Host is the set of host-side operations control does not own: their
// implementations still live in internal/cli, which control must not import.
// Whoever builds the Client supplies one.
//
// This is the same direction as internal/tui's Actor — the consumer declares
// the narrow interface it needs and the package that has the implementation
// satisfies it — so the dependency graph stays acyclic while there is still
// exactly one implementation of each operation.
type Host interface {
	// Teardown stops and removes a sandbox, wiping its clone, sessions and
	// volumes when wipeState is true, and writes progress plus any
	// "[cspace] warning: …" lines to out. It has no error return on purpose:
	// the underlying teardownSandbox has none either, and its only failure
	// signal is that warning text.
	Teardown(ctx context.Context, project, sandbox string, out io.Writer, wipeState bool)

	// RestartBrowser restarts the project's shared browser sidecar through
	// the escalation ladder (stop → SIGKILL → host-process teardown → start)
	// and reverifies liveness with protocol-level probes.
	RestartBrowser(ctx context.Context, project string) error
}

// ErrNoHost is returned by the actions that need a Host when the Client was
// built without one. Failing closed beats a nil-pointer panic in a UI that
// only meant to read.
var ErrNoHost = errors.New("control: no Host configured for this action")
```

- [ ] **Step 4: Add the Host to Options and Client**

In `internal/control/client.go`, add the field to `Options` (after `Entries`):

```go
	// Host supplies the operations whose implementations still live in
	// internal/cli. Nil is legal: read-only callers need no Host, and the
	// actions that do fail with ErrNoHost.
	Host Host
```

add the field to `Client` (after `entries`):

```go
	host       Host
```

and set it in `New`, after `entries: o.Entries,`:

```go
		host:          o.Host,
```

- [ ] **Step 5: Implement the actions**

Create `internal/control/actions.go`:

```go
package control

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// Down tears down a sandbox: container, sidecars, registry entry, and — as
// `cspace down`'s default does — its clone, sessions and volumes.
//
// The reporting rule is inherited verbatim from the dashboard. teardownSandbox
// has no error return and swallows the container Stop error; its only failure
// signal is "[cspace] warning: …" text, so any warning becomes this action's
// error. That over-reports benign cleanup noise as a failure — see
// .cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md
// — and is preserved here deliberately: fixing it means changing
// teardownSandbox's signature, which the CLI down path shares.
func (c *Client) Down(ctx context.Context, project, sandbox string) error {
	if c.host == nil {
		return ErrNoHost
	}
	var buf bytes.Buffer
	c.host.Teardown(ctx, project, sandbox, &buf, true /* wipeState */)
	if strings.Contains(buf.String(), "warning:") {
		return fmt.Errorf("%s", strings.TrimSpace(buf.String()))
	}
	return nil
}

// RestartBrowser restarts the project's shared browser sidecar and waits for
// it to answer again. The caller's context carries the deadline; the ladder
// itself applies its own restart budget on top.
func (c *Client) RestartBrowser(ctx context.Context, project string) error {
	if c.host == nil {
		return ErrNoHost
	}
	return c.host.RestartBrowser(ctx, project)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/control/... -run 'TestDown|TestRestartBrowser|TestHostBacked' -v`

Expected: `--- PASS` for `TestDownReportsTeardownWarningsAsFailure` (both subtests), `TestRestartBrowserDelegatesToHost` and `TestHostBackedActionsFailClosedWithoutAHost`, then `ok github.com/elliottregan/cspace/internal/control`.

- [ ] **Step 7: Implement the Host in `internal/cli`**

Create `internal/cli/control_host.go`:

```go
package cli

import (
	"context"
	"io"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// cliHost implements control.Host over the two host operations whose
// implementations still live in this package: teardownSandbox (cmd_down.go)
// and the browser restart ladder (browser.go). It exists so internal/control
// can run them without importing internal/cli, keeping that dependency
// one-way.
type cliHost struct {
	adapter  *applecontainer.Adapter
	registry *registry.Registry
}

var _ control.Host = (*cliHost)(nil)

func newCLIHost(a *applecontainer.Adapter, r *registry.Registry) *cliHost {
	return &cliHost{adapter: a, registry: r}
}

func (h *cliHost) Teardown(ctx context.Context, project, sandbox string, out io.Writer, wipeState bool) {
	teardownSandbox(ctx, h.adapter, h.registry, project, sandbox, out, wipeState)
}

// RestartBrowser goes through restartBrowserFn, the same var-seam the
// daemon's POST /browser/restart/{project} handler uses, so a test can fake
// the ladder's outcome without touching real containers. An empty version
// lets the ladder pin the running sidecar's version or fall back to the
// default.
func (h *cliHost) RestartBrowser(ctx context.Context, project string) error {
	_, err := restartBrowserFn(ctx, project, "")
	return err
}
```

- [ ] **Step 8: Wire the Host and shrink the actor**

In `internal/cli/cmd_tui.go`, add `Host` to the options and drop the actor's extra arguments:

```go
			ctrl := control.New(control.Options{
				Containers: adapter,
				Entries:    reg,
				Host:       newCLIHost(adapter, reg),
				DaemonURL:  daemonBaseURL,
				Home:       home,
				Now:        time.Now,
			})
			actor := newTUIActor(ctrl, home)
			model := tui.NewModel(ctrl, actor, home, interval, time.Now)
```

In `internal/cli/tui_actor.go`, replace the whole file with:

```go
package cli

import (
	"context"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor by delegating to internal/control, which owns
// the one implementation of each action. Only Attach stays here: it needs
// tea.ExecProcess to hand the terminal to the child, which is a Bubble Tea
// concern and therefore not control's. home is kept for the attach lock and
// client records under ~/.cspace/controlplane/. Constructed by cmd_tui.go.
type tuiActor struct {
	ctrl *control.Client
	home string
}

func newTUIActor(ctrl *control.Client, home string) *tuiActor {
	return &tuiActor{ctrl: ctrl, home: home}
}

func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	ctx := context.Background()
	spec := control.ClaudeAttach(row.Container, defaultTmux.Present(ctx, row.Container))
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	att, err := control.BeginAttach(ctx, defaultTmux, row.Container,
		control.ControlPlaneDir(t.home, row.Project, row.Name), spec.Session)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if closeErr := att.Close(closeCtx); closeErr != nil && execErr == nil {
			execErr = closeErr
		}
		return tui.Result("attach", execErr)
	})
}

func (t *tuiActor) Down(row tui.Row) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return tui.Result("down", ctrl.Down(ctx, project, name))
	}
}

func (t *tuiActor) Send(row tui.Row, text string) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("send", ctrl.Send(ctx, project, name, "", text))
	}
}

func (t *tuiActor) Interrupt(row tui.Row) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("interrupt", ctrl.Interrupt(ctx, project, name))
	}
}

func (t *tuiActor) RestartBrowser(row tui.Row) tea.Cmd {
	ctrl, project := t.ctrl, row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return tui.Result("browser restart", ctrl.RestartBrowser(ctx, project))
	}
}
```

In `internal/cli/tui_actor_test.go`, `actorAgainst` now passes `newTUIActor` two arguments:

```go
	return newTUIActor(control.New(control.Options{Entries: reg}), t.TempDir())
```

and the `"github.com/elliottregan/cspace/internal/registry"` import stays (the helper still builds a registry), while nothing else changes.

- [ ] **Step 9: Run the full suite**

Run: `make vet && make lint && make test`

Expected: no findings; `ok` for `internal/control`, `internal/cli`, `internal/tui`.

- [ ] **Step 10: Commit**

```bash
git add internal/control internal/cli
git commit -m "Route the dashboard's down and browser restart through internal/control"
```

---

### Task 4: `Events` and `InteractiveState`

Moves the event-log tail over with its tests, and adds the read of the hook-written interactive-state file the control plane's sidebar glyph will use.

**Files:**
- Create: `internal/control/paths.go`
- Create: `internal/control/events.go` (from `internal/tui/events.go`)
- Create: `internal/control/interactive.go`
- Test: `internal/control/paths_test.go`
- Test: `internal/control/events_test.go` (from `internal/tui/events_test.go`)
- Test: `internal/control/interactive_test.go`
- Delete: `internal/tui/events.go`, `internal/tui/events_test.go`
- Modify: `internal/tui/types.go` (alias `EventLine`), `internal/tui/model.go:115`

**Interfaces:**
- Consumes: `control.Client`, `c.home` (Task 1).
- Produces:
  - `func SessionDir(home, project, sandbox string) string`
  - `func SessionEventsPath(home, project, sandbox string) string`
  - `func AgentStatePath(home, project, sandbox string) string`
  - `type EventLine struct{ Ts, Kind, Type, Subtype string }`
  - `func TailEvents(path string, n int) ([]EventLine, error)`
  - `func (c *Client) Events(project, sandbox string, n int) ([]EventLine, error)`
  - `type InteractiveState struct{ State, At, SessionID, Event string }` with `func (s InteractiveState) Known() bool`
  - `func ReadInteractiveState(path string) InteractiveState`
  - `func (c *Client) InteractiveState(project, sandbox string) InteractiveState`

- [ ] **Step 1: Move the event tests and write the new ones**

```bash
git mv internal/tui/events_test.go internal/control/events_test.go
```

Change its first line to `package control`, and **delete `TestSessionEventsPath` from it** (it moves to `paths_test.go` below).

Create `internal/control/paths_test.go`:

```go
package control

import "testing"

func TestHostPaths(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"session dir", SessionDir("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury"},
		{"events log", SessionEventsPath("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury/primary/events.ndjson"},
		{"agent state", AgentStatePath("/home/x", "alpha", "mercury"), "/home/x/.cspace/sessions/alpha/mercury/agent-state.json"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
```

Create `internal/control/interactive_test.go`:

```go
package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInteractiveState(t *testing.T) {
	cases := []struct {
		name      string
		write     bool
		contents  string
		wantState string
		wantKnown bool
	}{
		{
			name:      "a complete record parses",
			write:     true,
			contents:  `{"state":"working","at":"2026-09-17T18:40:12Z","session_id":"abc123","event":"UserPromptSubmit"}`,
			wantState: "working",
			wantKnown: true,
		},
		{
			// cspace-agent-state.sh writes a temp file and renames, but a
			// crashed writer (or a non-atomic filesystem) can still leave a
			// half-written record. It must read as "unknown", never as an error.
			name:      "a torn write reads as unknown",
			write:     true,
			contents:  `{"state":"nee`,
			wantState: "",
			wantKnown: false,
		},
		{
			name:      "an empty file reads as unknown",
			write:     true,
			contents:  "",
			wantState: "",
			wantKnown: false,
		},
		{
			name:      "a missing file reads as unknown",
			write:     false,
			wantState: "",
			wantKnown: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-state.json")
			if tc.write {
				if err := os.WriteFile(path, []byte(tc.contents), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got := ReadInteractiveState(path)
			if got.State != tc.wantState {
				t.Errorf("State = %q, want %q", got.State, tc.wantState)
			}
			if got.Known() != tc.wantKnown {
				t.Errorf("Known() = %v, want %v", got.Known(), tc.wantKnown)
			}
		})
	}
}

func TestClientInteractiveStateAndEventsReadTheSessionDir(t *testing.T) {
	home := t.TempDir()
	dir := SessionDir(home, "alpha", "mercury")
	if err := os.MkdirAll(filepath.Join(dir, "primary"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(AgentStatePath(home, "alpha", "mercury"),
		[]byte(`{"state":"needs-input","event":"PermissionRequest"}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.WriteFile(SessionEventsPath(home, "alpha", "mercury"),
		[]byte(`{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	c := New(Options{Containers: &fakeContainers{}, Home: home})

	if got := c.InteractiveState("alpha", "mercury"); got.State != "needs-input" || got.Event != "PermissionRequest" {
		t.Errorf("InteractiveState = %+v", got)
	}
	lines, err := c.Events("alpha", "mercury", 8)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(lines) != 1 || lines[0].Type != "assistant" {
		t.Errorf("Events = %+v", lines)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/control/... -run 'TestHostPaths|TestTailEvents|TestReadInteractiveState|TestClientInteractiveState' -v`

Expected: build failure listing `undefined: SessionDir`, `undefined: SessionEventsPath`, `undefined: AgentStatePath`, `undefined: TailEvents`, `undefined: ReadInteractiveState`, ending in `FAIL github.com/elliottregan/cspace/internal/control [build failed]`.

- [ ] **Step 3: Write the host path layout**

Create `internal/control/paths.go`:

```go
package control

import "path/filepath"

// The host-side layout cspace materializes per sandbox. Every path here
// mirrors a literal cmd_up.go uses when it builds the container's mounts —
// change one and you must change the other.
//
// ControlPlaneDir (~/.cspace/controlplane/<project>/<sandbox>, the attach lock
// and client records) is the fourth member of this layout and already lives in
// control.go — do not redeclare it here.

// SessionDir is the host directory bind-mounted into a sandbox as /sessions.
func SessionDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "sessions", project, sandbox)
}

// SessionEventsPath is the supervisor's event log. The "primary" segment is
// the supervisor's hardcoded SESSION_ID — do not make it configurable.
func SessionEventsPath(home, project, sandbox string) string {
	return filepath.Join(SessionDir(home, project, sandbox), DefaultSession, "events.ndjson")
}

// AgentStatePath is the interactive session's state file, written by the
// Claude Code hooks running cspace-agent-state.sh inside the sandbox. It sits
// at the root of /sessions, not under a session id: there is one interactive
// Claude per sandbox.
func AgentStatePath(home, project, sandbox string) string {
	return filepath.Join(SessionDir(home, project, sandbox), "agent-state.json")
}
```

- [ ] **Step 4: Move the event tail**

```bash
git mv internal/tui/events.go internal/control/events.go
```

In `internal/control/events.go`: change the first line to `package control`, and **delete the `SessionEventsPath` function and the `"path/filepath"` import** (both now live in `paths.go`). Then append the Client method:

```go
// Events returns the tail of a sandbox's supervisor event log, newest last.
func (c *Client) Events(project, sandbox string, n int) ([]EventLine, error) {
	return TailEvents(SessionEventsPath(c.home, project, sandbox), n)
}
```

- [ ] **Step 5: Implement the interactive state read**

Create `internal/control/interactive.go`:

```go
package control

import (
	"encoding/json"
	"os"
)

// InteractiveState is the interactive Claude session's state, written into a
// sandbox's /sessions directory by the Claude Code hooks that run
// cspace-agent-state.sh. Known states are "starting", "working",
// "needs-input", "idle" and "exited".
//
// It describes the session a person drives through `cspace attach` or a
// control-plane pane — not the headless supervisor, whose state comes from
// AgentStatus.
type InteractiveState struct {
	State     string `json:"state"`
	At        string `json:"at"`
	SessionID string `json:"session_id"`
	Event     string `json:"event"`
}

// Known reports whether a state was actually read. It is false before the
// first hook has fired, after `cspace down` wiped the session tree, and for
// an unparseable record — all cases where the caller should fall back to the
// supervisor's state rather than render a wrong glyph.
func (s InteractiveState) Known() bool { return s.State != "" }

// ReadInteractiveState parses the hook-written state file at path.
//
// It is deliberately total: a missing file, an unreadable one, and a torn
// write all yield the zero value. The writer renames a temp file into place,
// so a partial record should be impossible — but a crashed writer or a
// filesystem without atomic rename can still leave one, and a dashboard
// polling this file once a second must not turn that into an error banner.
func ReadInteractiveState(path string) InteractiveState {
	data, err := os.ReadFile(path)
	if err != nil {
		return InteractiveState{}
	}
	var s InteractiveState
	if err := json.Unmarshal(data, &s); err != nil {
		return InteractiveState{}
	}
	return s
}

// InteractiveState reads the sandbox's hook-written interactive session state.
func (c *Client) InteractiveState(project, sandbox string) InteractiveState {
	return ReadInteractiveState(AgentStatePath(c.home, project, sandbox))
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/control/... -run 'TestHostPaths|TestTailEvents|TestReadInteractiveState|TestClientInteractiveState' -v`

Expected: `--- PASS` for `TestHostPaths`, `TestTailEventsLastN`, `TestTailEventsFewerThanN`, `TestTailEventsToleratesMalformedTrailingLine`, `TestTailEventsMissingFileIsNotError`, `TestReadInteractiveState` (four subtests) and `TestClientInteractiveStateAndEventsReadTheSessionDir`, then `ok github.com/elliottregan/cspace/internal/control`.

- [ ] **Step 7: Repoint the dashboard's event read**

In `internal/tui/types.go`, add `EventLine` to the type alias block (after `Snapshot`):

```go
	EventLine     = control.EventLine
```

In `internal/tui/model.go`, inside `readEventsCmd` (line 115), change:

```go
		lines, err := TailEvents(SessionEventsPath(home, project, name), 8)
```

to:

```go
		lines, err := control.TailEvents(control.SessionEventsPath(home, project, name), 8)
```

and add `"github.com/elliottregan/cspace/internal/control"` to `model.go`'s import block.

- [ ] **Step 8: Run the full suite**

Run: `make vet && make lint && make test`

Expected: no findings; `ok` for `internal/control`, `internal/cli`, `internal/tui`.

- [ ] **Step 9: Commit**

```bash
git add internal/control internal/tui
git commit -m "Move the event tail into internal/control and add the interactive-state read"
```

---

### Task 5: `Ports`

New code: the in-sandbox statusline's port logic (`lib/runtime/scripts/statusline.sh:189-300`) ported to Go so the host can answer the same question. Label sources, the curation rule and the internal-port skip list are ported exactly; the listener enumeration becomes an `ss -tln` exec into the sandbox; URLs use the resolver check.

**Files:**
- Create: `internal/control/ports.go`
- Test: `internal/control/ports_test.go`
- Modify: `internal/control/client.go` (`Options.ResolverInstalled`, `Client.resolverInstalled`)
- Modify: `internal/control/paths.go` (add `CloneDir`)
- Modify: `internal/control/snapshot_test.go` (make `fakeContainers.Exec` record and replay)
- Modify: `internal/cli/cmd_dns.go:23,25` (alias the control constants instead of re-declaring the literals)

**Interfaces:**
- Consumes: `control.Client`, `c.containers`, `c.entries`, `c.home`, `containerName` (Task 1); `SessionDir`'s sibling layout (Task 4).
- Produces:
  - `const control.DNSDomain = "cspace.test"`, `const control.ResolverFile = "/etc/resolver/cspace.test"`
  - `type Port struct{ Port int; Label string; URL string }`
  - `func (c *Client) Ports(ctx context.Context, project, sandbox string) ([]Port, error)`
  - `func CloneDir(home, project, sandbox string) string`
  - `Options.ResolverInstalled func() bool`
  - Unexported: `parseListeningPorts`, `portLabelsFrom`, `curatePorts`, `portURL`

- [ ] **Step 1: Write the failing tests**

Create `internal/control/ports_test.go`:

```go
package control

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elliottregan/cspace/internal/registry"
)

func TestParseListeningPorts(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []int
	}{
		{
			name: "header is skipped and the bind address ignored",
			// The entrypoint's PREROUTING DNAT rewrites inbound traffic to
			// 127.0.0.1, so a loopback-only listener is still reachable from
			// outside the microVM — the bind address must not filter.
			out: "State  Recv-Q Send-Q Local Address:Port  Peer Address:Port\n" +
				"LISTEN 0      511          0.0.0.0:5173         0.0.0.0:*\n" +
				"LISTEN 0      4096       127.0.0.1:6201         0.0.0.0:*\n",
			want: []int{5173, 6201},
		},
		{
			name: "IPv6 brackets parse and duplicates collapse",
			out: "LISTEN 0 511    [::]:3000    [::]:*\n" +
				"LISTEN 0 511 0.0.0.0:3000 0.0.0.0:*\n",
			want: []int{3000},
		},
		{
			name: "non-LISTEN and short lines are ignored",
			out:  "ESTAB 0 0 10.0.0.1:5173 10.0.0.2:5555\nrubbish\n\n",
			want: nil,
		},
		{
			name: "output is sorted ascending",
			out: "LISTEN 0 511 *:8080 *:*\n" +
				"LISTEN 0 511 *:3000 *:*\n",
			want: []int{3000, 8080},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseListeningPorts(tc.out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseListeningPorts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPortLabelsFrom(t *testing.T) {
	writeClone := func(t *testing.T, devcontainerJSON, cspaceJSON string) string {
		t.Helper()
		dir := t.TempDir()
		if devcontainerJSON != "" {
			if err := os.MkdirAll(filepath.Join(dir, ".devcontainer"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".devcontainer", "devcontainer.json"), []byte(devcontainerJSON), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if cspaceJSON != "" {
			if err := os.WriteFile(filepath.Join(dir, ".cspace.json"), []byte(cspaceJSON), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	cases := []struct {
		name         string
		devcontainer string
		cspace       string
		want         map[int]string
	}{
		{
			name: "devcontainer portsAttributes wins, JSONC comments and all",
			devcontainer: `{
				// the dev server
				"name": "demo",
				"portsAttributes": {"5173": {"label": "dev"}, "4173": {"label": "preview"}}
			}`,
			cspace: `{"container":{"ports":{"9999":"ignored"}}}`,
			want:   map[int]string{5173: "dev", 4173: "preview"},
		},
		{
			name:         "entries without a label are dropped",
			devcontainer: `{"portsAttributes": {"5173": {"label": "dev"}, "24678": {"onAutoForward": "silent"}}}`,
			want:         map[int]string{5173: "dev"},
		},
		{
			name:   "falls back to .cspace.json container.ports",
			cspace: `{"container":{"ports":{"3000":"api","5173":"dev"}}}`,
			want:   map[int]string{3000: "api", 5173: "dev"},
		},
		{
			name:         "an empty portsAttributes still falls back",
			devcontainer: `{"portsAttributes": {}}`,
			cspace:       `{"container":{"ports":{"3000":"api"}}}`,
			want:         map[int]string{3000: "api"},
		},
		{
			name: "no sources at all yields no labels",
			want: map[int]string{},
		},
		{
			name:   "an empty ports object yields no labels",
			cspace: `{"container":{"ports":{}}}`,
			want:   map[int]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := portLabelsFrom(writeClone(t, tc.devcontainer, tc.cspace))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("portLabelsFrom = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCuratePorts(t *testing.T) {
	cases := []struct {
		name      string
		listening []int
		labels    map[int]string
		want      []Port
	}{
		{
			// The curation gate: labels present means the project told us what
			// it cares about, so everything unlabeled is noise.
			name:      "labels hide unlabeled ports",
			listening: []int{3000, 5173, 24678},
			labels:    map[int]string{5173: "dev"},
			want:      []Port{{Port: 5173, Label: "dev"}},
		},
		{
			// No labels is no signal, so show everything rather than nothing.
			name:      "no labels shows every listener",
			listening: []int{3000, 5173},
			labels:    map[int]string{},
			want:      []Port{{Port: 3000}, {Port: 5173}},
		},
		{
			// 6201 is the supervisor control port, 53 the dnsmasq forwarder:
			// cspace plumbing, never a user's dev server, even when labeled.
			name:      "cspace-internal ports never show",
			listening: []int{53, 5173, 6201},
			labels:    map[int]string{6201: "control", 5173: "dev"},
			want:      []Port{{Port: 5173, Label: "dev"}},
		},
		{
			name:      "output is sorted ascending",
			listening: []int{8080, 3000},
			labels:    map[int]string{},
			want:      []Port{{Port: 3000}, {Port: 8080}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := curatePorts(tc.listening, tc.labels)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("curatePorts = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPortURL(t *testing.T) {
	cases := []struct {
		name     string
		resolver bool
		want     string
	}{
		{"resolver installed uses the project-qualified name", true, "http://mercury.alpha.cspace.test:5173/"},
		{"no resolver falls back to the sandbox IP", false, "http://10.0.0.1:5173/"},
	}
	for _, tc := range cases {
		if got := portURL("Alpha", "Mercury", "10.0.0.1", 5173, tc.resolver); got != tc.want {
			t.Errorf("%s: portURL = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClientPortsEndToEnd(t *testing.T) {
	home := t.TempDir()
	clone := CloneDir(home, "alpha", "mercury")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".cspace.json"),
		[]byte(`{"container":{"ports":{"5173":"dev"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{
		Project: "alpha", Name: "mercury", IP: "10.0.0.1", State: "ready",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	fc := &fakeContainers{execOut: "State Recv-Q Send-Q Local Address:Port Peer Address:Port\n" +
		"LISTEN 0 511    0.0.0.0:5173  0.0.0.0:*\n" +
		"LISTEN 0 511    0.0.0.0:24678 0.0.0.0:*\n" +
		"LISTEN 0 4096 127.0.0.1:6201  0.0.0.0:*\n"}
	c := New(Options{
		Containers:        fc,
		Entries:           reg,
		Home:              home,
		ResolverInstalled: func() bool { return true },
	})

	got, err := c.Ports(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("Ports: %v", err)
	}
	want := []Port{{Port: 5173, Label: "dev", URL: "http://mercury.alpha.cspace.test:5173/"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ports = %+v, want %+v", got, want)
	}
	// The listener enumeration must run inside the sandbox's own container.
	wantExec := execCall{name: "cspace-alpha-mercury", cmd: []string{"ss", "-tln"}}
	if len(fc.execCalls) != 1 || !reflect.DeepEqual(fc.execCalls[0], wantExec) {
		t.Errorf("exec calls = %+v, want exactly %+v", fc.execCalls, wantExec)
	}
}

func TestClientPortsSurfacesExecFailure(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	if err := reg.Register(registry.Entry{Project: "alpha", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c := New(Options{
		Containers: &fakeContainers{execExit: 127, execStderr: "ss: command not found"},
		Entries:    reg,
		Home:       t.TempDir(),
	})
	if _, err := c.Ports(context.Background(), "alpha", "mercury"); err == nil {
		t.Error("a non-zero ss exit must surface as an error")
	}
}
```

- [ ] **Step 2: Extend the shared container fake**

In `internal/control/snapshot_test.go`, replace the `fakeContainers` struct and its `Exec` method with the recording version:

```go
// execCall is one recorded Exec, so a test can assert both the container it
// targeted and the argv it ran.
type execCall struct {
	name string
	cmd  []string
}

// fakeContainers is the ContainerCLI seam: canned results so control's tests
// never shell out to the real `container` CLI.
type fakeContainers struct {
	out      []applecontainer.ContainerSummary
	err      error
	stats    []applecontainer.ContainerStats
	statsErr error

	execOut    string
	execStderr string
	execExit   int
	execErr    error
	execCalls  []execCall
}

func (f *fakeContainers) Exec(_ context.Context, name string, cmd []string, _ substrate.ExecOpts) (substrate.ExecResult, error) {
	f.execCalls = append(f.execCalls, execCall{name: name, cmd: cmd})
	return substrate.ExecResult{Stdout: f.execOut, Stderr: f.execStderr, ExitCode: f.execExit}, f.execErr
}
```

`List` and `Stats` are unchanged.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/control/... -run 'TestParseListeningPorts|TestPortLabelsFrom|TestCuratePorts|TestPortURL|TestClientPorts' -v`

Expected: build failure listing `undefined: parseListeningPorts`, `undefined: portLabelsFrom`, `undefined: curatePorts`, `undefined: Port`, `undefined: portURL`, `undefined: CloneDir`, `unknown field ResolverInstalled in struct literal of type Options`, ending in `FAIL github.com/elliottregan/cspace/internal/control [build failed]`.

- [ ] **Step 4: Add the clone path and the resolver seam**

Append to `internal/control/paths.go`:

```go
// CloneDir is a sandbox's own git clone on the host — the tree bind-mounted
// into the container as /workspace. Project configuration is read from here,
// never from the caller's cwd: a control-plane window shows sandboxes from
// several projects at once, and `cspace down` already learned this lesson.
func CloneDir(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "clones", project, sandbox)
}
```

In `internal/control/client.go`, add to `Options` (after `Home`):

```go
	// ResolverInstalled reports whether macOS routes *.cspace.test at the
	// cspace daemon. Injected so tests don't depend on the host's /etc; nil
	// means "check ResolverFile".
	ResolverInstalled func() bool
```

add to `Client`:

```go
	resolverInstalled func() bool
```

and in `New`, before the return:

```go
	resolver := o.ResolverInstalled
	if resolver == nil {
		resolver = func() bool {
			_, err := os.Stat(ResolverFile)
			return err == nil
		}
	}
```

with `resolverInstalled: resolver,` added to the struct literal and `"os"` added to the imports.

- [ ] **Step 5: Implement Ports**

Create `internal/control/ports.go`:

```go
package control

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/elliottregan/cspace/internal/devcontainer"
	"github.com/elliottregan/cspace/internal/substrate"
)

// DNSDomain is the suffix cspace's daemon answers DNS queries for; each
// sandbox is reachable at http://<sandbox>.<project>.cspace.test:<port>/.
// ResolverFile is the macOS resolver stanza `sudo cspace dns install` writes;
// its presence is what makes those names resolve on the host.
const (
	DNSDomain    = "cspace.test"
	ResolverFile = "/etc/resolver/" + DNSDomain
)

// Port is one listening TCP port inside a sandbox, with the project's label
// for it (empty when the project declared none) and the URL to reach it.
type Port struct {
	Port  int
	Label string
	URL   string
}

// internalPorts are cspace's own plumbing and never show: 6201 is the
// supervisor's control port, 53 the in-sandbox dnsmasq forwarder for
// *.cspace.test. Mirrors INTERNAL_PORTS in lib/runtime/scripts/statusline.sh.
var internalPorts = map[int]bool{6201: true, 53: true}

// Ports lists the sandbox's labeled listeners, as the in-sandbox statusline
// does, but from the host: labels from the sandbox's own workspace clone,
// live listeners from an `ss -tln` exec, the statusline's curation rule, and
// a URL per port.
func (c *Client) Ports(ctx context.Context, project, sandbox string) ([]Port, error) {
	entry, err := c.lookup(project, sandbox)
	if err != nil {
		return nil, err
	}
	res, err := c.containers.Exec(ctx, containerName(project, sandbox),
		[]string{"ss", "-tln"}, substrate.ExecOpts{})
	if err != nil {
		return nil, fmt.Errorf("list listeners in %s: %w", sandbox, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list listeners in %s: ss exited %d: %s",
			sandbox, res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	labels := portLabelsFrom(CloneDir(c.home, project, sandbox))
	ports := curatePorts(parseListeningPorts(res.Stdout), labels)
	resolver := c.resolverInstalled()
	for i := range ports {
		ports[i].URL = portURL(project, sandbox, entry.IP, ports[i].Port, resolver)
	}
	return ports, nil
}

// parseListeningPorts extracts the listening TCP ports from `ss -tln` output:
//
//	State  Recv-Q Send-Q Local Address:Port  Peer Address:Port
//	LISTEN 0      511          0.0.0.0:5173         0.0.0.0:*
//	LISTEN 0      511             [::]:3000            [::]:*
//
// The bind address is deliberately ignored: the entrypoint's PREROUTING DNAT
// rewrites inbound traffic to 127.0.0.1, so a loopback-only listener (Vite's
// default) is still reachable from outside the microVM. Ports are deduped and
// returned ascending. Unlike statusline.sh's `NR>1` awk, this matches on the
// LISTEN state word, which skips the header without assuming it is line one.
func parseListeningPorts(ssOutput string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, line := range strings.Split(ssOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "LISTEN" {
			continue
		}
		addr := fields[3]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		p, err := strconv.Atoi(addr[i+1:])
		if err != nil || seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// portLabelsFrom resolves a project's port→label map from a sandbox's
// workspace clone, using the same two sources and the same precedence as the
// in-sandbox statusline: devcontainer.json's portsAttributes first (the
// standard format, which survives a future deprecation of .cspace.json's
// container block), falling back to .cspace.json's container.ports only when
// portsAttributes produced no labels at all.
//
// Unreadable or malformed files yield no labels rather than an error: a
// project whose config cannot be parsed has, as far as this query is
// concerned, declared nothing.
func portLabelsFrom(cloneDir string) map[int]string {
	labels := map[int]string{}
	if cfg, err := devcontainer.Load(filepath.Join(cloneDir, ".devcontainer", "devcontainer.json")); err == nil {
		for key, attr := range cfg.PortsAttributes {
			if attr.Label == "" {
				continue
			}
			if p, err := strconv.Atoi(key); err == nil {
				labels[p] = attr.Label
			}
		}
	}
	if len(labels) > 0 {
		return labels
	}

	data, err := os.ReadFile(filepath.Join(cloneDir, ".cspace.json"))
	if err != nil {
		return labels
	}
	var doc struct {
		Container struct {
			Ports map[string]string `json:"ports"`
		} `json:"container"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return labels
	}
	for key, label := range doc.Container.Ports {
		if label == "" {
			continue
		}
		if p, err := strconv.Atoi(key); err == nil {
			labels[p] = label
		}
	}
	return labels
}

// curatePorts applies the statusline's curation rule. cspace's own plumbing
// never shows. Unlabeled ports are hidden ONLY when the project actually
// declared labels — that is the user's explicit "these are the URLs I care
// about" signal. An empty ports object is no signal at all, so with no labels
// every listener shows, noise included. Output is sorted ascending.
func curatePorts(listening []int, labels map[int]string) []Port {
	curate := len(labels) > 0
	out := make([]Port, 0, len(listening))
	for _, p := range listening {
		if internalPorts[p] {
			continue
		}
		label := labels[p]
		if curate && label == "" {
			continue
		}
		out = append(out, Port{Port: p, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// portURL renders one port's address. With the resolver installed the host
// can reach the project-qualified DNS name the daemon answers for, which
// survives the sandbox moving to a new vmnet IP; without it, only the raw IP
// works. Both labels are lowercased to match the daemon's lowercased,
// case-sensitive comparison.
func portURL(project, sandbox, ip string, port int, resolverInstalled bool) string {
	if resolverInstalled {
		return fmt.Sprintf("http://%s.%s.%s:%d/",
			strings.ToLower(sandbox), strings.ToLower(project), DNSDomain, port)
	}
	return fmt.Sprintf("http://%s:%d/", ip, port)
}
```

`curatePorts` returns a non-nil empty slice when nothing survives; `TestCuratePorts` never exercises that case, and `Ports` returning `[]Port{}` rather than nil is the friendlier contract for a renderer.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/control/... -run 'TestParseListeningPorts|TestPortLabelsFrom|TestCuratePorts|TestPortURL|TestClientPorts' -v`

Expected: `--- PASS` for `TestParseListeningPorts` (4 subtests), `TestPortLabelsFrom` (6 subtests), `TestCuratePorts` (4 subtests), `TestPortURL`, `TestClientPortsEndToEnd` and `TestClientPortsSurfacesExecFailure`, then `ok github.com/elliottregan/cspace/internal/control`.

- [ ] **Step 7: Point the CLI's DNS constants at control's**

Two packages now need the same two strings. Rather than let them drift, in `internal/cli/cmd_dns.go` change lines 23 and 25 inside the existing `const` block from:

```go
	dnsResolverFile = "/etc/resolver/cspace.test"
	dnsLocalPort    = "5354"
	dnsDomain       = "cspace.test"
```

to:

```go
	dnsResolverFile = control.ResolverFile
	dnsLocalPort    = "5354"
	dnsDomain       = control.DNSDomain
```

and add `"github.com/elliottregan/cspace/internal/control"` to that file's imports. The values are byte-identical, so nothing else changes.

- [ ] **Step 8: Run the full suite**

Run: `make vet && make lint && make test`

Expected: no findings; `ok` for `internal/control`, `internal/cli`, `internal/tui`.

- [ ] **Step 9: Commit**

```bash
git add internal/control internal/cli/cmd_dns.go
git commit -m "Add the Ports query, porting the statusline's label and curation rules to Go"
```

---

### Task 6: `Up`, `ListClients` and `DetachClient`

The last three actions the spec names. `Up` shells out to the running cspace binary; the two tmux calls are one-line delegations to the `Tmux` driver the sandbox-side plan landed in `tmux.go`, and are what step 4's detach protocol will build on.

**Files:**
- Modify: `internal/control/tmux.go` (append the two Client delegations; the file, the `Execer` seam and the `Tmux` driver belong to the sandbox-side plan — do not touch what is already in it)
- Create: `internal/control/up.go`
- Test: `internal/control/tmux_client_test.go`
- Test: `internal/control/up_test.go`
- Modify: `internal/control/client.go` (`Options.ProjectRoot`, the `executable` / `runCommand` seams)

**Interfaces:**
- Consumes: `control.Client`, `c.containers`, `c.tmux`, `containerName`, `containerExecer` (Task 1); `fakeContainers` with `execCalls` (Task 5); the sandbox-side plan's `SessionClaude` / `SessionShell` constants and its `(*Tmux).ListClients` / `(*Tmux).DetachClient`.
- Produces:
  - (session names: consume the sandbox-side plan's `SessionClaude` / `SessionShell`; do not redeclare them)
  - `func (c *Client) ListClients(ctx context.Context, project, sandbox, session string) ([]string, error)` — delegates to `(*Tmux).ListClients`
  - `func (c *Client) DetachClient(ctx context.Context, project, sandbox, tty string) error` — delegates to `(*Tmux).DetachClient`
  - `Options.Tmux *Tmux`, `containerExecer` (the ContainerCLI→Execer adapter) — both added in Task 1 Step 6; Task 6 is what uses them
  - `func (c *Client) Up(ctx context.Context, sandbox string) error`
  - `Options.ProjectRoot string`

- [ ] **Step 1: Write the failing tests**

Create `internal/control/tmux_client_test.go`:

```go
package control

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// The driver itself is covered by tmux_test.go (sandbox-side plan). These two
// cover only the Client's delegation: that it targets the right container and
// carries the driver's failure out.
func TestClientListClientsDelegatesToTheTmuxDriver(t *testing.T) {
	fc := &fakeContainers{execOut: "/dev/ttys004\n"}
	c := New(Options{Containers: fc})

	got, err := c.ListClients(context.Background(), "alpha", "mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"/dev/ttys004"}) {
		t.Errorf("clients = %+v, want [/dev/ttys004]", got)
	}
	want := execCall{name: "cspace-alpha-mercury", cmd: []string{"tmux", "list-clients", "-t", SessionClaude, "-F", "#{client_tty}"}}
	if len(fc.execCalls) != 1 || !reflect.DeepEqual(fc.execCalls[0], want) {
		t.Errorf("exec calls = %+v, want exactly %+v", fc.execCalls, want)
	}
}

func TestClientDetachClientSurfacesTmuxFailure(t *testing.T) {
	// The Execer seam reports a non-zero exit as a code, not stderr, so the
	// error names the exit status.
	c := New(Options{Containers: &fakeContainers{execExit: 1, execStderr: "can't find client /dev/ttys009"}})
	err := c.DetachClient(context.Background(), "alpha", "mercury", "/dev/ttys009")
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("err = %v, want the driver's non-zero exit surfaced", err)
	}
}
```

Create `internal/control/up_test.go`:

```go
package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestUpRunsThisBinaryInTheProjectRoot(t *testing.T) {
	var gotDir, gotBin string
	var gotArgs []string
	c := New(Options{Containers: &fakeContainers{}, ProjectRoot: "/Users/x/proj"})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(_ context.Context, dir, bin string, args ...string) (string, error) {
		gotDir, gotBin, gotArgs = dir, bin, args
		return "sandbox issue-42 up\n", nil
	}

	if err := c.Up(context.Background(), "issue-42"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if gotDir != "/Users/x/proj" || gotBin != "/usr/local/bin/cspace" {
		t.Errorf("ran %q in %q", gotBin, gotDir)
	}
	if want := []string{"up", "issue-42"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestUpSurfacesFailureWithOutput(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { return "/usr/local/bin/cspace", nil }
	c.runCommand = func(context.Context, string, string, ...string) (string, error) {
		return "error: sandbox issue-42 already exists\n", errors.New("exit status 1")
	}
	err := c.Up(context.Background(), "issue-42")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want the command's output carried through", err)
	}
}

func TestUpReportsAnUnresolvableBinary(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})
	c.executable = func() (string, error) { return "", errors.New("no such file") }
	if err := c.Up(context.Background(), "issue-42"); err == nil {
		t.Error("want an error when the running binary cannot be resolved")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/control/... -run 'TestClientListClients|TestClientDetachClient|TestUp' -v`

Expected: build failure listing `c.ListClients undefined`, `c.DetachClient undefined`, `c.Up undefined`, `c.executable undefined`, `c.runCommand undefined`, `unknown field ProjectRoot in struct literal of type Options`, ending in `FAIL github.com/elliottregan/cspace/internal/control [build failed]`.

- [ ] **Step 3: Add the process seams to the Client**

In `internal/control/client.go`, add to `Options` (after `ResolverInstalled`):

```go
	// ProjectRoot is the working directory `cspace up` runs in — the tree it
	// reads .cspace.json and .devcontainer from.
	ProjectRoot string
```

add to `Client`:

```go
	projectRoot string

	// executable and runCommand are the process seams Up uses, fields so a
	// test drives it without spawning anything.
	executable func() (string, error)
	runCommand func(ctx context.Context, dir, bin string, args ...string) (string, error)
```

and in `New`'s struct literal:

```go
		projectRoot:   o.ProjectRoot,
		executable:    os.Executable,
		runCommand:    runHostCommand,
```

- [ ] **Step 4: Add the Client's tmux delegations**

Append to `internal/control/tmux.go` (created by the sandbox-side plan; leave its existing contents alone):

```go
// ListClients reports the ttys attached to one of the sandbox's tmux
// sessions. It delegates to the Tmux driver so the package keeps one
// implementation of the tmux plumbing; no server at all is not an error.
func (c *Client) ListClients(ctx context.Context, project, sandbox, session string) ([]string, error) {
	return c.tmux.ListClients(ctx, containerName(project, sandbox), session)
}

// DetachClient detaches one tmux client by its tty. A dead host side never
// reaches the guest, so every pane close and every `cspace attach` exit must
// detach explicitly; this is the Client-level entry to that call.
func (c *Client) DetachClient(ctx context.Context, project, sandbox, tty string) error {
	return c.tmux.DetachClient(ctx, containerName(project, sandbox), tty)
}
```

`tmux.go` already imports `context`, so its import block is unchanged.

- [ ] **Step 5: Implement Up**

Create `internal/control/up.go`:

```go
package control

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Up boots a sandbox by running this same cspace binary's `up` command from
// the project's root.
//
// `cspace up` is a cobra command wrapping a long boot flow and a Bubble Tea
// overlay, not a callable function, and internal/control must not import
// internal/cli — so control shells out to the binary it is already running
// inside, exactly as a person would. Everything the boot flow needs (config,
// devcontainer, compose) it reads from Options.ProjectRoot.
func (c *Client) Up(ctx context.Context, sandbox string) error {
	exe, err := c.executable()
	if err != nil {
		return fmt.Errorf("resolve the running cspace binary: %w", err)
	}
	out, err := c.runCommand(ctx, c.projectRoot, exe, "up", sandbox)
	if err != nil {
		return fmt.Errorf("cspace up %s: %w (%s)", sandbox, err, strings.TrimSpace(out))
	}
	return nil
}

// runHostCommand runs bin in dir and returns its combined output. The default
// for Client.runCommand.
func runHostCommand(ctx context.Context, dir, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/control/... -run 'TestClientListClients|TestClientDetachClient|TestUp' -v`

Expected: `--- PASS` for `TestClientListClientsDelegatesToTheTmuxDriver`, `TestClientDetachClientSurfacesTmuxFailure`, `TestUpRunsThisBinaryInTheProjectRoot`, `TestUpSurfacesFailureWithOutput` and `TestUpReportsAnUnresolvableBinary`, then `ok github.com/elliottregan/cspace/internal/control`.

- [ ] **Step 7: Run the whole control package and the full suite**

Run: `go test ./internal/control/... -v`

Expected: every test above passes; `ok github.com/elliottregan/cspace/internal/control`.

Run: `make vet && make lint && make test`

Expected: no findings; `ok` for every package with tests.

- [ ] **Step 8: Confirm `cspace tui` still builds and registers**

Run: `make build && ./bin/cspace-go tui --help`

Expected: the build succeeds and the help text prints `Full-screen dashboard of cspace containers with common actions` with an `--interval` flag defaulting to `2s`. Do not run `cspace up`.

- [ ] **Step 9: Commit**

```bash
git add internal/control
git commit -m "Add the up action and the Client tmux delegations to internal/control"
```

---

## Notes for the executor

- **Do not touch `internal/cli/cmd_ports.go`, `cmd_agent.go`'s command bodies, or `cmd_send.go`.** They keep their current implementations. `cmd_send.go`'s `resolveEntry` is dual-context (in-sandbox it queries the daemon over `CSPACE_REGISTRY_URL`); repointing it at `control.Send` needs an `EntryStore` that wraps that path, and that belongs to a later step. `control.EntryStore` is an interface precisely so this is cheap later.
- **`cspace ports` and `control.Ports` deliberately answer different questions** for now: the command TCP-probes a static well-known port list from the host, the query enumerates real listeners from inside the sandbox. Converging them is out of scope here.
- **The sibling step-1 plan lands first and owns `control.go`, `argv.go`, `tmux.go`, `attach.go` and their tests.** Leave them as they are, skip the "create the package directory" half of Task 1 Step 1, and do not add a second package doc comment, a second set of session-name constants, or a second exec seam. `tmux.go` is appended to (the two Client delegations), never rewritten.
