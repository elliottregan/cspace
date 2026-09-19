# cspace control plane: embedded terminals over the existing CLI

- **Date:** 2026-09-17
- **Status:** approved design, not yet implemented
- **Supersedes:** `2026-07-20-cspace-tui-design.md` (the bubbletea v1 dashboard,
  which this design deletes rather than migrates)
- **Related:** branch `claude/happy-goodall-737o7w` (`experiments/`), the Rust
  multiplexer exploration whose findings shaped this; its audit document
  (`experiments/QUALITY_FINDINGS.md` §11–§12) records what that prototype got
  wrong and why the Go stack chosen here passes the same probes.

## Problem

cspace has a dashboard (`cspace tui`) and an attach command, but no place
where a person can watch several sandboxed agents at once, type into one, run
a shell beside it, and see cspace's own data — lifecycle, agent state, ports —
without leaving the window. herdr provides the multiplexing half of that for
any agent CLI, and cspace runs inside it today, but it cannot show cspace's
data or take cspace's actions, and it offers no customization hook that would
let it.

The exploration branch prototyped a herdr-style multiplexer in Rust. It proved
the rendering path but failed the probes that matter for hosting Claude Code:
terminal queries went unanswered, bracketed paste lost its markers, Shift+Tab
and modifier chords were dropped, and a stalled child could freeze the UI. It
also never asked whether the same thing could be built in Go inside the
binary cspace already ships. Two spikes on 2026-09-17 answered that: it can,
and the substrate cooperates.

## Goal

One `cspace tui` window for the whole machine that is both the dashboard and
the place sessions run: a sidebar of sandboxes grouped by project carrying
cspace's data, live Claude sessions and shells in panes beside it, sessions
that survive the window closing, and the cspace actions the CLI already has.
The TUI is a wrapper: it reads and acts through one internal control API, and
cspace itself changes only where new information must be reported or new
input handled.

## Non-goals (this design)

- Drag-to-select and copy inside panes. Mouse mode disables the terminal's
  native selection; Ghostty's shift-drag still works and the help overlay
  (leader `?`) says so.
- tmux-style splits, resizable dividers, or an N-up grid. One focused pane
  with tabs; other panes stay live behind their tabs.
- Git branch, dirty state, PR and check status per sandbox.
- Context usage and cost per session (the hook file below has room for it).
- Forwarding mouse events to the child program.
- Sandboxes on other machines, a browser-rendered terminal, or a native TCP
  transport. Apple Container has no remote mode; this stays gated on the
  Docker substrate work.
- Migrating the `cspace up` overlay. It stays on bubbletea v1, which coexists
  with v2 under a different module path.

## Decisions, and the evidence behind them

Each of these was verified on this host on 2026-09-17, under a Python pty
harness with a control run per probe, against a throwaway container started
from the local `cspace:latest` image. The scripts and captures are throwaway;
the facts are what this design rests on.

| Decision | Evidence |
|---|---|
| Build in Go, inside the cspace binary, on bubbletea v2 + lipgloss v2 + `charmbracelet/x/vt` + `creack/pty`. | A 700-line spike passed every probe the Rust prototype failed: per-pane query answering, bracketed paste, kitty CSI-u keys with correct degradation, exact resize, UI isolation from a stalled child, clean quit. |
| Persistence comes from tmux inside the sandbox, not a host daemon. | tmux 3.3a (Debian bookworm) in the image: a shell kept its PID across host-side death and reattach, the screen was redrawn, two clients at different sizes shared one session. |
| `extended-keys always`, never `on`. | With `on`, Shift+Enter in CSI-u form was silently swallowed, not even downgraded to Enter. With `always` it passed byte-for-byte. |
| Closing a pane must explicitly detach its tmux client. | Killing the host-side `container exec` client left the guest tmux client attached indefinitely; `tmux detach-client -t <tty>` reaped it at once. A plain exec'd process likewise survived its host terminal closing 30 s later, which means today's `cspace attach` orphans `claude` on every closed window. |
| Host-side resize propagates through `container exec`. | `pty.Setsize` on the host changed `stty size` inside the guest with no fallback needed. |
| Image paste needs no new dependency. | `osascript` wrote the clipboard's PNG to a file and read it back byte-identical. |
| Interactive agent state comes from Claude Code hooks. | Hook events and their fields are documented; `Stop` does not fire on user interrupt, so an activity heuristic backs it. |

Known costs of the Go stack, accepted:

- `x/vt` is untagged and self-described as experimental. Pin a pseudo-version,
  keep it behind an interface, expect to carry a patch.
- `x/vt.Emulator.SendKey` does not encode modified keys (its source carries a
  TODO); the pane engine carries a ~170-line overlay for them.
- `x/vt` allocates a fixed 4 MiB parser buffer per emulator; two runs of the
  spike's memory probe measured roughly 1.5 to 3 MB of RSS per additional open
  pane, host-dependent. Panes are opened on demand, not one per sandbox.
- `x/vt.SafeEmulator` does not guard `Close`; the race detector flags a
  teardown that closes while a `Read` is in flight, and the spike's own
  teardown still trips it. The handshake below is the proposed mitigation;
  the pane tests run under `-race` and the mitigation counts as done only
  when they are clean. If it cannot be made clean from outside the library,
  the fix is a patch to `x/vt` carried in `go.mod` via `replace`.

## Architecture

Three layers in one binary. Each layer calls only the one below it.

```
internal/controlplane   bubbletea v2 UI: layout, focus, keys, mouse, paste
        │
        ├── internal/pane       PTY + emulator + goroutines; knows nothing of cspace
        │
        └── internal/control    queries and actions; pure Go, no terminal code
                │
                registry · substrate/applecontainer · supervisor HTTP · session dirs
```

`internal/tui` and `internal/cli/tui_actor.go` are deleted. `cspace tui` keeps
its name and becomes this. Any function the CLI commands implement that the
TUI also needs moves into `internal/control` so there is one implementation;
a CLI `--json` flag can later expose any control query for free.

### `internal/control`

Queries, each a plain function returning plain data:

- `Snapshot()` — every sandbox on the host grouped by project: lifecycle
  (stopped / booting / running / degraded), memory cap and usage, uptime,
  nested compose sidecars, the project's browser sidecar and its health,
  daemon health. This is the old dashboard's poller and correlation fold
  moved over with their tests. Sources: the registry file, `container ls`,
  `container stats` (slow, on its own cadence), the daemon's `/health`.
- `AgentStatus(sandbox)` — the supervisor's authenticated `GET /status`,
  probed concurrently with a short timeout, as today.
- `InteractiveState(sandbox)` — the hook-written state file (below).
- `Ports(sandbox)` — labeled listeners: labels from `devcontainer.json`
  `portsAttributes`, else `.cspace.json` `container.ports`; live listeners
  from an `ss -tln` exec into the sandbox; the statusline's curation rule
  (hide unlabeled ports only when the project labeled any); each rendered
  as `http://<sandbox>.<project>.cspace.test:<port>/` when the resolver is
  installed, else the IP form.
- `Events(sandbox, n)` — the tail of `events.ndjson`, tolerant of a partial
  last line.

Actions: `AttachArgv(sandbox, session)`, `DetachClient(sandbox, tty)`,
`ListClients(sandbox)`, `Down`, `Send`, `Interrupt`, `RestartBrowser`, `Up`.
`AttachArgv` owns the TERM/COLORTERM mapping that `cmd_attach.go` holds today.

### `internal/pane`

One `Pane` is a PTY (creack/pty) and an emulator behind this interface:

```go
type Emulator interface {
    Write([]byte) (int, error)   // PTY output in
    Read([]byte) (int, error)    // responses the emulator wants sent to the PTY
    Resize(cols, rows int)
    Render() string              // the visible screen as styled text
    CursorPosition() (x, y int)  // where the child left the cursor; tea.View places the terminal cursor from it
    SendKey(KeyEvent)            // encodes and queues a key for the child
    Paste(string)                // brackets when the child asked for it
    Scrollback() Scrollback
    Close() error
}
```

The `x/vt` implementation is the only one; the interface exists so a vendored
vt10x or go-libghostty can replace it without touching the UI. `x/vt`'s
`SendKey` returns nothing and silently drops the modifier combinations it
does not implement, so the adapter decides from a static table of key and
modifier which keys `x/vt` encodes correctly and which go through the
overlay; the table is what the spike's `keys.go` hand-codes today. `Paste`
brackets on its own from the emulator's mode state; no separate query is
needed.

Four goroutines per pane: output pump (PTY → emulator), response drain
(emulator → PTY, for query answers), writer (a bounded channel drained into
the PTY, enqueued without blocking so a child that stops reading stalls only
its own queue), and process waiter. The kitty keyboard protocol is tracked by
registering a CSI `u` handler on the emulator; the key overlay encodes
modified keys as CSI-u when the child has kitty on, xterm modifier forms
otherwise, and degrades a modified legacy key to its plain byte when neither
applies, which is what a real terminal does.

Teardown handshake: stop accepting input, end the child's process group and
reap it, send the group one more unconditional `SIGKILL` (a member that
ignored the first `SIGHUP` — bash gives a backgrounded job in a
non-interactive script `SIG_IGN` for it, for instance — would otherwise
survive), close the PTY, then `Close` the emulator once via `sync.Once` and
join the response drain. What actually returns a writer parked in a PTY
write, or the output pump parked in a PTY read, is the child dying, just
reaped above — not the PTY close that follows it: creack/pty's master
descriptor is a plain blocking fd outside Go's runtime poller, so closing it
only marks it closed and does not itself return an in-flight read or write —
a probe against a real pane confirmed both stayed parked seconds after
`Close` returned and were released the instant the child was killed. The PTY
close still has to run, and be joined, before the emulator's `Close`,
because it and the output pump's own exit are what guarantee no pump write
into the emulator is in flight when that runs. On Linux, which has no
tty-revoke equivalent to macOS's, a setsid grandchild that ignored `SIGHUP`
and still holds the PTY's slave side open could leave the output pump parked
even after the direct child is reaped, which is what the extra group
`SIGKILL` above closes. The response drain cannot be cancelled first: it is
parked inside x/vt's unbuffered input pipe, and only closing that pipe
returns it. The property the original ordering was buying — a `Close` that
cannot race a `Read` — is bought instead by the adapter's `Close`, which
closes the emulator's input pipe (synchronized by `io.Pipe`) rather than
calling x/vt's own `Close`, which writes an unguarded `closed` bool that
`Read` reads. The scrollback is read under the adapter's own lock for the
same reason: `SafeEmulator` wraps the accessor but not the buffer it
returns. `make test-race` is the acceptance check for both.

Host-shell panes use the engine with `$SHELL` and no container exec.

### `internal/controlplane`

Layout, fixed geometry: a 24-column sidebar on the left; on the right a tabs
row, the main area, and a one-line footer.

```
┌ sandboxes ─────────┬ resume-redux/mercury · shell · host ───────────────┐
│ ▾ resume-redux     │                                                     │
│   ● mercury  5173  │                                                     │
│     ├ convex       │              focused pane                           │
│   ○ issue-42       │                                                     │
│   ✓ browser        │                                                     │
│ ▾ cspace           │                                                     │
│   ▲ venus          │                                                     │
├────────────────────┴─────────────────────────────────────────────────────┤
│ ⌃Space h sidebar · n/p tabs · t new · x close · [ scroll · v paste image │
└──────────────────────────────────────────────────────────────────────────┘
```

Sidebar rows: project headers, sandboxes with one state glyph, their labeled
ports as hyperlinks, nested sidecars dimmed, the browser row. The glyph is
decided by lifecycle first, then agent state: stopped `✕`, booting `◐`,
degraded (running, supervisor unreachable) `!`; a running sandbox shows the
interactive session's state when a Claude pane or `cspace attach` session
exists (`●` working, `○` idle, `▲` needs input), else the supervisor's state
(`●` working, `○` idle), else `○`. Selecting a
sandbox shows a detail band: uptime, memory, agent session and last event,
URLs.

Tabs: one per open pane, titled `<project>/<sandbox> · <kind>`.

Rollout step 3 places both of those differently, and step 4 moves them.
With no panes to compete for it, the detail band occupies the main area —
24 columns cannot hold a URL, and `renderDetail` takes its width as a
parameter so that move is a layout change, not a rewrite. The reserved tabs
row carries the selection's title and daemon health rather than sitting
empty, because an empty line above the main area reads as a rendering bug
and not as a promise. When panes land, the band moves under the sidebar and
the row becomes the tabs it is named for.

Main area, by tab kind:

- Claude pane and shell pane: the emulator's rendered screen inside a border
  colored by state.
- Supervisor view: a viewport over the event tail with assistant text
  rendered as markdown, a text area for the next prompt (Enter sends through
  `Send`; a key interrupts), and a spinner while working.
- Host shell: the emulator screen.
- Exited pane: the last screen dimmed, with the exit reason and a restart key.

Widgets: `bubbles/v2/tree` for the sidebar, decided at implementation: if it
cannot express non-selectable project headers with selectable children, the
sidebar is a small hand-rolled model rendered with lipgloss v2's `tree`
package instead; lipgloss v2 for tabs and borders, `bubbles/v2/help`
and `key` for the footer and configurable bindings, `viewport`, `textarea`,
`spinner` and `glamour/v2` for the supervisor view, `huh/v2` for the down
confirmation and the new-pane picker, lipgloss v2 `Hyperlink` for URLs. Mouse
hit-testing is arithmetic over the fixed geometry; `bubblezone` is not used.

## Sandbox side

### Image

`lib/templates/Dockerfile` installs `tmux` (3.3a on bookworm) and COPYs
`lib/runtime/tmux.conf` to `/usr/local/etc/cspace-tmux.conf` and
`lib/runtime/scripts/cspace-agent-state.sh` to `/usr/local/bin/` — one COPY
line each, per the builder's no-recursion rule. `make sync-embedded` gains a
rule for `tmux.conf` (no existing glob covers `lib/runtime/*.conf`); the
script is already swept by the `lib/runtime/scripts/*.sh` glob.

`tmux.conf`; every line was checked for acceptance on tmux 3.3a, and the
input-handling lines (`extended-keys`, `default-terminal`,
`terminal-overrides`) were verified behaviourally by the spike:

```
set -g extended-keys always      # "on" silently drops Shift+Enter (CSI-u)
set -g allow-passthrough on
set -s escape-time 0
set -g status off
set -g mouse off
set -g prefix None               # the host owns every key
set -g default-terminal tmux-256color
set -ga terminal-overrides ',xterm-256color:RGB'   # matches the outer TERM attach passes
set -g focus-events on
set -g history-limit 2000        # serves the reattach redraw only
```

`extended-keys-format` is not set: the passthrough does not need it, and
tmux 3.3a does not know the option. No key bindings of any kind.

### Sessions

One tmux session per pane kind per sandbox:

| Session | Command | Created by |
|---|---|---|
| `cspace-claude` | `claude --dangerously-skip-permissions` in `/workspace` | first attach |
| `cspace-shell` | the image's login shell in `/workspace` | first shell pane |

Attach-or-create is one argv, shared by `cspace attach` and the control
plane:

```
container exec -it -e TERM=<mapped> -e COLORTERM=<host> <container> \
  tmux -f /usr/local/etc/cspace-tmux.conf new-session -A -s <session> -c /workspace <command…>
```

`-A` attaches when the session exists and ignores the command; the command
runs only on creation. When it exits the session ends, the client exits, and
the pane shows the exited state. Separate sessions per kind mean two panes on
one sandbox never contend for a current window; `cspace attach` from any
terminal joins `cspace-claude` and shares its screen.

### Detach protocol

A dead host side never reaches the guest, so every pane close and every
`cspace attach` exit runs an explicit detach:

1. Take the sandbox's attach lock, `flock` on
   `~/.cspace/controlplane/<project>/<sandbox>/attach.lock`. The lock is a
   file because the control plane and a separately started `cspace attach`
   are different processes attaching to the same sessions.
2. List the session's clients (`tmux list-clients -F '#{client_tty}'` via a
   plain `container exec`), attach, and when the first bytes arrive list
   again. The one new tty is this client's. Write one record file per
   client, `~/.cspace/controlplane/<project>/<sandbox>/<session>.<tty>.json`
   holding session, tty, the host pid, and a timestamp. Release the lock.
3. On close: stop the writer, run `tmux detach-client -t <tty>` via a plain
   `container exec`, then run the pane teardown handshake, then delete that
   client's record file.
4. On control-plane startup: for every record file under
   `~/.cspace/controlplane/` whose host pid is no longer running and whose
   tty tmux still lists, detach it and delete the file. This covers a crash
   of the control plane or of `cspace attach`.

`cspace attach` stops using `syscall.Exec`. It runs the exec as a foreground
child with the terminal's signals flowing to it, and on the child's exit or
its own SIGHUP performs step 3. This is the fix for the orphaned `claude`
processes today's attach leaves behind.

### Agent state hooks

The entrypoint's settings seed gains a `hooks` block whose every entry runs
`cspace-agent-state.sh <state>`. The script writes
`/sessions/agent-state.json` atomically (write a temp file, rename):

```json
{"state":"working","at":"2026-09-17T18:40:12Z","session_id":"…","event":"UserPromptSubmit"}
```

| Hook event | Matcher | State |
|---|---|---|
| `SessionStart` | `startup\|resume\|clear` | `starting` |
| `UserPromptSubmit` | | `working` |
| `PostToolUse` | | `working` (a tool finished, Claude continues; also clears a `needs-input` once the question or permission was answered) |
| `PreToolUse` | `AskUserQuestion` | `needs-input` |
| `PermissionRequest` | | `needs-input` |
| `Notification` | `idle_prompt` | `idle` |
| `Stop`, `StopFailure` | | `idle` |
| `SessionEnd` | | `exited` |

No event has two entries, so no two hooks race to write different states
for one event. Hooks matching the same event run in parallel, which is why
`PreToolUse` is matched only on `AskUserQuestion` and the generic "working"
comes from `PostToolUse` instead. `SessionStart`'s matcher excludes `compact`
and `fork` so a mid-turn context compaction or a session fork doesn't flip an
already-`working` session back to `starting`.

`/sessions` is the per-sandbox session directory already bind-mounted from
`~/.cspace/sessions/<project>/<sandbox>/`, so the host reads the file
directly. `Stop` does not fire on a user interrupt, so the pane keeps the
output-activity heuristic: the file's `idle` or `needs-input` is shown only
once the pane has produced no output for three fast-ticker periods (3 s by
default); otherwise the pane is `working`. The statusline command already runs on every assistant message
with context percentage and cost on stdin; when that data is wanted it is one
more field written by the same script, not a new mechanism.

## Data flow and cadence

The UI runs one poll loop with three tickers, each dispatching a control
query and delivering a message:

| Ticker | Queries | Default |
|---|---|---|
| fast | `InteractiveState`, `AgentStatus` | 1 s |
| medium | `Snapshot` without stats, `Events` for the open supervisor view | 2 s |
| slow | `container stats`, `Ports` | 10 s |

Pane output does not go through the poll loop: each pane's output pump
schedules a redraw, coalesced to at most ~30 per second. A poll failure
degrades the fields it feeds and shows the error in the footer; the sidebar
never blanks. If polling proves heavy, the state files can move to an
`fsnotify` watcher and the container list to the daemon's memoized inspect
without changing any interface above.

## Input

Focus is on the sidebar or on the main area.

Sidebar focused: arrows or `j`/`k` move; `Enter` opens or focuses the
sandbox's Claude pane; `s` shell pane; `a` supervisor view; `d` down (huh
confirm); `b` browser restart; `u` up; `Tab` focuses the main area.

Main area focused: every key goes to the pane except the leader. Leader is
`Ctrl+Space` by default, declared with `bubbles/v2/key` and overridable in the
user-level cspace config along with every other binding. It must not be
`Ctrl+b`, which Claude Code uses to background a task.

Leader then: `h` focus sidebar · `n` / `p` next / previous tab · `t` new-pane
picker (Claude, shell, supervisor, host shell) · `x` close pane (detach
protocol) · `[` scroll mode (arrows, PageUp/Down, wheel move the scrollback;
any other key returns to live) · `g` back to live · `v` image paste · `?`
help overlay (the full binding list from `bubbles/v2/help`, plus the
selection note) · `q` quit · leader again sends the leader to the child. Quit does not confirm:
tmux holds every session.

Mouse: cell-motion mode. Click selects a sidebar row or a tab or focuses the
main area; the wheel scrolls the focused pane's scrollback or the sidebar.
Nothing is forwarded to the child.

Paste: text paste events go to the focused pane through `Emulator.Paste`,
which brackets them when the child asked. Image paste (leader `v`) runs
`osascript` to write the clipboard's PNG to
`~/.cspace/sessions/<project>/<sandbox>/paste/<timestamp>.png`, then types
`/sessions/paste/<timestamp>.png` into the pane with no trailing newline. An
empty or text-only clipboard falls back to a text paste.

## Error handling

- Exec failure for a pane: the pane area shows the error and a retry key.
  Other panes and the sidebar are unaffected.
- Sandbox image without tmux (built before this change): detected once per
  sandbox by probing for the binary; the pane and `cspace attach` fall back
  to the direct `claude` exec with a footer warning naming
  `cspace image build`. The stale-image gate in `cspace up` prompts for the
  rebuild as it does for any image bump.
- Detach failure: logged, and retried by the startup sweep.
- Supervisor unreachable: send and interrupt are disabled on that row rather
  than failing on press.
- Poll failure: degrade the affected fields, footer message, keep the last
  good values.
- Panic anywhere in the UI: bubbletea's recovery restores the terminal; tmux
  keeps the sessions; the next start sweeps the orphaned clients.

## Testing

- `internal/pane`: Go tests against a real PTY running a small inner script,
  porting the spike's probes: CPR answered from the pane; bracketed paste
  markers preserved; the nine key sequences with kitty off and on; resize
  arithmetic; an 8 KiB paste into a non-reading child leaves the pane
  responsive and quittable; teardown under `-race`. Also an interface test
  suite any `Emulator` implementation must pass.
- `internal/control`: the inherited correlation tests; `Ports` label and
  curation rules with fixture files; `InteractiveState` parsing including a
  torn write; argv golden tests including the TERM mapping table.
- `internal/controlplane`: model tests as today — messages in, golden views
  out — for sidebar grouping, focus transitions, leader dispatch, tab
  lifecycle, exited-pane rendering, and the supervisor view.
- `lib/runtime/scripts/cspace-agent-state.sh`: bash test under
  `make test-scripts`, including the atomic write.
- Integration, gated on Apple Container: attach, detach, reattach, concurrent
  attach, and the client sweep against a throwaway container from
  `cspace:latest`, as the substrate spike did. Run by hand and in the release
  dry run; not in CI.

## Rollout

1. **Sandbox side.** tmux, its config, the state script and hooks in the
   entrypoint seed, and `cspace attach` on the tmux argv with detach on exit.
   Independently useful, fixes the orphan bug, bumps the image.
2. **Control API.** Extract `internal/control` from the dashboard's data code
   with its tests; the old dashboard is repointed at it and keeps working.
3. **Dashboard on v2.** New `cspace tui` with sidebar, detail band, actions,
   and no panes, landing in the same change that deletes `internal/tui` and
   `tui_actor.go`, so `cspace tui` is never absent from `main`.
4. **Panes.** `internal/pane`, the Claude, shell and host-shell panes, the
   supervisor view, the detach protocol and sweep.
5. **Mouse and image paste.**

Each step lands on its own and leaves `make check` green.

Dependencies added: `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`,
`charm.land/bubbles/v2`, `charm.land/huh/v2`, `charm.land/glamour/v2`,
`github.com/charmbracelet/x/vt` at a pinned pseudo-version,
`github.com/creack/pty`. `github.com/charmbracelet/bubbletea` v1 stays for the
overlay. CI is unchanged: Go only.

Upstream issues to file against `charmbracelet/x`: `vt.Emulator.SendKey`
drops modified keys; `vt.SafeEmulator.Close` is unsynchronized with `Read`;
the 4 MiB parser buffer is not configurable. Findings to file in
`.cspace/context/findings/`: exec'd processes survive their host terminal
closing (today's `cspace attach` orphans `claude`); statusline port links
move to the control plane and the OSC 8 block in `statusline.sh` can be
simplified back to text once step 3 lands.

## Open questions

Deliberately few; each has a default that the plan proceeds with.

1. Tab title length with many open panes: default to truncating from the
   left and showing a count; revisit after use.
2. Whether the supervisor view should render tool calls or only assistant
   text: default to assistant text plus one-line tool summaries, as the
   events stream already distinguishes them.
3. Whether `cspace attach` should keep a `--no-tmux` escape hatch after the
   fallback path exists: default yes, undocumented, removed once no image
   without tmux is in use.
