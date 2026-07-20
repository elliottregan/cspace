# cspace TUI Design

**Date:** 2026-07-20
**Status:** approved
**Author:** brainstormed with Elliott

## Goal

A full-screen terminal dashboard, launched with `cspace tui`, that shows every
cspace-related container on the host — sandboxes, their compose sidecars, and
the shared browser sidecar, grouped by project — with live agent status, and
lets the user run the common per-sandbox commands (attach, down, agent
send/interrupt, browser restart) without leaving the view.

## Non-Goals (v1)

- **Booting new sandboxes (`up`).** `up` needs a project cwd, name/role/model
  inputs, and the boot overlay; the CLI already does this well. The TUI is
  read-and-act on existing state.
- **Cross-host / remote visibility.** Single host only (the daemon and registry
  are host-local).
- **Editing config, secrets, or DNS.** Out of scope.
- **A web/HTTP UI.** Terminal-first, in-binary.

## Architecture

`cspace tui` launches a Bubble Tea app in alt-screen mode. Bubble Tea,
lipgloss, and bubbles are already dependencies (used by `internal/overlay`), so
no new framework is introduced.

The TUI reuses the same internals the CLI commands already use — it never
parses human-oriented CLI output:

- `registry.List()` (`internal/registry`) → project/sandbox names, control
  URLs, tokens, recorded `State`, `StartedAt`, `BrowserContainer`.
- A new substrate adapter method `List(ctx)` (see below) → live container state,
  IPs, resources, uptime for every `cspace-*` container plus `buildkit`.
- The agent-status HTTP client shape from `cmd_agent.go` (`GET /status`,
  `POST /send`, `POST /interrupt`, Bearer auth) → agent state, queue depth,
  session, `lastEventType`/`lastEventSubtype`.
- Daemon `GET /health` (`127.0.0.1:6280`) → version + reachability.
- Host-side event files at
  `~/.cspace/sessions/<project>/<sandbox>/primary/events.ndjson` → the detail
  pane's live tail (no container exec).
- `attach` and `down` execute the same code the CLI commands run, through
  seams (below).

### Package layout

New package `internal/tui`:

- `internal/tui/model.go` — the Bubble Tea `Model`: state struct, `Init`,
  `Update` (all message handling, keybindings, selection, confirm/input flows,
  action-in-flight gating). Pure and unit-testable.
- `internal/tui/poll.go` — the `Poller` interface and its real implementation;
  the `Snapshot` type that `Update` consumes; the event-tail reader.
- `internal/tui/view.go` — `View()` rendering (header, list, detail pane,
  footer) via lipgloss.
- `internal/tui/actions.go` — the `Actor` interface (attach/down/send/
  interrupt/browser-restart) and its real implementation; returns `tea.Cmd`s.
- `internal/cli/cmd_tui.go` — `newTUICmd()` cobra command, registered in
  `root.go`; wires the real `Poller`/`Actor` and runs the program.

The `Poller` and `Actor` interfaces are the test seams: unit tests inject fakes
so no test touches the host, and the whole suite stays inside the
`CSPACE_E2E`-free default `go test ./...`.

### New substrate method

`internal/substrate/applecontainer/adapter.go` gains:

```go
// ContainerSummary is one row of `container ls --format json`, narrowed to
// the fields the TUI renders. Field tags match the Apple Container CLI's
// JSON keys (verified against 0.12.x); parse failures carry the same
// version-drift hint as IP().
type ContainerSummary struct {
    Name    string // e.g. "cspace-resume-redux-mercury"
    Image   string
    State   string // "running" | "stopped" | ...
    IP      string // first IPv4, CIDR stripped, "" if none
    CPUs    int
    MemoryB int64  // bytes; rendered as G/M
    Started time.Time
}

// List returns every container the CLI reports (all states). The caller
// filters to cspace-* / buildkit. Mirrors IP()'s shell-out + JSON-parse
// pattern with the same drift diagnostics.
func (a *Adapter) List(ctx context.Context) ([]ContainerSummary, error)
```

Runs `container ls --all --format json`, unmarshals into an internal record
type, maps to `ContainerSummary`. Parse errors reuse IP()'s version-drift
message. Unit-tested with canned JSON fixtures (no CLI needed) plus one
`CSPACE_E2E`-gated live test.

### Attach seam (important nuance)

The CLI's `attachInteractive` (`cmd_attach.go`) uses `syscall.Exec`, which
**replaces** the process — it can never return control to a TUI. The TUI needs
control back after the session ends. So we factor the argv:

```go
// attachArgs returns the container-exec argv shared by both attach paths,
// so the interactive CLI (syscall.Exec) and the TUI (tea.ExecProcess) stay
// identical. bin is the resolved `container` binary path.
func attachArgs(containerName string) (bin string, argv []string, err error)
```

`attachInteractive` keeps calling `syscall.Exec(bin, argv, env)`. The TUI's
`Actor.Attach` builds `exec.Command(bin, argv[1:]...)` and hands it to
`tea.ExecProcess`, which suspends the program, runs the command wired to the
real terminal, and re-enters the TUI on exit. The `\033c` screen reset stays in
the CLI path only (the TUI repaints itself on resume).

### Down seam

`teardownSandbox` (`cmd_down.go`) already encapsulates teardown. The TUI's
`Actor.Down` calls it with the selected project/name and `wipeState=true`
(matching the default `cspace down`), capturing its `io.Writer` output into a
buffer surfaced in the footer on failure. No behavior change to teardown.

## UI Layout

Alt-screen, three regions: header, scrollable list, detail pane + footer.

```
 cspace tui                          daemon: ok 1.0.0-rc.39        ⟳ 2s
─────────────────────────────────────────────────────────────────────────
 resume-redux
  ● mercury          agent: idle    q:0   192.168.64.108  16G  ↑2h14m
    ├ convex-backend            running   192.168.64.86    1G
    ├ convex-dashboard          running   192.168.64.116   1G
  ◐ browser (shared)            running   192.168.64.115   4G
 other-project
  ○ issue-42         agent: working q:1   192.168.64.99    8G  ↑12m
─────────────────────────────────────────────────────────────────────────
 mercury · session primary · idle · lastEvent result (2m ago)
 ┌ events.ndjson ────────────────────────────────────────────────────┐
 │ 04:12:03 assistant  "Running pnpm test:run…"                      │
 │ 04:12:41 result     success (turn 7)                              │
 └───────────────────────────────────────────────────────────────────┘
 [enter] attach  [s]end  [i]nterrupt  [d]own  [b]rowser restart  [q]uit
```

### Rows and glyphs

- Sandboxes are first-class rows with a state glyph:
  - `●` running — container running **and** supervisor `/status` reachable.
  - `○` stopped — in the registry but no running container.
  - `◐` degraded — container running but supervisor `/status` unreachable or
    erroring (agent column shows `?`).
- Compose sidecars (from `container ls`, matched by the
  `cspace-<project>-<sandbox>-*` name prefix) nest under their sandbox as
  `├`/`└` children, showing name, state, IP, memory — no agent column, not
  independently actionable.
- The shared browser sidecar (`cspace-<project>-browser`) is a project-level
  row labeled `browser (shared)`, selectable for the browser-restart action.
- `buildkit` and any other non-cspace container the CLI reports are shown
  dimmed at the very bottom under a `— system —` divider; never actionable.
- Projects with a registry entry but zero running containers still render (all
  rows `○`), so a fully-stopped project is visible.

### Selection & navigation

- `↑`/`↓` or `k`/`j` move the selection across **selectable** rows (sandboxes +
  browser rows; sidecar children and system rows are skipped).
- The selected row is highlighted; the detail pane and footer reflect it.
- Action keys are contextual — shown enabled only when valid for the selection:
  - `enter` (attach), `d` (down): sandbox rows in any container state with a
    running container; disabled (dimmed) on `○` stopped rows.
  - `s` (send): sandbox rows whose supervisor is reachable.
  - `i` (interrupt): sandbox rows whose agent state is `working`.
  - `b` (browser restart): a browser row, or a sandbox row (acts on that
    project's browser).
- Global keys: `r` force-refresh now, `q`/`ctrl-c` quit, `?` toggles a help
  overlay listing all keys.

### Detail pane

For the selected sandbox: one status line (`<name> · session <id> · <state> ·
lastEvent <type/subtype> (<age> ago)`) and a live tail box of the last N
(default 8) lines of its `events.ndjson`, each rendered as `HH:MM:SS <type>
<summary>`. For a browser row, the detail pane shows the sidecar's last known
health probe result instead. For a `○` stopped sandbox, `no running agent`.

### Footer

Contextual action hints on the normal line. Transient states replace it:
- **Confirm** (`down mercury? [y/N]`) — `y` runs, any other key cancels.
- **Input** (`send to mercury› ‹text›`) — a one-line text field; `enter`
  submits, `esc` cancels.
- **Action running** (`restarting browser…`, `tearing down mercury…`) — a
  spinner; action keys ignored until it resolves.
- **Notice** — success (fades after ~3s) or error (persists until next keypress).

## Data Flow

A `tea.Tick` every 2s (configurable via `--interval`, floor 1s) issues a poll
`tea.Cmd`. Only one poll is in flight at a time (a new tick while a poll runs is
a no-op); polling is **paused** while a confirm/input modal or an action is
active, so the list can't shift under the user mid-decision, then resumes.

Each poll produces a `Snapshot`:

1. `Adapter.List(ctx)` — one shell-out, all containers.
2. `registry.List()` — cheap file read.
3. Parallel `GET /status` to each **running** sandbox's supervisor (200ms
   timeout each, bounded by a small worker pool), Bearer-authed.
4. Daemon `GET /health` (200ms timeout).
5. Correlate by container name → project/sandbox rows with nested sidecars.

The event tail is read **only for the selected sandbox**, separately from the
poll (on selection change and on each tick): last N lines by seeking near EOF.
Rotation is handled by detecting size-shrink or inode change and re-reading from
the new generation's start. Missing file → `no events yet`.

`Update` merges each `Snapshot` into the model, preserving the current
selection by (project, name) identity — if the selected row vanished (e.g. after
`down`), selection moves to the nearest remaining selectable row.

## Error Handling

Every data source fails independently; the TUI renders what it has and marks the
rest stale rather than blanking:

- **Daemon unreachable** → header `daemon: unreachable` (red); the version drops
  to `?`. List still renders from `container ls` + registry.
- **`container ls` fails** (apiserver down) → a full-width banner with the error
  text and `run cspace doctor`; the list keeps last-known rows, dimmed with an
  `as of HH:MM:SS` marker.
- **A supervisor `/status` times out** → that row becomes `◐`, agent column `?`;
  container data still shows.
- **Actions** surface outcomes in the footer using the CLI's existing error
  text (`restartErrorText`, `agentErrorText`) — no new error vocabulary. A
  `POST /interrupt` 409 ("no active task") is a transient notice, not an error
  state.
- **Event tail** is best-effort: missing/rotating file → `no events yet`, never
  an error.
- **Quit is always clean**: `q`/`ctrl-c` restores the terminal even mid-poll.
  In-flight server-side actions (a running teardown) are left to finish; the TUI
  never kills one by exiting.

Only `down` is destructive, and it always confirms.

## Testing

- **Model/update logic** — the bulk of coverage. Feed `tea.Msg` sequences
  (snapshots, keypresses, action results) to `Update`; assert model state:
  selection movement skips non-selectable rows; contextual keybinding
  enable/disable; confirm flow (`d`→`y` triggers `Actor.Down`, `d`→`n`
  cancels); input flow; action-in-flight gating (keys ignored while running);
  degraded/stale derivation; selection-preservation across snapshots and
  selection-repair when a row disappears. Pure functions, no terminal, no
  containers.
- **Poller** — against fakes: a fake substrate returning canned
  `ContainerSummary` slices, an `httptest` supervisor + daemon (the
  `cmd_agent_test.go` pattern), a temp-dir `events.ndjson` for tail + rotation
  cases. The `Poller` seam is an interface; tests never touch the host.
- **`Adapter.List`** — canned-JSON unit tests for the parse/map, plus one
  `CSPACE_E2E`-gated live test.
- **View** — a handful of `strings.Contains` assertions on rendered output for
  load-bearing states (degraded row, confirm footer, stale banner, `no events
  yet`), not pixel-perfect golden files (which churn with width/lipgloss).
- **Actors** — assert the right seam call with the right args (fake
  `Actor` records calls); the real attach/down/restart paths are already tested
  elsewhere. No live-host integration test for the TUI itself.

## Files

- Create: `internal/tui/model.go`, `internal/tui/poll.go`,
  `internal/tui/view.go`, `internal/tui/actions.go` and their `_test.go`
  siblings.
- Create: `internal/cli/cmd_tui.go`.
- Modify: `internal/cli/root.go` (register `newTUICmd`).
- Modify: `internal/cli/cmd_attach.go` (extract `attachArgs`; `attachInteractive`
  calls it).
- Modify: `internal/substrate/applecontainer/adapter.go` (+`List`,
  `ContainerSummary`) and `adapter_test.go`.
- Docs: a `cspace tui` line in `CLAUDE.md`'s Commands section and README.

## Open Questions

None blocking. Deferred to a possible v2: booting via a `up` form, multi-select
bulk actions, log search, and per-sandbox resource graphs.
