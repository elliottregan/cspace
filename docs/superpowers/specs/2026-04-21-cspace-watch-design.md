# cspace watch — TUI Visualizer Design

**Date:** 2026-04-21

## Overview

`cspace watch` is a live terminal dashboard for observing a cspace project's running state: which containers are up, what agents are doing, open ports, and the health of shared services. It is read-only in v1 and designed to accept interactive keyboard commands (interrupt, send) in a future iteration.

---

## Architecture

`cspace watch` is a new Cobra command that acts as a pure view layer on top of the existing diagnostics infrastructure.

**Startup sequence:**
1. Check if diagnostics server is alive (`GET :8384/health`).
2. If not running, start it in-process using the existing `RunDiagnosticsServer()` function before launching the TUI.
3. Connect WebSocket to `ws://localhost:8384/ws` and subscribe to all agents (`{"subscribe":["*"]}`).
4. Start HTTP poll ticker (2s) against `GET :8384/agents` for full `AgentSnapshot` state.
5. Start Docker health poll ticker (30s) querying shared service container status.
6. Launch Bubbletea program.

The TUI holds no authoritative state — everything is received from the diagnostics server. When interactivity is added later, keyboard commands map directly to WebSocket messages (`{"cmd":"interrupt","agent":"<name>"}`, `{"cmd":"send","agent":"<name>","text":"..."}`) that the server already supports.

---

## Layout

Split view with a fixed-width left panel and a scrollable right panel.

```
┌─ cspace watch · my-project · 4 agents ──────────── $0.287 total · ● ws connected ─┐
│ Agents               │ Activity stream [all agents]          ↑↓ scroll · auto on   │
│ ● mercury  advisor   │ 14:33:12  earth    Bash   npm test --testPathPattern=auth   │
│   active · t:12 $0.04│ 14:33:09  earth    text   Running auth tests before ship.  │
│   dev:3001  vnc:5901  │ 14:33:01  mercury  Read   internal/auth/middleware.go      │
│                       │ 14:32:55  mercury  text   Missing token expiry check.      │
│ ● venus    worker     │ 14:32:48  venus    result ✓ success · 8 turns · $0.021    │
│   idle · t:8  $0.02  │ 14:32:41  venus    Edit   internal/auth/token.go +12 -3   │
│   dev:3002            │ 14:32:38  venus    text   Adding expiry validation.        │
│                       │ 14:32:30  earth    Bash   git log --oneline -10            │
│ ● earth    coord      │ ...                                                         │
│   stuck · t:31 $0.19 │                                                             │
│   ⚠ Bash · 1m 12s    │                                                             │
│   dev:3003            │                                                             │
│                       │                                                             │
│ ● mars     worker     │                                                             │
│   exited · t:5 $0.03 │                                                             │
│──────────────────────│                                                             │
│ Shared services       │                                                             │
│ ● traefik      :80   │                                                             │
│ ● coredns      :53   │                                                             │
│ ● playwright   :9323 │                                                             │
│ ◐ chromium-cdp :9222 │                                                             │
│──────────────────────│                                                             │
│ Diagnostics           │                                                             │
│ ● diagnostics  :8384 │                                                             │
├───────────────────────────────────────────────────────────────────────────────────┤
│ q quit  ↑↓ scroll  f filter agent  [i interrupt  s send — coming soon]            │
└───────────────────────────────────────────────────────────────────────────────────┘
```

**Left panel (~36% width, fixed):**

- **Agents section**: one block per agent, sorted by status priority (stuck → active → idle → exited). Each block shows:
  - Name, role (advisor / coordinator / worker / agent)
  - Colored status dot + label: green=active, yellow=idle, red=stuck, gray=exited
  - Left border colored by status (2px)
  - Turns, cost, git branch
  - Port tags: `label:port` for each binding
  - Stuck warning line: `⚠ <tool> pending · <elapsed>`
- **Shared services section**: global proxy containers (Traefik, CoreDNS) and shared sidecars (Playwright, Chromium CDP). Each row: status dot, name, host port (if bound), optional label. Containers with no host port binding (e.g. Playwright, which is network-only) show only the status dot and name.
- **Diagnostics section**: diagnostics server itself.

Agent status dot colors:
- `active` — green (`#3fb950`)
- `idle` — yellow (`#e3b341`)
- `stuck` — red (`#f85149`), block gets a red left border and faint red background
- `exited` / `unknown` — gray (`#6e7681`)

**Right panel (remaining width):**

Scrollable activity stream. One row per event, columns: `time  instance  type  content`.

Event types shown:
- `tool_use` — tool name + first 60 chars of input (truncated with `…`)
- `assistant` text blocks — assistant reasoning/commentary
- `result` (session end) — green summary row: `✓ success · N turns · $X.XXX` or `✗ failure · …`

Events not shown by default: raw `tool_result` content, system messages. This keeps signal-to-noise high.

Auto-scroll: stream follows the tail. Pauses when user scrolls up; resumes on `G` or `End`.

Activity cap: 1000 rows. Oldest rows evicted first.

**Title bar**: project name, agent count, total cost across all agents, WebSocket connection status.

**Status bar**: keyboard hints. `i` and `s` are shown in dim gray (future). "Updated Ns ago" timestamp on the right.

---

## Data Flow

```
Docker daemon ──── (30s) ──→ docker.go → ServicesUpdatedMsg
                                              │
diagnostics server:8384                       │
  GET /agents ──── (2s) ──→ wsclient.go → AgentsUpdatedMsg
  WS /ws ─────── (live) ──→ wsclient.go → EventReceivedMsg
                                              │
                                         Bubbletea Update()
                                              │
                                         model.View()
                                              │
                                         terminal output
```

The WebSocket client runs in a dedicated goroutine. On `EventReceivedMsg`, the model appends to the event ring buffer and re-renders. On `AgentsUpdatedMsg`, the model replaces the agent list and re-renders. On `ServicesUpdatedMsg`, the model replaces the service list.

WebSocket reconnect: on disconnect, the client waits 2s and retries indefinitely. Title bar shows `● ws reconnecting…` in yellow while disconnected.

---

## Bubbletea Messages

```go
// tea.Msg types in internal/tui/messages.go

type AgentsUpdatedMsg  []diagnostics.AgentSnapshot
type EventReceivedMsg  diagnostics.Envelope
type ServicesUpdatedMsg []ServiceStatus
type TickMsg           time.Time  // 2s HTTP poll trigger
type WSStatusMsg       struct{ Connected bool }
```

`ServiceStatus` is a lightweight struct defined in `internal/tui/docker.go`:
```go
type ServiceStatus struct {
    Name      string  // "traefik", "playwright", etc.
    Port      string  // ":80"
    Label     string  // "proxy", "dns", "browser", "cdp"
    Running   bool
    Slow      bool    // warn state: running but unhealthy/slow
}
```

---

## File Structure

```
internal/cli/watch.go        — newWatchCmd(), startup logic, Bubbletea launch
internal/tui/
  model.go                   — root Bubbletea Model, Init/Update/View
  left.go                    — left panel View function
  right.go                   — right panel View function (activity stream)
  styles.go                  — Lipgloss color/style definitions
  messages.go                — all tea.Msg types
  wsclient.go                — WebSocket connect/reconnect, event pump
  docker.go                  — shared service health checks, ServiceStatus type
```

`internal/cli/root.go` — add `rootCmd.AddCommand(newWatchCmd())`.

---

## `f` Filter

Single keypress cycles: `[all] → mercury → venus → earth → [all]`. No modal or input required. The activity header updates to show the active filter. The left panel highlights the selected agent (bold name or cursor indicator) for future use as the interactive target.

---

## Interactivity Scaffold

The following are designed in but not implemented in v1:

- **`i` — interrupt**: sends `{"cmd":"interrupt","agent":"<focused>"}` over the WebSocket. Stub: key captured but displays "not yet available" in status bar.
- **`s` — send**: opens a single-line text input (Huh or inline) and sends `{"cmd":"send","agent":"<focused>","text":"..."}`. Stub only.
- **Arrow keys / Enter**: navigate the agent list (focus changes the `f` filter target). Navigation works in v1; actions are stubbed.

The focused agent index is tracked in the model from v1. This keeps the interactive path clean when the time comes.

---

## Dependencies

- `github.com/charmbracelet/bubbletea` — TUI event loop
- `github.com/charmbracelet/lipgloss` — styling, layout
- `github.com/docker/docker/client` — shared service health. Verify at implementation time whether the Docker client is already a module dependency; if so, no new import needed.

No new external dependencies beyond Charm libs, which are not currently in the module. They will need to be added.

---

## Out of Scope (v1)

- Port URL opening (OSC 8 hyperlinks or `open`/`xdg-open` shortcut)
- Per-agent drill-down view
- Cost graphs or sparklines
- Session log tailing beyond the WebSocket stream
- Multi-project view
