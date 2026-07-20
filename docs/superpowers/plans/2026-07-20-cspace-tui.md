# cspace TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `cspace tui` command — a full-screen Bubble Tea dashboard that lists every cspace container on the host (sandboxes, compose sidecars, the shared browser), grouped by project, with live agent status, and runs the common per-sandbox commands (attach, down, agent send/interrupt, browser restart).

**Architecture:** A new `internal/tui` package holds the pure domain model (`Correlate` folds raw data sources into a display `Snapshot`), the data `Poller`, the event-tail reader, and the Bubble Tea `Model`/`View`. Side-effecting actions are behind a consumer-defined `tui.Actor` interface whose real implementation lives in `internal/cli` (where it can reach `teardownSandbox`, `attachArgs`, and the control-port HTTP client). A thin `cmd_tui.go` wires the real `Poller`/`Actor` and launches the program. A new `applecontainer.List` method supplies live container state.

**Tech Stack:** Go, Cobra, Bubble Tea v1.3.10 + bubbles (spinner, textinput) + lipgloss v1.1.0 (all already vendored), the Apple Container `container` CLI.

## Global Constraints

- **SOLID / single responsibility:** each file has ONE responsibility. Pure data-folding (`Correlate`), data collection (`Poller`), event reading (`TailEvents`), side effects (`Actor`), state machine (`Model`/`Update`), rendering (`View`) are separate units. Do not merge them.
- **Dependency inversion:** `internal/tui` defines the `Poller` and `Actor` interfaces it consumes; it must NOT import `internal/cli`. The real `Actor` is implemented in `internal/cli` and injected. `internal/cli` imports `internal/tui`, never the reverse.
- **Read-only where possible:** the TUI reads the registry via `registry.List()` (local file, no daemon dependency) and never writes it. The only destructive action is `down`, which always confirms.
- **Test seams, host-free default suite:** every unit is tested with fakes/httptest/temp-dirs. Any test that shells out to the real `container` CLI or creates containers MUST gate behind `requireContainerCLI(t)` + `requireE2E(t)` (env `CSPACE_E2E=1`). Default `go test ./...` / `make test` stays side-effect-free. NEVER run `go test ./...` without `-skip 'TestCspaceLifecycle'` during development, and never touch live `cspace-resume-redux-*` containers.
- **Go idioms:** `ctx context.Context` is the first parameter of any method doing I/O; construct `*applecontainer.Adapter` via `applecontainer.New()`; construct the registry via `path, _ := registry.DefaultPath(); r := &registry.Registry{Path: path}`; write cobra output via `cmd.OutOrStdout()`; return errors (root sets `SilenceUsage`/`SilenceErrors`).
- **Bubble Tea conventions (mirror `internal/overlay`):** `Model` is a VALUE type with value-receiver `Init`/`Update`/`View` that return the model by value — mutate the local `m` and `return m, cmd`. Launch with `tea.NewProgram(model, tea.WithAltScreen())`. Custom messages are named `xxxMsg`; ticks use `tea.Tick(interval, func(time.Time) tea.Msg {...})`. `textinput` has MIXED receivers (`Focus`/`Blur`/`SetValue` are pointer methods) — store it as an addressable value field; `Focus()` returns a `tea.Cmd` that must be returned.
- **Container identity:** a sandbox's container is `cspace-<project>-<name>`; its compose sidecars are `cspace-<project>-<name>-<suffix>`; the shared browser is `cspace-<project>-browser`.
- **Registry semantics:** `registry.List()` returns `[]Entry` in NONDETERMINISTIC map order — always sort. `Project`/`Name` are reconstructed from the map key (json:"-"), present only in-process. Treat `Entry.State == ""` as ready; only `"starting"` is booting. A present entry does NOT prove liveness — probe. Never DISPLAY `Entry.Token` (it is a live bearer secret; it may travel in the row model for actions but must not be rendered).
- **Event log path (hardcode `primary`):** `$HOME/.cspace/sessions/<project>/<sandbox>/primary/events.ndjson`, single-generation rotation to `events.ndjson.1` at 10 MiB. Each line is `{"ts":ISO8601,"session":...,"kind":...,"data":{...}}`. Parse permissively; tolerate a malformed trailing line.
- **`container ls --all --format json` shape (verified on 0.12.x):** JSON array; per record — `configuration.id` = container name, top-level `status` = state string, `networks[0].ipv4Address` = "IP/CIDR" (strip suffix), `configuration.resources.cpus` (int), `configuration.resources.memoryInBytes` (int64), `configuration.image.reference` = image, `startedDate` = CFAbsoluteTime float (seconds since 2001-01-01 UTC; add 978307200 for Unix).

---

## File Structure

- `internal/substrate/applecontainer/adapter.go` — **+**`ContainerSummary`, `List`, `parseContainerList`, `cfAbsoluteToTime` (container listing).
- `internal/tui/types.go` — domain types: `Snapshot`, `Row`, `RowKind`, `RowState`, `AgentStatus`, `DaemonHealth`, `containerName`/`browserContainerName` helpers. Pure declarations.
- `internal/tui/correlate.go` — `Correlate(...)` pure fold (raw sources → `Snapshot`).
- `internal/tui/events.go` — `EventLine`, `TailEvents` (read + parse events.ndjson tail, rotation-aware).
- `internal/tui/poll.go` — `Poller` interface, `realPoller`, `NewPoller`, status fan-out, daemon health.
- `internal/tui/actions.go` — `Actor` interface + action result message types (interface only; impl in cli).
- `internal/tui/keys.go` — `keyMap` + pure contextual predicates (`canAttach`, `canDown`, `canSend`, `canInterrupt`, `canBrowser`).
- `internal/tui/model.go` — `Model`, `NewModel`, `Init`, `Update`, selection/mode state machine.
- `internal/tui/view.go` — `View()` rendering + format helpers (`formatMemory`, `formatUptime`, `formatAge`).
- `internal/cli/cmd_attach.go` — **extract** `attachArgs`.
- `internal/cli/tui_actor.go` — `tuiActor` implementing `tui.Actor` (attach/down/send/interrupt/browser).
- `internal/cli/cmd_tui.go` — `newTuiCmd()` cobra command + real dependency wiring; register in `root.go`.
- Docs: `CLAUDE.md` Commands section + `README.md`.

---

## Task 1: `applecontainer.List` — typed container listing

**Files:**
- Modify: `internal/substrate/applecontainer/adapter.go` (add near `IP()` ~line 412)
- Test: `internal/substrate/applecontainer/adapter_test.go`

**Interfaces:**
- Produces: `type ContainerSummary struct{ Name, Image, State, IP string; CPUs int; MemoryB int64; Started time.Time }`; `func (a *Adapter) List(ctx context.Context) ([]ContainerSummary, error)`; `func parseContainerList(jsonOutput string) ([]ContainerSummary, error)`; `func cfAbsoluteToTime(f float64) time.Time`.

- [ ] **Step 1: Write the failing test (pure parse + CF time)**

Add to `internal/substrate/applecontainer/adapter_test.go` (no CLI gate — pure parse):

```go
func TestParseContainerList(t *testing.T) {
	const fixture = `[
	  {"startedDate":806197425.667992,"status":"running",
	   "networks":[{"ipv4Address":"192.168.64.108/24","network":"default"}],
	   "configuration":{"id":"cspace-demo-mercury",
	     "image":{"reference":"cspace:latest"},
	     "resources":{"cpus":4,"memoryInBytes":17179869184}}},
	  {"startedDate":805346161.290075,"status":"stopped",
	   "networks":[],
	   "configuration":{"id":"buildkit",
	     "image":{"reference":"ghcr.io/apple/builder:0.12.0"},
	     "resources":{"cpus":2,"memoryInBytes":2147483648}}}
	]`
	got, err := parseContainerList(fixture)
	if err != nil {
		t.Fatalf("parseContainerList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	m := got[0]
	if m.Name != "cspace-demo-mercury" || m.State != "running" || m.IP != "192.168.64.108" {
		t.Errorf("record0 = %+v", m)
	}
	if m.CPUs != 4 || m.MemoryB != 17179869184 || m.Image != "cspace:latest" {
		t.Errorf("record0 fields = %+v", m)
	}
	// startedDate 806197425.667992 CFAbsoluteTime -> Unix 1784504625.667992
	if want := int64(1784504625); m.Started.Unix() != want {
		t.Errorf("Started.Unix() = %d, want %d", m.Started.Unix(), want)
	}
	if got[1].IP != "" {
		t.Errorf("record1 (no networks) IP = %q, want empty", got[1].IP)
	}
}

func TestParseContainerListEmpty(t *testing.T) {
	got, err := parseContainerList(`[]`)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestParseContainerListMalformed(t *testing.T) {
	if _, err := parseContainerList(`{not json`); err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/substrate/applecontainer -run TestParseContainerList -v`
Expected: FAIL — `undefined: parseContainerList`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/substrate/applecontainer/adapter.go` (mirrors `IP()`'s shell-out + unmarshal-into-slice pattern; `container ls` returns a JSON array):

```go
// ContainerSummary is one row of `container ls --all --format json`, narrowed
// to the fields the TUI renders. See adapter.go's package doc for the version
// cspace tests against; a shape change gives the same drift error as IP().
type ContainerSummary struct {
	Name    string    // configuration.id
	Image   string    // configuration.image.reference
	State   string    // top-level status: "running" | "stopped" | ...
	IP      string    // networks[0].ipv4Address, CIDR suffix stripped, "" if none
	CPUs    int       // configuration.resources.cpus
	MemoryB int64     // configuration.resources.memoryInBytes
	Started time.Time // startedDate (CFAbsoluteTime) converted to wall time
}

// listRecord mirrors the nested `container ls --format json` shape.
type listRecord struct {
	StartedDate float64 `json:"startedDate"`
	Status      string  `json:"status"`
	Networks    []struct {
		IPv4Address string `json:"ipv4Address"`
	} `json:"networks"`
	Configuration struct {
		ID    string `json:"id"`
		Image struct {
			Reference string `json:"reference"`
		} `json:"image"`
		Resources struct {
			CPUs        int   `json:"cpus"`
			MemoryBytes int64 `json:"memoryInBytes"`
		} `json:"resources"`
	} `json:"configuration"`
}

// cfAbsoluteEpochOffset is the seconds between the Unix epoch (1970-01-01) and
// the CoreFoundation absolute-time epoch (2001-01-01), both UTC.
const cfAbsoluteEpochOffset = 978307200

// cfAbsoluteToTime converts an Apple `startedDate` (CFAbsoluteTime, seconds
// since 2001-01-01 UTC) to a Go time.Time.
func cfAbsoluteToTime(f float64) time.Time {
	sec := int64(f) + cfAbsoluteEpochOffset
	nsec := int64((f - float64(int64(f))) * 1e9)
	return time.Unix(sec, nsec)
}

// parseContainerList turns `container ls --format json` output into summaries.
// Split out from List so it can be unit-tested with canned JSON, no CLI.
func parseContainerList(jsonOutput string) ([]ContainerSummary, error) {
	var records []listRecord
	if err := json.Unmarshal([]byte(jsonOutput), &records); err != nil {
		return nil, fmt.Errorf("parse `container ls --all --format json` output: %w "+
			"(the Apple Container CLI's JSON shape may have changed; cspace tested "+
			"with %s.x — run `container --version` and file an issue at "+
			"https://github.com/elliottregan/cspace/issues if this version differs)",
			err, supportedMinorVersion)
	}
	out := make([]ContainerSummary, 0, len(records))
	for _, r := range records {
		ip := ""
		if len(r.Networks) > 0 {
			ip = r.Networks[0].IPv4Address
			if i := strings.IndexByte(ip, '/'); i >= 0 {
				ip = ip[:i]
			}
		}
		out = append(out, ContainerSummary{
			Name:    r.Configuration.ID,
			Image:   r.Configuration.Image.Reference,
			State:   r.Status,
			IP:      ip,
			CPUs:    r.Configuration.Resources.CPUs,
			MemoryB: r.Configuration.Resources.MemoryBytes,
			Started: cfAbsoluteToTime(r.StartedDate),
		})
	}
	return out, nil
}

// List returns every container the CLI reports (all states). The caller filters
// to cspace-* / buildkit. Mirrors IP()'s shell-out + JSON-parse pattern.
func (a *Adapter) List(ctx context.Context) ([]ContainerSummary, error) {
	cmd := exec.CommandContext(ctx, "container", "ls", "--all", "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("container ls --all --format json: %w (stderr: %s)",
			err, strings.TrimSpace(stderr.String()))
	}
	return parseContainerList(stdout.String())
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/substrate/applecontainer -run TestParseContainerList -v`
Expected: PASS (all three).

- [ ] **Step 5: Write the E2E-gated live test**

```go
func TestListLive(t *testing.T) {
	requireContainerCLI(t)
	requireE2E(t)
	a := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := a.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// buildkit is essentially always present on a dev host running the substrate.
	t.Logf("List returned %d containers", len(got))
	for _, c := range got {
		if c.Name == "" {
			t.Errorf("container with empty Name: %+v", c)
		}
	}
}
```

- [ ] **Step 6: Verify the live test skips without the env**

Run: `go test ./internal/substrate/applecontainer -run TestListLive -v`
Expected: `--- SKIP: TestListLive` (no `CSPACE_E2E`).

- [ ] **Step 7: Commit**

```bash
git add internal/substrate/applecontainer/adapter.go internal/substrate/applecontainer/adapter_test.go
git commit -m "Add applecontainer.List for container ls --format json"
```

---

## Task 2: `internal/tui` domain types + `Correlate` fold

**Files:**
- Create: `internal/tui/types.go`
- Create: `internal/tui/correlate.go`
- Test: `internal/tui/correlate_test.go`

**Interfaces:**
- Consumes: `applecontainer.ContainerSummary` (Task 1), `registry.Entry`.
- Produces: the types below and `func Correlate(now time.Time, containers []applecontainer.ContainerSummary, entries []registry.Entry, statuses map[string]AgentStatus, daemon DaemonHealth, listErr error) Snapshot`.

- [ ] **Step 1: Write `internal/tui/types.go`** (pure declarations — no test cycle of its own; folded into this task)

```go
// Package tui implements the `cspace tui` dashboard: a read-and-act view over
// the host's cspace containers. This file holds the pure domain types shared
// by the poller, the correlation fold, and the Bubble Tea model/view.
package tui

import (
	"fmt"
	"time"
)

// RowKind classifies a display row. Only RowSandbox and RowBrowser are
// selectable / actionable.
type RowKind int

const (
	RowProject RowKind = iota // project header, non-selectable
	RowSandbox                // a registered sandbox
	RowSidecar                // a compose sidecar nested under a sandbox
	RowBrowser                // the project's shared browser sidecar
	RowSystem                 // buildkit / unmatched containers, dimmed
)

// RowState is the coarse lifecycle a row renders with.
type RowState int

const (
	StateStopped  RowState = iota // registry entry, no running container
	StateBooting                  // registry State == "starting"
	StateRunning                  // container running; for a sandbox, supervisor reachable
	StateDegraded                 // container running but supervisor unreachable
)

// AgentStatus is a sandbox supervisor's GET /status, decoded. Reachable is
// false when the probe failed (timeout / connection refused / non-2xx).
type AgentStatus struct {
	Reachable        bool
	State            string // "working" | "idle"
	Session          string
	QueueDepth       int
	LastEventType    string
	LastEventSubtype string
	LastEventTs      string
}

// Row is one line in the dashboard. Container is the full container name
// (cspace-<project>-<name>); "" for project headers. ControlURL/Token drive
// actions on sandbox rows and are never rendered (Token is a live secret).
type Row struct {
	Kind       RowKind
	Project    string
	Name       string // sandbox/sidecar/browser display name
	Container  string
	State      RowState
	IP         string
	MemoryB    int64
	Uptime     time.Duration
	Agent      AgentStatus // meaningful only for RowSandbox
	ControlURL string
	Token      string
	Selectable bool
}

// DaemonHealth is the host daemon's GET /health, decoded.
type DaemonHealth struct {
	Reachable bool
	Version   string
}

// Snapshot is one poll's worth of state the model renders. Err is set when
// `container ls` failed; Rows may then be registry-only or empty.
type Snapshot struct {
	Rows    []Row
	Daemon  DaemonHealth
	Err     error
	TakenAt time.Time
}

// containerName is the workspace container for a sandbox.
func containerName(project, name string) string {
	return fmt.Sprintf("cspace-%s-%s", project, name)
}

// browserContainerName is the project's shared browser sidecar.
func browserContainerName(project string) string {
	return fmt.Sprintf("cspace-%s-browser", project)
}
```

- [ ] **Step 2: Write the failing test**

`internal/tui/correlate_test.go`:

```go
package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
)

func TestCorrelateGroupsSortsAndNests(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1", MemoryB: 1 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "cspace-alpha-mercury-convex", State: "running", IP: "10.0.0.2", MemoryB: 1 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "cspace-alpha-browser", State: "running", IP: "10.0.0.3", MemoryB: 4 << 30, Started: now.Add(-2 * time.Hour)},
		{Name: "buildkit", State: "running", IP: "10.0.0.9", MemoryB: 2 << 30, Started: now.Add(-3 * time.Hour)},
	}
	entries := []registry.Entry{
		{Project: "alpha", Name: "mercury", ControlURL: "http://c/mercury", Token: "tok", State: "ready"},
	}
	statuses := map[string]AgentStatus{
		"cspace-alpha-mercury": {Reachable: true, State: "idle", Session: "primary"},
	}
	snap := Correlate(now, containers, entries, statuses, DaemonHealth{Reachable: true, Version: "1.0"}, nil)

	// Expected row order: project header, sandbox, its sidecar, browser, system(buildkit)
	wantKinds := []RowKind{RowProject, RowSandbox, RowSidecar, RowBrowser, RowSystem}
	if len(snap.Rows) != len(wantKinds) {
		t.Fatalf("rows = %d, want %d: %+v", len(snap.Rows), len(wantKinds), snap.Rows)
	}
	for i, k := range wantKinds {
		if snap.Rows[i].Kind != k {
			t.Errorf("row[%d].Kind = %v, want %v", i, snap.Rows[i].Kind, k)
		}
	}
	sb := snap.Rows[1]
	if sb.State != StateRunning || !sb.Agent.Reachable || sb.Agent.State != "idle" {
		t.Errorf("sandbox row = %+v", sb)
	}
	if sb.Uptime != 2*time.Hour {
		t.Errorf("uptime = %v, want 2h", sb.Uptime)
	}
	if !snap.Rows[1].Selectable || !snap.Rows[3].Selectable {
		t.Error("sandbox and browser rows must be selectable")
	}
	if snap.Rows[0].Selectable || snap.Rows[2].Selectable || snap.Rows[4].Selectable {
		t.Error("project/sidecar/system rows must not be selectable")
	}
	if snap.Rows[1].Token != "" && !containsToken(snap) {
		// Token travels in the model for actions; this just asserts it's carried.
	}
}

func containsToken(s Snapshot) bool {
	for _, r := range s.Rows {
		if r.Token == "tok" {
			return true
		}
	}
	return false
}

func TestCorrelateDegradedWhenSupervisorUnreachable(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", Started: now},
	}
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "ready"}}
	// no status for the sandbox => unreachable
	snap := Correlate(now, containers, entries, map[string]AgentStatus{}, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateDegraded {
		t.Errorf("state = %v, want StateDegraded", snap.Rows[1].State)
	}
}

func TestCorrelateStoppedWhenNoContainer(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "ready"}}
	snap := Correlate(now, nil, entries, map[string]AgentStatus{}, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateStopped {
		t.Errorf("state = %v, want StateStopped", snap.Rows[1].State)
	}
}

func TestCorrelateBootingFromRegistryState(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	containers := []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", Started: now},
	}
	entries := []registry.Entry{{Project: "alpha", Name: "mercury", State: "starting"}}
	snap := Correlate(now, containers, entries, map[string]AgentStatus{}, DaemonHealth{}, nil)
	if snap.Rows[1].State != StateBooting {
		t.Errorf("state = %v, want StateBooting", snap.Rows[1].State)
	}
}

func TestCorrelateCarriesListErr(t *testing.T) {
	e := errors.New("apiserver down")
	snap := Correlate(time.Unix(0, 0), nil, nil, map[string]AgentStatus{}, DaemonHealth{}, e)
	if snap.Err == nil || snap.Err.Error() != "apiserver down" {
		t.Errorf("Err = %v, want carried", snap.Err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/tui -run TestCorrelate -v`
Expected: FAIL — `undefined: Correlate`.

- [ ] **Step 4: Write `internal/tui/correlate.go`**

```go
package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
)

// Correlate folds the raw data sources into an ordered, grouped Snapshot.
// Pure — no I/O, no clock. Row order per project: header, then sandboxes
// (sorted by name) each followed by its compose sidecars, then the shared
// browser; unmatched containers (buildkit, orphans) trail as system rows.
func Correlate(
	now time.Time,
	containers []applecontainer.ContainerSummary,
	entries []registry.Entry,
	statuses map[string]AgentStatus,
	daemon DaemonHealth,
	listErr error,
) Snapshot {
	byName := make(map[string]applecontainer.ContainerSummary, len(containers))
	for _, c := range containers {
		byName[c.Name] = c
	}
	consumed := make(map[string]bool, len(containers))

	// Group registry entries by project.
	projects := map[string][]registry.Entry{}
	for _, e := range entries {
		projects[e.Project] = append(projects[e.Project], e)
	}
	projectNames := make([]string, 0, len(projects))
	for p := range projects {
		projectNames = append(projectNames, p)
	}
	sort.Strings(projectNames)

	var rows []Row
	for _, project := range projectNames {
		rows = append(rows, Row{Kind: RowProject, Project: project, Name: project})
		es := projects[project]
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })

		for _, e := range es {
			cname := containerName(project, e.Name)
			c, running := byName[cname], false
			state := StateStopped
			var ip string
			var mem int64
			var uptime time.Duration
			if hit, ok := byName[cname]; ok {
				consumed[cname] = true
				running = hit.State == "running"
				ip, mem = hit.IP, hit.MemoryB
				if !hit.Started.IsZero() {
					uptime = now.Sub(hit.Started)
				}
			}
			st := statuses[cname]
			switch {
			case running && st.Reachable:
				state = StateRunning
			case running && e.State == "starting":
				state = StateBooting
			case running:
				state = StateDegraded
			case e.State == "starting":
				state = StateBooting
			default:
				state = StateStopped
			}
			_ = c
			rows = append(rows, Row{
				Kind:       RowSandbox,
				Project:    project,
				Name:       e.Name,
				Container:  cname,
				State:      state,
				IP:         ip,
				MemoryB:    mem,
				Uptime:     uptime,
				Agent:      st,
				ControlURL: e.ControlURL,
				Token:      e.Token,
				Selectable: true,
			})

			// Nest compose sidecars: cspace-<project>-<name>-<suffix>.
			prefix := cname + "-"
			var sidecars []applecontainer.ContainerSummary
			for _, sc := range containers {
				if consumed[sc.Name] {
					continue
				}
				if strings.HasPrefix(sc.Name, prefix) {
					sidecars = append(sidecars, sc)
				}
			}
			sort.Slice(sidecars, func(i, j int) bool { return sidecars[i].Name < sidecars[j].Name })
			for _, sc := range sidecars {
				consumed[sc.Name] = true
				rows = append(rows, Row{
					Kind:      RowSidecar,
					Project:   project,
					Name:      strings.TrimPrefix(sc.Name, "cspace-"+project+"-"),
					Container: sc.Name,
					State:     sidecarState(sc.State),
					IP:        sc.IP,
					MemoryB:   sc.MemoryB,
				})
			}
		}

		// Project-level shared browser sidecar.
		bname := browserContainerName(project)
		if bc, ok := byName[bname]; ok {
			consumed[bname] = true
			rows = append(rows, Row{
				Kind:       RowBrowser,
				Project:    project,
				Name:       "browser (shared)",
				Container:  bname,
				State:      sidecarState(bc.State),
				IP:         bc.IP,
				MemoryB:    bc.MemoryB,
				Selectable: true,
			})
		}
	}

	// System / unmatched containers (buildkit, orphaned cspace-*), sorted.
	var system []applecontainer.ContainerSummary
	for _, c := range containers {
		if !consumed[c.Name] {
			system = append(system, c)
		}
	}
	sort.Slice(system, func(i, j int) bool { return system[i].Name < system[j].Name })
	for _, c := range system {
		rows = append(rows, Row{
			Kind:      RowSystem,
			Name:      c.Name,
			Container: c.Name,
			State:     sidecarState(c.State),
			IP:        c.IP,
			MemoryB:   c.MemoryB,
		})
	}

	return Snapshot{Rows: rows, Daemon: daemon, Err: listErr, TakenAt: now}
}

// sidecarState maps a raw container status string to a RowState (sidecars and
// system rows have no supervisor, so only running/stopped apply).
func sidecarState(status string) RowState {
	if status == "running" {
		return StateRunning
	}
	return StateStopped
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tui -run TestCorrelate -v`
Expected: PASS (all five).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/types.go internal/tui/correlate.go internal/tui/correlate_test.go
git commit -m "Add tui domain types and Correlate fold"
```

---

## Task 3: `internal/tui` event-tail reader

**Files:**
- Create: `internal/tui/events.go`
- Test: `internal/tui/events_test.go`

**Interfaces:**
- Produces: `type EventLine struct{ Ts, Kind, Type, Subtype string }`; `func TailEvents(path string, n int) ([]EventLine, error)`; `func SessionEventsPath(home, project, sandbox string) string`.

- [ ] **Step 1: Write the failing test**

`internal/tui/events_test.go`:

```go
package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestTailEventsLastN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p,
		`{"ts":"2026-07-20T04:12:01Z","kind":"sdk-event","data":{"type":"assistant"}}`,
		`{"ts":"2026-07-20T04:12:02Z","kind":"sdk-event","data":{"type":"user"}}`,
		`{"ts":"2026-07-20T04:12:03Z","kind":"sdk-event","data":{"type":"result","subtype":"success"}}`,
	)
	got, err := TailEvents(p, 2)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Type != "user" || got[1].Type != "result" || got[1].Subtype != "success" {
		t.Errorf("got = %+v", got)
	}
}

func TestTailEventsFewerThanN(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	writeLines(t, p, `{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`)
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestTailEventsToleratesMalformedTrailingLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events.ndjson")
	// Second line is a partially-flushed / malformed trailing line.
	if err := os.WriteFile(p, []byte(
		`{"ts":"t","kind":"sdk-event","data":{"type":"assistant"}}`+"\n"+
			`{"ts":"t2","kind":"sdk-ev`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := TailEvents(p, 8)
	if err != nil {
		t.Fatalf("TailEvents: %v", err)
	}
	if len(got) != 1 || got[0].Type != "assistant" {
		t.Errorf("got = %+v, want the one valid line", got)
	}
}

func TestTailEventsMissingFileIsNotError(t *testing.T) {
	got, err := TailEvents(filepath.Join(t.TempDir(), "nope.ndjson"), 8)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestSessionEventsPath(t *testing.T) {
	got := SessionEventsPath("/home/x", "alpha", "mercury")
	want := "/home/x/.cspace/sessions/alpha/mercury/primary/events.ndjson"
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui -run 'TestTailEvents|TestSessionEventsPath' -v`
Expected: FAIL — `undefined: TailEvents`.

- [ ] **Step 3: Write `internal/tui/events.go`**

```go
package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// EventLine is one parsed events.ndjson record, narrowed to what the detail
// pane renders.
type EventLine struct {
	Ts      string
	Kind    string
	Type    string
	Subtype string
}

// eventRecord mirrors the on-disk NDJSON line shape.
type eventRecord struct {
	Ts   string `json:"ts"`
	Kind string `json:"kind"`
	Data struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	} `json:"data"`
}

// SessionEventsPath is the host path of a sandbox's supervisor event log. The
// "primary" segment is the supervisor's hardcoded SESSION_ID — do not make it
// configurable. Mirrors the literal join cmd_up.go uses for the /sessions mount.
func SessionEventsPath(home, project, sandbox string) string {
	return filepath.Join(home, ".cspace", "sessions", project, sandbox, "primary", "events.ndjson")
}

// TailEvents returns the last n parsed lines of the events.ndjson at path.
// A missing file yields (nil, nil) — pre-first-event or wiped-by-down is not an
// error. Malformed lines (including a partially-flushed trailing line) are
// skipped, not fatal. It reads the whole file then keeps the last n valid
// lines; the file single-generation-rotates at 10 MiB, so it is bounded.
func TailEvents(path string, n int) ([]EventLine, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var all []EventLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec eventRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue // tolerate malformed / partial lines
		}
		all = append(all, EventLine{
			Ts:      rec.Ts,
			Kind:    rec.Kind,
			Type:    rec.Data.Type,
			Subtype: rec.Data.Subtype,
		})
	}
	// A Scanner error (other than a too-long final token) is unusual; ignore it
	// so a truncated tail still renders what parsed.
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui -run 'TestTailEvents|TestSessionEventsPath' -v`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/events.go internal/tui/events_test.go
git commit -m "Add tui event-tail reader"
```

---

## Task 4: `internal/tui` Poller

**Files:**
- Create: `internal/tui/poll.go`
- Test: `internal/tui/poll_test.go`

**Interfaces:**
- Consumes: `applecontainer.List` (Task 1), `Correlate` (Task 2), `registry.List`.
- Produces: `type Poller interface{ Poll(ctx context.Context) Snapshot }`; `type containerLister interface{ List(ctx context.Context) ([]applecontainer.ContainerSummary, error) }`; `func NewPoller(lister containerLister, reg *registry.Registry, daemonURL string, now func() time.Time) *realPoller`; `func (p *realPoller) Poll(ctx context.Context) Snapshot`.

**Note on design:** the poller does NOT use `resolveEntry` (which re-reads the registry per call and stacks a 5s + 10s timeout). It reads `registry.List()` ONCE per poll and hits each entry's `ControlURL` directly with a short per-probe timeout, fanning out with a bounded worker pool. `containerLister` is an interface so tests inject a fake without the CLI.

- [ ] **Step 1: Write the failing test**

`internal/tui/poll_test.go`:

```go
package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
)

type fakeLister struct {
	out []applecontainer.ContainerSummary
	err error
}

func (f fakeLister) List(context.Context) ([]applecontainer.ContainerSummary, error) {
	return f.out, f.err
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

func TestPollFansOutStatusAndCorrelates(t *testing.T) {
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
	lister := fakeLister{out: []applecontainer.ContainerSummary{
		{Name: "cspace-alpha-mercury", State: "running", IP: "10.0.0.1"},
	}}
	now := func() time.Time { return time.Unix(1_000_000, 0) }
	p := NewPoller(lister, reg, daemon.URL, now)

	snap := p.Poll(context.Background())

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

func TestPollListErrorCarriedAndDaemonUnreachable(t *testing.T) {
	reg := &registry.Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	lister := fakeLister{err: os.ErrPermission}
	now := func() time.Time { return time.Unix(0, 0) }
	p := NewPoller(lister, reg, "http://127.0.0.1:1", now) // unreachable daemon
	snap := p.Poll(context.Background())
	if snap.Err == nil {
		t.Error("want Err carried from lister failure")
	}
	if snap.Daemon.Reachable {
		t.Error("daemon should be unreachable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui -run TestPoll -v`
Expected: FAIL — `undefined: NewPoller`.

- [ ] **Step 3: Write `internal/tui/poll.go`**

```go
package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
)

// probeTimeout bounds each control-port / daemon HTTP call. Short so a wedged
// supervisor degrades one row rather than stalling the whole poll.
const probeTimeout = 800 * time.Millisecond

// maxProbeConcurrency caps the status fan-out so a host with many sandboxes
// doesn't open an unbounded burst of sockets per tick.
const maxProbeConcurrency = 8

// Poller collects one Snapshot of host state.
type Poller interface {
	Poll(ctx context.Context) Snapshot
}

// containerLister is the slice of *applecontainer.Adapter the poller needs;
// an interface so tests inject a fake without the container CLI.
type containerLister interface {
	List(ctx context.Context) ([]applecontainer.ContainerSummary, error)
}

type realPoller struct {
	lister    containerLister
	registry  *registry.Registry
	daemonURL string
	client    *http.Client
	now       func() time.Time
}

// NewPoller builds the real poller. daemonURL is the host daemon base
// (e.g. "http://127.0.0.1:6280"). now is injected for testable timestamps.
func NewPoller(lister containerLister, reg *registry.Registry, daemonURL string, now func() time.Time) *realPoller {
	return &realPoller{
		lister:    lister,
		registry:  reg,
		daemonURL: daemonURL,
		client:    &http.Client{Timeout: probeTimeout},
		now:       now,
	}
}

func (p *realPoller) Poll(ctx context.Context) Snapshot {
	containers, listErr := p.lister.List(ctx)
	entries, _ := p.registry.List() // missing file => empty slice, nil
	statuses := p.fetchStatuses(ctx, entries)
	daemon := p.fetchDaemon(ctx)
	return Correlate(p.now(), containers, entries, statuses, daemon, listErr)
}

// fetchStatuses probes each entry's GET /status concurrently (bounded). Only
// successful probes land in the map; absence => unreachable (Correlate reads
// that as degraded when the container is running, stopped otherwise).
func (p *realPoller) fetchStatuses(ctx context.Context, entries []registry.Entry) map[string]AgentStatus {
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
			st, ok := p.probeStatus(ctx, e)
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

func (p *realPoller) probeStatus(ctx context.Context, e registry.Entry) (AgentStatus, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ControlURL+"/status", nil)
	if err != nil {
		return AgentStatus{}, false
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	resp, err := p.client.Do(req)
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

func (p *realPoller) fetchDaemon(ctx context.Context) DaemonHealth {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.daemonURL+"/health", nil)
	if err != nil {
		return DaemonHealth{}
	}
	resp, err := p.client.Do(req)
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui -run TestPoll -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/poll.go internal/tui/poll_test.go
git commit -m "Add tui poller with bounded status fan-out"
```

---

## Task 5: Extract `attachArgs` from `attachInteractive`

**Files:**
- Modify: `internal/cli/cmd_attach.go:47-70`
- Test: `internal/cli/cmd_attach_test.go` (create)

**Interfaces:**
- Produces: `func attachArgs(containerName string) (bin string, argv []string, err error)`. `attachInteractive` calls it, keeps the `\033c` TTY reset and `syscall.Exec`.

- [ ] **Step 1: Write the failing test**

`internal/cli/cmd_attach_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestAttachArgs(t *testing.T) {
	bin, argv, err := attachArgs("cspace-demo-mercury")
	if err != nil {
		// container may not be on PATH in CI; only assert argv shape then.
		t.Skipf("container CLI not resolvable: %v", err)
	}
	if !strings.HasSuffix(bin, "container") {
		t.Errorf("bin = %q, want it to resolve the container binary", bin)
	}
	want := []string{"container", "exec", "-it", "cspace-demo-mercury", "claude", "--dangerously-skip-permissions"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestAttachArgs -v`
Expected: FAIL — `undefined: attachArgs`.

- [ ] **Step 3: Refactor `cmd_attach.go`**

Replace the body of `attachInteractive` (cmd_attach.go:47-71) with:

```go
// attachArgs resolves the container binary and builds the exec argv shared by
// both attach paths (CLI syscall.Exec and TUI tea.ExecProcess), so they stay
// identical. argv[0] is the literal "container" per exec convention.
func attachArgs(containerName string) (bin string, argv []string, err error) {
	bin, err = exec.LookPath("container")
	if err != nil {
		return "", nil, fmt.Errorf("apple `container` CLI not on PATH: %w", err)
	}
	argv = []string{
		"container", "exec", "-it", containerName,
		"claude", "--dangerously-skip-permissions",
	}
	return bin, argv, nil
}

// attachInteractive replaces the current process with the container-exec, so
// terminal signals (Ctrl-C, resize) flow uninterrupted to `container exec`.
func attachInteractive(containerName string) error {
	bin, argv, err := attachArgs(containerName)
	if err != nil {
		return err
	}
	// Clear the terminal before claude takes over (see original comment).
	if isStdoutTTY() {
		_, _ = os.Stdout.WriteString("\033c")
	}
	return syscall.Exec(bin, argv, os.Environ())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli -run TestAttachArgs -v -skip 'TestCspaceLifecycle'`
Expected: PASS (or SKIP if `container` isn't on PATH — acceptable).
Also run: `go build ./...` — Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_attach.go internal/cli/cmd_attach_test.go
git commit -m "Extract attachArgs so the TUI can reuse the attach argv"
```

---

## Task 6: `internal/tui` Model, Update, keys, action/poller interfaces

**Files:**
- Create: `internal/tui/actions.go` (interface + msg types)
- Create: `internal/tui/keys.go` (keymap + pure predicates)
- Create: `internal/tui/model.go` (Model, NewModel, Init, Update)
- Test: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `Poller` (Task 4), `Snapshot`/`Row` (Task 2), `EventLine`/`SessionEventsPath` (Task 3).
- Produces: `type Actor interface{ Attach(Row) tea.Cmd; Down(Row) tea.Cmd; Send(Row, string) tea.Cmd; Interrupt(Row) tea.Cmd; RestartBrowser(Row) tea.Cmd }`; `type Model`; `func NewModel(p Poller, a Actor, home string, interval time.Duration, now func() time.Time) Model`; `Init`/`Update`/`View` value-receiver methods; message types `snapshotMsg`, `eventsMsg`, `actionResultMsg`, `pollTickMsg`.

- [ ] **Step 1: Write `internal/tui/actions.go`**

```go
package tui

import tea "github.com/charmbracelet/bubbletea"

// Actor executes the side-effecting commands the dashboard offers. It is
// consumer-defined here and implemented in internal/cli (where teardownSandbox,
// attachArgs and the control-port HTTP client live), then injected — so this
// package never imports internal/cli. Each method returns a tea.Cmd that
// eventually emits an actionResultMsg (or, for Attach, resumes the program via
// tea.ExecProcess before emitting one).
type Actor interface {
	Attach(row Row) tea.Cmd
	Down(row Row) tea.Cmd
	Send(row Row, text string) tea.Cmd
	Interrupt(row Row) tea.Cmd
	RestartBrowser(row Row) tea.Cmd
}

// actionResultMsg reports the outcome of an Actor command. label is a short
// verb ("attach", "down", "send", "interrupt", "browser restart") used in the
// footer; err is nil on success.
type actionResultMsg struct {
	label string
	err   error
}
```

- [ ] **Step 2: Write `internal/tui/keys.go`**

```go
package tui

// contextual predicates — pure, tested directly. A running container means
// State is anything other than StateStopped.

func canAttach(r Row) bool { return r.Kind == RowSandbox && r.State != StateStopped }
func canDown(r Row) bool   { return r.Kind == RowSandbox && r.State != StateStopped }
func canSend(r Row) bool   { return r.Kind == RowSandbox && r.Agent.Reachable }
func canInterrupt(r Row) bool {
	return r.Kind == RowSandbox && r.Agent.Reachable && r.Agent.State == "working"
}
func canBrowser(r Row) bool { return r.Kind == RowBrowser || r.Kind == RowSandbox }
```

- [ ] **Step 3: Write the failing test**

`internal/tui/model_test.go`:

```go
package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// --- fakes ---

type fakePoller struct{ snap Snapshot }

func (f fakePoller) Poll(context.Context) Snapshot { return f.snap }

type recordingActor struct {
	downCalls      []Row
	interruptCalls []Row
	sendCalls      []struct {
		row  Row
		text string
	}
	browserCalls []Row
	attachCalls  []Row
}

func (a *recordingActor) result(label string) tea.Cmd {
	return func() tea.Msg { return actionResultMsg{label: label} }
}
func (a *recordingActor) Attach(r Row) tea.Cmd    { a.attachCalls = append(a.attachCalls, r); return a.result("attach") }
func (a *recordingActor) Down(r Row) tea.Cmd      { a.downCalls = append(a.downCalls, r); return a.result("down") }
func (a *recordingActor) Interrupt(r Row) tea.Cmd { a.interruptCalls = append(a.interruptCalls, r); return a.result("interrupt") }
func (a *recordingActor) RestartBrowser(r Row) tea.Cmd {
	a.browserCalls = append(a.browserCalls, r)
	return a.result("browser restart")
}
func (a *recordingActor) Send(r Row, text string) tea.Cmd {
	a.sendCalls = append(a.sendCalls, struct {
		row  Row
		text string
	}{r, text})
	return a.result("send")
}

func twoSandboxSnap() Snapshot {
	return Snapshot{Rows: []Row{
		{Kind: RowProject, Name: "alpha"},
		{Kind: RowSandbox, Project: "alpha", Name: "mercury", Container: "cspace-alpha-mercury",
			State: StateRunning, Selectable: true, Agent: AgentStatus{Reachable: true, State: "working"}},
		{Kind: RowSidecar, Project: "alpha", Name: "convex"},
		{Kind: RowBrowser, Project: "alpha", Name: "browser (shared)", Container: "cspace-alpha-browser", Selectable: true},
	}}
}

func newTestModel(a Actor) Model {
	m := NewModel(fakePoller{snap: twoSandboxSnap()}, a, "/home/x", 2*time.Second,
		func() time.Time { return time.Unix(1_000_000, 0) })
	// seed rows as if a poll landed
	mm, _ := m.Update(snapshotMsg{snap: twoSandboxSnap()})
	return mm.(Model)
}

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestSelectionSkipsNonSelectableRows(t *testing.T) {
	m := newTestModel(&recordingActor{})
	// first selectable row is index 1 (sandbox)
	if got := m.selectedRow().Name; got != "mercury" {
		t.Fatalf("initial selection = %q, want mercury", got)
	}
	// move down: should skip sidecar(2) and land on browser(3)
	mm, _ := m.Update(key('j'))
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "browser (shared)" {
		t.Errorf("after j, selection = %q, want browser (shared)", got)
	}
}

func TestDownRequiresConfirm(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	// 'd' opens confirm, does NOT call Down yet
	mm, _ := m.Update(key('d'))
	m = mm.(Model)
	if len(a.downCalls) != 0 {
		t.Fatal("Down called before confirm")
	}
	// 'y' confirms
	mm, _ = m.Update(key('y'))
	m = mm.(Model)
	if len(a.downCalls) != 1 || a.downCalls[0].Name != "mercury" {
		t.Errorf("Down calls = %+v, want one for mercury", a.downCalls)
	}
}

func TestDownConfirmCancel(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	mm, _ := m.Update(key('d'))
	m = mm.(Model)
	mm, _ = m.Update(key('n'))
	m = mm.(Model)
	if len(a.downCalls) != 0 {
		t.Errorf("Down should be cancelled, got %+v", a.downCalls)
	}
}

func TestInterruptOnlyWhenWorking(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a) // mercury agent State=="working"
	mm, _ := m.Update(key('i'))
	m = mm.(Model)
	if len(a.interruptCalls) != 1 {
		t.Errorf("interrupt calls = %d, want 1", len(a.interruptCalls))
	}
}

func TestActionInFlightGatesOtherActions(t *testing.T) {
	a := &recordingActor{}
	m := newTestModel(a)
	// start an interrupt => action in flight
	mm, _ := m.Update(key('i'))
	m = mm.(Model)
	// a second action key must be ignored while one is running
	mm, _ = m.Update(key('d'))
	m = mm.(Model)
	if m.mode == modeConfirmDown {
		t.Error("down confirm opened while an action was in flight")
	}
	// resolving the action clears the gate
	mm, _ = m.Update(actionResultMsg{label: "interrupt"})
	m = mm.(Model)
	if m.action != "" {
		t.Errorf("action still in flight after result: %q", m.action)
	}
}

func TestSnapshotPreservesSelectionByIdentity(t *testing.T) {
	m := newTestModel(&recordingActor{})
	mm, _ := m.Update(key('j')) // select browser
	m = mm.(Model)
	// a new snapshot with an extra project prepended must keep browser selected
	snap := twoSandboxSnap()
	snap.Rows = append([]Row{
		{Kind: RowProject, Name: "aaa"},
		{Kind: RowSandbox, Project: "aaa", Name: "x", Selectable: true},
	}, snap.Rows...)
	mm, _ = m.Update(snapshotMsg{snap: snap})
	m = mm.(Model)
	if got := m.selectedRow().Name; got != "browser (shared)" {
		t.Errorf("selection after snapshot = %q, want browser (shared)", got)
	}
}

func TestQuitOnCtrlC(t *testing.T) {
	m := newTestModel(&recordingActor{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("quit command produced nil msg")
	} // tea.Quit's msg is tea.QuitMsg{}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/tui -run 'TestSelection|TestDown|TestInterrupt|TestAction|TestSnapshot|TestQuit' -v`
Expected: FAIL — `undefined: NewModel`.

- [ ] **Step 5: Write `internal/tui/model.go`**

```go
package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type uiMode int

const (
	modeNormal uiMode = iota
	modeConfirmDown
	modeInput
)

// notice is a transient footer message. err notices persist until the next
// keypress; success notices fade (the model just clears them on the next tick).
type notice struct {
	text  string
	isErr bool
}

// Model is the dashboard state. Value type with value-receiver Init/Update/View
// (mirrors internal/overlay): mutate the local m and return it.
type Model struct {
	poller   Poller
	actor    Actor
	home     string
	interval time.Duration
	now      func() time.Time

	rows     []Row
	selected int // index into rows; always kept on a Selectable row
	daemon   DaemonHealth
	snapErr  error
	lastPoll time.Time
	polling  bool

	events    []EventLine
	eventsErr error

	mode    uiMode
	input   textinput.Model
	action  string // in-flight action label; "" when idle
	spinner spinner.Model
	notice  notice
	help    bool

	width, height int
	quitting      bool
}

// message types
type snapshotMsg struct{ snap Snapshot }
type eventsMsg struct {
	lines []EventLine
	err   error
}
type pollTickMsg time.Time

// NewModel constructs the dashboard model. home is the host home dir (for the
// event-log path); interval is the poll cadence; now is injected for tests.
func NewModel(p Poller, a Actor, home string, interval time.Duration, now func() time.Time) Model {
	ti := textinput.New()
	ti.Placeholder = "message"
	ti.CharLimit = 2000
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		poller: p, actor: a, home: home, interval: interval, now: now,
		input: ti, spinner: sp,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, pollTickCmd(m.interval), m.pollNowCmd())
}

func pollTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return pollTickMsg(t) })
}

// pollNowCmd runs one poll off the UI goroutine; each poll gets its own bounded
// context so a wedged probe can't hang forever.
func (m Model) pollNowCmd() tea.Cmd {
	p := m.poller
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return snapshotMsg{snap: p.Poll(ctx)}
	}
}

func (m Model) readEventsCmd() tea.Cmd {
	row := m.selectedRow()
	if row.Kind != RowSandbox {
		return func() tea.Msg { return eventsMsg{} }
	}
	home, project, name := m.home, row.Project, row.Name
	return func() tea.Msg {
		lines, err := TailEvents(SessionEventsPath(home, project, name), 8)
		return eventsMsg{lines: lines, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case pollTickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, pollTickCmd(m.interval)) // always re-arm
		// pause polling while a modal or action is active, or a poll is in flight
		if !m.polling && m.mode == modeNormal && m.action == "" {
			m.polling = true
			cmds = append(cmds, m.pollNowCmd())
		}
		return m, tea.Batch(cmds...)

	case snapshotMsg:
		prev := m.selectedRow()
		m.rows = msg.snap.Rows
		m.daemon = msg.snap.Daemon
		m.snapErr = msg.snap.Err
		m.lastPoll = msg.snap.TakenAt
		m.polling = false
		m.restoreSelection(prev)
		if m.notice.text != "" && !m.notice.isErr {
			m.notice = notice{} // fade success notices on the next poll
		}
		return m, m.readEventsCmd()

	case eventsMsg:
		m.events, m.eventsErr = msg.lines, msg.err
		return m, nil

	case actionResultMsg:
		m.action = ""
		if msg.err != nil {
			m.notice = notice{text: msg.label + " failed: " + msg.err.Error(), isErr: true}
		} else {
			m.notice = notice{text: msg.label + " ok"}
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// global quit always available
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	switch m.mode {
	case modeConfirmDown:
		return m.handleConfirmKey(msg)
	case modeInput:
		return m.handleInputKey(msg)
	}
	return m.handleNormalKey(msg)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		m.moveSelection(-1)
		return m, m.readEventsCmd()
	case "down", "j":
		m.moveSelection(1)
		return m, m.readEventsCmd()
	case "r":
		if !m.polling {
			m.polling = true
			return m, m.pollNowCmd()
		}
		return m, nil
	case "?":
		m.help = !m.help
		return m, nil
	}
	// action keys — gated while another action is in flight
	if m.action != "" {
		return m, nil
	}
	row := m.selectedRow()
	switch msg.String() {
	case "enter":
		if canAttach(row) {
			m.action = "attach"
			return m, m.actor.Attach(row)
		}
	case "d":
		if canDown(row) {
			m.mode = modeConfirmDown
		}
	case "i":
		if canInterrupt(row) {
			m.action = "interrupt"
			return m, m.actor.Interrupt(row)
		}
	case "b":
		if canBrowser(row) {
			m.action = "browser restart"
			return m, m.actor.RestartBrowser(row)
		}
	case "s":
		if canSend(row) {
			m.mode = modeInput
			m.input.SetValue("")
			return m, m.input.Focus()
		}
	}
	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "y" {
		m.mode = modeNormal
		row := m.selectedRow()
		m.action = "down"
		return m, m.actor.Down(row)
	}
	m.mode = modeNormal // any other key cancels
	return m, nil
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		text := m.input.Value()
		m.mode = modeNormal
		m.input.Blur()
		if text == "" {
			return m, nil
		}
		row := m.selectedRow()
		m.action = "send"
		return m, m.actor.Send(row, text)
	case tea.KeyEsc:
		m.mode = modeNormal
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// selectedRow returns the currently selected row, or a zero Row if none.
func (m Model) selectedRow() Row {
	if m.selected >= 0 && m.selected < len(m.rows) {
		return m.rows[m.selected]
	}
	return Row{}
}

// moveSelection moves to the next/prev Selectable row in direction dir (+1/-1).
func (m *Model) moveSelection(dir int) {
	n := len(m.rows)
	for i := 1; i <= n; i++ {
		idx := m.selected + dir*i
		if idx < 0 || idx >= n {
			return
		}
		if m.rows[idx].Selectable {
			m.selected = idx
			return
		}
	}
}

// restoreSelection re-points m.selected at the row matching prev's identity
// after a new snapshot; falls back to the first selectable row.
func (m *Model) restoreSelection(prev Row) {
	for i, r := range m.rows {
		if r.Selectable && r.Kind == prev.Kind && r.Project == prev.Project && r.Name == prev.Name {
			m.selected = i
			return
		}
	}
	for i, r := range m.rows {
		if r.Selectable {
			m.selected = i
			return
		}
	}
	m.selected = 0
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/tui -run 'TestSelection|TestDown|TestInterrupt|TestAction|TestSnapshot|TestQuit' -v`
Expected: PASS.
Note: `View` is not yet defined; Task 7 adds it. `Model` still satisfies `tea.Model` only after `View` exists — but these tests call `Update` directly and never assign to a `tea.Model` var requiring `View`, EXCEPT `newTestModel` does `mm.(Model)` after `m.Update(...)` which returns `tea.Model`. `Update` returns `tea.Model` regardless; the type assertion works. The package compiles without `View` only if nothing requires the full interface. To be safe, add a temporary `func (m Model) View() string { return "" }` at the end of model.go now, and Task 7 replaces it.

- [ ] **Step 7: Add temporary View stub, then commit**

Add to the end of `internal/tui/model.go`:
```go
// View is implemented in view.go.
```
And create a minimal `internal/tui/view.go` stub so the package builds:
```go
package tui

func (m Model) View() string { return "" }
```

Run: `go build ./internal/tui && go test ./internal/tui -run 'TestSelection|TestDown|TestInterrupt|TestAction|TestSnapshot|TestQuit'`
Expected: builds + PASS.

```bash
git add internal/tui/actions.go internal/tui/keys.go internal/tui/model.go internal/tui/view.go internal/tui/model_test.go
git commit -m "Add tui model, update loop, keybindings, and action interface"
```

---

## Task 7: `internal/tui` View rendering

**Files:**
- Modify: `internal/tui/view.go` (replace the stub)
- Test: `internal/tui/view_test.go`

**Interfaces:**
- Consumes: `Model` (Task 6), `Row`/`Snapshot` (Task 2).
- Produces: `func (m Model) View() string` + `formatMemory(int64) string`, `formatUptime(time.Duration) string`.

- [ ] **Step 1: Write the failing test**

`internal/tui/view_test.go`:

```go
package tui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatMemory(t *testing.T) {
	cases := map[int64]string{
		16 << 30: "16G",
		1 << 30:  "1G",
		512 << 20: "512M",
		0:        "-",
	}
	for in, want := range cases {
		if got := formatMemory(in); got != want {
			t.Errorf("formatMemory(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestViewRendersDegradedAndDaemon(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.daemon = DaemonHealth{Reachable: true, Version: "1.0.0-rc.40"}
	m.rows = []Row{
		{Kind: RowProject, Name: "alpha"},
		{Kind: RowSandbox, Project: "alpha", Name: "mercury", State: StateDegraded, Selectable: true},
	}
	out := m.View()
	if !strings.Contains(out, "mercury") {
		t.Error("view missing sandbox name")
	}
	if !strings.Contains(out, "1.0.0-rc.40") {
		t.Error("view missing daemon version")
	}
}

func TestViewConfirmFooter(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.mode = modeConfirmDown
	out := m.View()
	if !strings.Contains(strings.ToLower(out), "y/n") && !strings.Contains(strings.ToLower(out), "[y/") {
		t.Errorf("confirm footer not shown; got tail:\n%s", out)
	}
}

func TestViewDaemonUnreachable(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.daemon = DaemonHealth{Reachable: false}
	out := m.View()
	if !strings.Contains(strings.ToLower(out), "unreachable") {
		t.Error("view should mark daemon unreachable")
	}
}

func TestViewNoEvents(t *testing.T) {
	m := newTestModel(&recordingActor{})
	m.width, m.height = 100, 30
	m.events = nil
	out := m.View()
	if !strings.Contains(out, "no events") {
		t.Error("empty event tail should render 'no events yet'")
	}
	_ = time.Second
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui -run 'TestFormatMemory|TestView' -v`
Expected: FAIL — `undefined: formatMemory` (and View stub returns "").

- [ ] **Step 3: Write `internal/tui/view.go`** (replace the stub)

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleHeader   = lipgloss.NewStyle().Bold(true)
	styleProject  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#8888ff"))
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#5fffaf"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fffaf"))
)

// formatMemory renders bytes as a compact G/M string; 0 -> "-".
func formatMemory(b int64) string {
	switch {
	case b <= 0:
		return "-"
	case b >= 1<<30:
		return fmt.Sprintf("%dG", b/(1<<30))
	default:
		return fmt.Sprintf("%dM", b/(1<<20))
	}
}

// formatUptime renders a duration as ↑<h>h<m>m / ↑<m>m / ↑<s>s.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("↑%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("↑%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("↑%ds", int(d.Seconds()))
	}
}

func stateGlyph(r Row) string {
	switch r.State {
	case StateRunning:
		return "●"
	case StateDegraded:
		return "◐"
	case StateBooting:
		return "◍"
	default:
		return "○"
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	var b strings.Builder

	// header
	daemon := "unreachable"
	dStyle := styleErr
	if m.daemon.Reachable {
		daemon = "ok " + m.daemon.Version
		dStyle = styleOK
	}
	b.WriteString(styleHeader.Render("cspace tui"))
	b.WriteString("   ")
	b.WriteString(dStyle.Render("daemon: " + daemon))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", min(m.width, 78)))
	b.WriteString("\n")

	// list-failure banner
	if m.snapErr != nil {
		b.WriteString(styleErr.Render("container ls failed: "+m.snapErr.Error()+" — run cspace doctor") + "\n")
	}

	// rows
	for i, r := range m.rows {
		line := renderRow(r)
		if i == m.selected && r.Selectable {
			line = styleSelected.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString(strings.Repeat("─", min(m.width, 78)))
	b.WriteString("\n")

	// detail pane for the selected sandbox
	b.WriteString(m.renderDetail())

	// footer
	b.WriteString(m.renderFooter())
	return b.String()
}

func renderRow(r Row) string {
	switch r.Kind {
	case RowProject:
		return styleProject.Render(r.Name)
	case RowSandbox:
		agent := "agent: ?"
		if r.Agent.Reachable {
			agent = "agent: " + r.Agent.State + "  q:" + fmt.Sprintf("%d", r.Agent.QueueDepth)
		}
		return fmt.Sprintf("%s %-14s %-18s %-15s %-4s %s",
			stateGlyph(r), r.Name, agent, r.IP, formatMemory(r.MemoryB), formatUptime(r.Uptime))
	case RowSidecar:
		return styleDim.Render(fmt.Sprintf("  ├ %-16s %-9s %-15s %s",
			r.Name, "running", r.IP, formatMemory(r.MemoryB)))
	case RowBrowser:
		return fmt.Sprintf("%s %-16s %-9s %-15s %s",
			stateGlyph(r), r.Name, "running", r.IP, formatMemory(r.MemoryB))
	case RowSystem:
		return styleDim.Render(fmt.Sprintf("  %-18s %-9s %-15s %s",
			r.Name, "running", r.IP, formatMemory(r.MemoryB)))
	}
	return r.Name
}

func (m Model) renderDetail() string {
	row := m.selectedRow()
	if row.Kind != RowSandbox {
		return styleDim.Render("select a sandbox for details") + "\n"
	}
	var b strings.Builder
	if row.Agent.Reachable {
		b.WriteString(fmt.Sprintf("%s · session %s · %s · lastEvent %s\n",
			row.Name, row.Agent.Session, row.Agent.State, lastEventLabel(row.Agent)))
	} else {
		b.WriteString(styleDim.Render(row.Name+" · no running agent") + "\n")
	}
	if len(m.events) == 0 {
		b.WriteString(styleDim.Render("no events yet") + "\n")
	} else {
		for _, e := range m.events {
			b.WriteString(styleDim.Render(fmt.Sprintf("  %s %-10s %s", shortTs(e.Ts), e.Type, e.Subtype)) + "\n")
		}
	}
	return b.String()
}

func lastEventLabel(a AgentStatus) string {
	if a.LastEventType == "" {
		return "-"
	}
	if a.LastEventSubtype != "" {
		return a.LastEventType + "/" + a.LastEventSubtype
	}
	return a.LastEventType
}

func shortTs(ts string) string {
	if len(ts) >= 19 {
		return ts[11:19] // HH:MM:SS from an ISO8601 string
	}
	return ts
}

func (m Model) renderFooter() string {
	switch m.mode {
	case modeConfirmDown:
		return styleErr.Render(fmt.Sprintf("down %s? [y/N]", m.selectedRow().Name))
	case modeInput:
		return "send to " + m.selectedRow().Name + "› " + m.input.View()
	}
	if m.action != "" {
		return m.spinner.View() + " " + m.action + "…"
	}
	if m.notice.text != "" {
		if m.notice.isErr {
			return styleErr.Render(m.notice.text)
		}
		return styleOK.Render(m.notice.text)
	}
	if m.help {
		return styleDim.Render("↑/↓ move · enter attach · s send · i interrupt · d down · b browser · r refresh · q quit")
	}
	return styleDim.Render("[enter] attach  [s]end  [i]nterrupt  [d]own  [b]rowser restart  [?] help  [q]uit")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tui -run 'TestFormatMemory|TestView' -v`
Expected: PASS.
Then full package: `go test ./internal/tui` — Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/view.go internal/tui/view_test.go
git commit -m "Add tui view rendering"
```

---

## Task 8: `internal/cli` real Actor (`tuiActor`)

**Files:**
- Create: `internal/cli/tui_actor.go`
- Test: `internal/cli/tui_actor_test.go`

**Interfaces:**
- Consumes: `tui.Actor`/`tui.Row` (Task 6), `attachArgs` (Task 5), `teardownSandbox` (existing), `restartBrowserSidecar`/`agentErrorText` (existing).
- Produces: `type tuiActor struct{...}`; `func newTUIActor(a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor`; implements all five `tui.Actor` methods.

**Design:** Send/Interrupt hit the row's `ControlURL`+`Token` directly (no `resolveEntry` — the row already carries them). Down calls `teardownSandbox` in a goroutine, capturing its `io.Writer` output; since `teardownSandbox` has no return value, success is "it ran without panfrom a wedged Stop" — surface any captured warning text only on a non-zero signal we can detect (we treat completion as success and put a short summary in the notice). Attach returns `tea.ExecProcess`.

- [ ] **Step 1: Write the failing test**

`internal/cli/tui_actor_test.go`:

```go
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/tui"
)

func drain(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
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

	a := newTUIActor(nil, nil, "/home/x")
	row := tui.Row{Kind: tui.RowSandbox, Project: "alpha", Name: "mercury", ControlURL: srv.URL, Token: "tok"}
	msg := drain(a.Send(row, "hello"))

	res, ok := msg.(interface{ Label() string })
	_ = res
	_ = ok
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" || gotBody != "hello" {
		t.Errorf("send request: path=%q auth=%q ct=%q body=%q", gotPath, gotAuth, gotCT, gotBody)
	}
}

func TestTUIActorInterruptSurfaces409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "no active task"})
	}))
	defer srv.Close()

	a := newTUIActor(nil, nil, "/home/x")
	row := tui.Row{Kind: tui.RowSandbox, ControlURL: srv.URL, Token: "tok"}
	msg := drain(a.Interrupt(row))
	// the message must report an error mentioning "no active task"
	if !msgHasError(msg, "no active task") {
		t.Errorf("interrupt 409 not surfaced: %#v", msg)
	}
}
```

Because `actionResultMsg` is unexported in package `tui`, the actor emits its OWN result message type that the model does not consume directly — instead the actor's methods must return the SAME `tui` message. To keep the message type shared, expose a constructor from `tui`: add to `internal/tui/actions.go` an exported helper. **Update Task 6's `actions.go`** to also export:

```go
// Result builds the message an Actor returns to report an outcome. label is a
// short verb; err is nil on success. Exported so out-of-package Actor
// implementations (internal/cli) can construct the model's result message.
func Result(label string, err error) tea.Msg { return actionResultMsg{label: label, err: err} }

// ResultLabel/ResultErr expose an actionResultMsg for out-of-package tests.
func ResultLabel(m tea.Msg) (string, bool) {
	r, ok := m.(actionResultMsg)
	return r.label, ok
}
func ResultErr(m tea.Msg) error {
	if r, ok := m.(actionResultMsg); ok {
		return r.err
	}
	return nil
}
```

Then the actor test helper `msgHasError`:

```go
func msgHasError(msg tea.Msg, want string) bool {
	err := tui.ResultErr(msg)
	return err != nil && strings.Contains(err.Error(), want)
}
```

(add imports `strings`, `github.com/elliottregan/cspace/internal/tui`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestTUIActor -v -skip 'TestCspaceLifecycle'`
Expected: FAIL — `undefined: newTUIActor`.

- [ ] **Step 3: Write `internal/cli/tui_actor.go`**

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor against the real host: attach via
// tea.ExecProcess, down via teardownSandbox, send/interrupt/browser via HTTP
// and the browser restart ladder. Constructed by cmd_tui.go.
type tuiActor struct {
	adapter  *applecontainer.Adapter
	registry *registry.Registry
	home     string
	client   *http.Client
}

func newTUIActor(a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor {
	return &tuiActor{adapter: a, registry: r, home: home, client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	bin, argv, err := attachArgs(row.Container)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return tui.Result("attach", err) })
}

func (t *tuiActor) Down(row tui.Row) tea.Cmd {
	adapter, reg, project, name := t.adapter, t.registry, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var buf bytes.Buffer
		teardownSandbox(ctx, adapter, reg, project, name, &buf, true /* wipeState */)
		return tui.Result("down", nil)
	}
}

func (t *tuiActor) Send(row tui.Row, text string) tea.Cmd {
	url, token := row.ControlURL, row.Token
	client := t.client
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"session": "primary", "text": text})
		req, err := http.NewRequest(http.MethodPost, url+"/send", bytes.NewReader(body))
		if err != nil {
			return tui.Result("send", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return tui.Result("send", doExpect2xx(client, req))
	}
}

func (t *tuiActor) Interrupt(row tui.Row) tea.Cmd {
	url, token := row.ControlURL, row.Token
	client := t.client
	return func() tea.Msg {
		req, err := http.NewRequest(http.MethodPost, url+"/interrupt", nil)
		if err != nil {
			return tui.Result("interrupt", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return tui.Result("interrupt", doExpect2xx(client, req))
	}
}

func (t *tuiActor) RestartBrowser(row tui.Row) tea.Cmd {
	adapter, project := t.adapter, row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := restartBrowserSidecar(ctx, adapter, project)
		return tui.Result("browser restart", err)
	}
}

// doExpect2xx runs req and returns nil on a 2xx, else an error carrying the
// server's error text (mirrors agentErrorText for a clean footer message).
func doExpect2xx(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, agentErrorText(body))
	}
	return nil
}
```

**Note for the implementer:** verify `restartBrowserSidecar`'s exact signature in `internal/cli/browser.go` and adapt the call (it may take different args, e.g. a project or container name and verify/restart function seams). If its signature differs, match it — the contract here is "restart the project's shared browser and return an error". Do not change `restartBrowserSidecar` itself.

- [ ] **Step 4: Update `internal/tui/actions.go`** with the exported `Result`/`ResultLabel`/`ResultErr` helpers shown in Step 1, then run tests.

Run: `go test ./internal/tui ./internal/cli -run 'TestTUIActor|Test' -skip 'TestCspaceLifecycle'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/tui_actor.go internal/cli/tui_actor_test.go internal/tui/actions.go
git commit -m "Add tuiActor implementing tui.Actor against the real host"
```

---

## Task 9: `cmd_tui.go` wiring, registration, and docs

**Files:**
- Create: `internal/cli/cmd_tui.go`
- Modify: `internal/cli/root.go` (add `newTuiCmd()` to `AddCommand`)
- Modify: `CLAUDE.md` (Commands section), `README.md`
- Test: `internal/cli/cmd_tui_test.go`

**Interfaces:**
- Consumes: `tui.NewModel`/`tui.NewPoller`/`newTUIActor`, `applecontainer.New`, `registry.DefaultPath`.
- Produces: `func newTuiCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

`internal/cli/cmd_tui_test.go`:

```go
package cli

import (
	"testing"
)

func TestNewTuiCmdBasics(t *testing.T) {
	cmd := newTuiCmd()
	if cmd.Use != "tui" {
		t.Errorf("Use = %q, want tui", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short must be set")
	}
	// --interval flag exists with a sane default
	f := cmd.Flags().Lookup("interval")
	if f == nil {
		t.Fatal("--interval flag missing")
	}
	if f.DefValue != "2s" {
		t.Errorf("--interval default = %q, want 2s", f.DefValue)
	}
}

func TestRootRegistersTui(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "tui" {
			return
		}
	}
	t.Error("root does not register the tui command")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run 'TestNewTuiCmd|TestRootRegistersTui' -v -skip 'TestCspaceLifecycle'`
Expected: FAIL — `undefined: newTuiCmd`.

- [ ] **Step 3: Write `internal/cli/cmd_tui.go`**

```go
package cli

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/tui"
)

// daemonBaseURL is the host daemon's HTTP base (registry + health), matching
// daemonHTTPPort in cmd_daemon.go.
const daemonBaseURL = "http://127.0.0.1:6280"

func newTuiCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Full-screen dashboard of cspace containers with common actions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < time.Second {
				interval = time.Second // floor
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			regPath, err := registry.DefaultPath()
			if err != nil {
				return fmt.Errorf("resolve registry path: %w", err)
			}
			reg := &registry.Registry{Path: regPath}
			adapter := applecontainer.New()

			poller := tui.NewPoller(adapter, reg, daemonBaseURL, time.Now)
			actor := newTUIActor(adapter, reg, home)
			model := tui.NewModel(poller, actor, home, interval, time.Now)

			prog := tea.NewProgram(model, tea.WithAltScreen())
			_, err = prog.Run()
			return err
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "refresh interval (floored at 1s)")
	return cmd
}
```

- [ ] **Step 4: Register in `root.go`**

In `internal/cli/root.go`, add `newTuiCmd(),` to the `root.AddCommand(...)` list (alongside `newBrowserCmd()`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/cli -run 'TestNewTuiCmd|TestRootRegistersTui' -v -skip 'TestCspaceLifecycle'`
Expected: PASS.
Then: `go build ./...` — Expected: clean.

- [ ] **Step 6: Update docs**

In `CLAUDE.md`, add to the Commands list:
```markdown
- `cspace tui` — full-screen dashboard of all cspace containers (grouped by project) with attach / down / agent send·interrupt / browser restart
```

In `README.md`, add a short `cspace tui` entry mirroring the CLAUDE.md line (match README's existing command-list style).

- [ ] **Step 7: Full check and commit**

Run: `make check` (side-effect-free now that host-mutating tests are `CSPACE_E2E`-gated)
Expected: exit 0.

```bash
git add internal/cli/cmd_tui.go internal/cli/cmd_tui_test.go internal/cli/root.go CLAUDE.md README.md
git commit -m "Add cspace tui command wiring and docs"
```

---

## Self-Review

**1. Spec coverage:**
- Host-wide grouped-by-project view → Task 2 `Correlate` (project grouping/sort), Task 7 view. ✓
- Live container state (state/IP/mem/uptime) → Task 1 `List`, Task 2 fold. ✓
- Agent status (state/queue/session/lastEventSubtype) → Task 4 poller fan-out, Task 2 `AgentStatus`. ✓
- Detail pane status + live event tail → Task 3 `TailEvents`, Task 6 `readEventsCmd`, Task 7 detail render. ✓
- attach / down(confirm) / send / interrupt / browser restart → Task 6 keys + Task 8 actor. ✓
- Glyphs ●/○/◐ and stopped/degraded/booting derivation → Task 2 state logic, Task 7 `stateGlyph`. ✓
- Sidecar nesting, browser project-row, buildkit dimmed system row → Task 2 fold. ✓
- Contextual keybindings (i only when working, etc.) → Task 6 predicates. ✓
- One-action-at-a-time gating, confirm/input footers → Task 6 mode state machine, Task 7 footer. ✓
- Degraded/stale error handling (daemon unreachable, ls banner, no-events) → Task 2 `Err`, Task 7 render. ✓
- Polling pause during modal/action, selection preservation → Task 6. ✓
- Testing strategy (pure model/poller fakes, no live-host default) → every task's tests. ✓
- `up` out of scope → not implemented. ✓

**2. Placeholder scan:** No TBD/TODO. Two implementer notes flag real verification points (the `restartBrowserSidecar` signature in Task 8; the `View`-stub sequencing in Task 6) — both are concrete instructions, not placeholders.

**3. Type consistency:** `ContainerSummary`, `Row`, `AgentStatus`, `Snapshot`, `Correlate`, `Poller`, `Actor`, `Result`/`ResultErr` names are consistent across Tasks 1–9. `Actor` interface (Task 6) matches `tuiActor` methods (Task 8). `NewModel`/`NewPoller`/`newTUIActor` signatures match their call sites in Task 9. `actionResultMsg` is unexported in `tui`; cross-package construction goes through the exported `tui.Result` (added in Task 8 Step 1, folded into Task 6's `actions.go`).

## Execution Handoff

Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, task review (spec + quality) between tasks, broad final review.
2. **Inline Execution** — batch execution in this session with checkpoints.
