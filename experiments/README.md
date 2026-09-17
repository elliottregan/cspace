# cspace multiplexer/UI exploration (draft)

Status: exploratory, not wired into the Go CLI or `make check`. This
document is a snapshot of an in-progress design conversation, not a spec —
expect it to be wrong in places and to change as the prototypes evolve.

See [`QUALITY_FINDINGS.md`](./QUALITY_FINDINGS.md) for a productionization
audit — maintenance/performance/security risks identified across all three
prototypes, with no proposed fixes, as prep for turning this exploration
into a real cspace feature.

## Motivation

cspace today is a Go CLI + a bubbletea boot overlay/dashboard (`cspace tui`)
around Apple Container sandboxes, each running a Claude Code agent via
either an interactive `cspace attach` (execs `claude` directly into the
sandbox's TTY) or a headless Bun supervisor (`cspace send`, driven over an
authenticated HTTP control port, with a structured `events.ndjson` log of
the agent SDK's own message stream).

Looking at products that wrap coding-agent CLIs in a custom UI (t3code,
herdr, rmux) surfaced three things cspace doesn't have yet that would be
genuinely useful:

- **Terminal access alongside an agent** — running `git push` yourself
  without asking the agent to do it, without leaving the sandbox context.
- **Image paste into a session** — doesn't work today because the sandbox
  is isolated from the host clipboard; needs a wrapper UI in the loop to
  bridge clipboard bytes into the sandbox's filesystem.
- **At-a-glance visibility across multiple sandboxes** — which agents are
  working vs. blocked, without switching to each one.

The three things we're **not** trying to replace, and every design below
assumes stay exactly as they are:

1. Isolated env per session (own filesystem, database, etc.)
2. Friendly URLs reachable from the host (`<sandbox>.<project>.cspace.test`)
3. The shared browser sidecar for e2e tests and MCP-driven page viewing

Everything here is a **consumer** of that existing substrate (registry,
DNS, sidecar orchestration), not a replacement for it.

## Prior art looked at

| Project | What it is | Relevance |
|---|---|---|
| [t3code](https://github.com/pingdotgg/t3code) | Web GUI wrapping Claude Code/Codex/other agent CLIs | Proved the "drive the real CLI/SDK, stream its native events into a UI" pattern instead of reimplementing an agent harness; its image-paste flow (upload → server disk → read back → inline base64 into the agent protocol) is the model for cspace's version |
| [herdr](https://github.com/herdrdev/herdr) | Rust/ratatui terminal multiplexer purpose-built for AI coding agents | Proved the "real PTYs multiplexed in one binary, local control socket, screen-manifest status heuristics" pattern works in production; cspace can do better on status since it has structured events instead of screen-scraping |
| [rmux](https://github.com/Helvesec/rmux) | Rust/Tokio/ratatui general-purpose tmux-compatible multiplexer engine, daemon + IPC clients | Evaluated as a dependency (`ratatui-rmux` + `rmux-sdk`) — see comparison below |
| [tui-term](https://github.com/a-kenji/tui-term) | ratatui widget: renders a `vt100::Screen` into a `Frame` | The rendering piece `mux-prototype` is built on |

## The two prototypes

### `mux-prototype/` — hand-rolled, **current chosen direction**

Stack: `portable-pty` (spawn/own each child's PTY) → `vt100::Parser`
(terminal emulation: colors, cursor, scrollback) → `tui-term`'s
`PseudoTerminal` widget (renders `vt100::Screen` into ratatui) → hand-rolled
sidebar/tabs/leader-key multiplexer logic.

Everything lives in one process. No daemon, no IPC, no second binary to
ship. Panes are literal child processes of the multiplexer.

Verified with an automated test (spawn a pane, type into it, assert the
vt100-parsed output contains what was typed) since a full-screen
interactive TUI can't be driven non-interactively.

Current controls:

```
Ctrl+b then n / p   next / previous pane
Ctrl+b then t       new pane (spawns another shell)
Ctrl+b then x       kill/dismiss focused pane (explicitly kills the child)
Ctrl+b then c       cycle focused pane's status override (none → blocked → done → none)
Ctrl+b then u / d   scroll focused pane's scrollback up / down
Ctrl+b then g       jump focused pane back to live output
Ctrl+b then m       toggle single-pane / grid (all panes at once)
Ctrl+b then q       quit
anything else       forwarded to the focused pane's shell
```

Gaps closed so far (see git log for detail):

- Scrollback (was hardcoded off) with scroll/jump-to-live keys
- Every pane resizes on terminal resize, not just the focused one; new
  panes spawn at the real current size instead of a hardcoded 24x80
- Working/idle status derived from real PTY output activity (a
  background-thread timestamp), not a manual toggle — blocked/done remain
  manual overrides layered on top
- Exited panes stay visible with a distinct status instead of vanishing
  silently, dismissed explicitly
- Minimal error footer instead of silently swallowing a failed spawn
- Color/formatting: confirmed **free** — `tui-term` already maps
  `vt100::Cell`'s bold/italic/underline/dim/inverse and 256-color/truecolor
  straight into ratatui `Style`, verified by reading its actual conversion
  code, not assumed
- Multi-pane grid display (`Ctrl+b m`) — see "Multi-pane grid" below

### `rmux-prototype/` — evaluated, not chosen

Stack: `rmux-sdk` (`Rmux::builder().connect_or_start()`) → `ratatui-rmux`'s
`PaneDriver`/`PaneWidget`.

Also verified working end-to-end with an automated test — genuinely
connected to a real, separately-spawned `rmux-daemon` process (confirmed
via `ps`, not assumed), created a session, typed into it via
`Pane::send_text`/`send_key` (tmux-style tokens — simpler than hand-rolling
key-to-bytes translation), and read the result back through a daemon
snapshot.

**Why not chosen**: `rmux-sdk` is architecturally forbidden (enforced by
its own crate's test suite) from linking the multiplexer engine in-process
— `connect_or_start` always spawns/attaches a *separate* `rmux-daemon`
binary over a Unix socket. Adopting it means cspace permanently runs a
second local daemon alongside `cspace daemon serve`, with its own
lifecycle/health/version-skew/packaging story (the binary isn't on `cargo
add` — building `rmux`/`rmux-daemon` from source was a separate step).
The embeddable widget itself (`ratatui-rmux`) is well-engineered — better
input ergonomics (`send_text`/`send_key` vs. hand-translating crossterm
keys) — but not worth the mandatory daemon for a per-user, one-machine
tool. `rmux`'s "Teammate Mode" Claude Code integration was investigated
and found irrelevant to cspace (it's Claude Code's own Agent-Teams feature
impersonating tmux for split-pane display, not a hook into any agent's
task/status state).

## Deliberately not attempted yet

Distinguishing genuine blockers from unbuilt-but-easy from
unbuilt-and-design-heavy, since that distinction changes what's safe to
commit to building next:

| Feature | Blocked? | Why |
|---|---|---|
| Copy/selection out of a pane | **No** — low risk, ~bounded | `vt100::Cell::contents()` already gives plain text per cell (proven by our own test helper); mouse drag events are standard crossterm (`EnableMouseCapture`), just not turned on yet; clipboard write is solved either via `arboard` (direct OS clipboard access — sufficient since the multiplexer runs locally on the user's own Mac) or OSC 52 (the tmux/neovim-style escape-sequence fallback for remote scenarios) |
| Multi-pane simultaneous display | **No longer blocked — built, tested, in `mux-prototype`** | `Ctrl+b m` toggles single-pane / an auto-computed grid showing every pane at once, each resized to its own cell (not a shared size). Confirms the rendering side was never the risk — the same `PseudoTerminal`-into-a-sub-`Rect` call from the single-pane path, just N times. A fixed grid, not tmux-style recursive resizable splits: no split-tree data structure, no draggable dividers (still no free ecosystem widget for that, confirmed via the widget survey) — see "Multi-pane grid" below |
| Session persistence / detach-reattach | **No longer blocked — prototyped and tested** | Was flagged as the real structural gap (panes are child processes of the multiplexer, so closing the window killed them). `persist-prototype/` proves the cheap fix: our own binary self-daemonizes (fork + `setsid` + ignore `SIGHUP`) instead of adopting rmux's heavyweight architecture — see "Persistence: proven" below |
| Image paste (clipboard → sandbox) | Not started | Needs: browser/OS clipboard paste event → write bytes somewhere the target process can read → hand the agent a path or inline it. See "Image paste flow" below for the shape |
| Real cspace integration | Not started | Both prototypes spawn a local `$SHELL`/session as a stand-in for `container exec -it <sandbox> claude`; neither talks to the registry, DNS, or `events.ndjson` yet |
| TERM/COLORTERM forwarding for real sandbox execs | Not started, but solved elsewhere to copy from | `portable-pty`'s `CommandBuilder` just inherits the multiplexer's own env verbatim — fine today since we spawn `$SHELL` directly. Breaks the moment the child becomes `container exec -it <sandbox> claude`, because Apple Container's TTY defaults to a bare `TERM=xterm` with no `COLORTERM` (documented in the main repo's CLAUDE.md). `cmd_attach.go`'s `terminalEnvArgs` already solves this for `cspace attach`; the multiplexer would need to replicate the same explicit `-e TERM=... -e COLORTERM=...` flags on its own `container exec` invocations |
| Mouse support generally (click-to-focus sidebar, draggable splits) | Not started | Standard crossterm capability, not enabled; draggable splits specifically have no ecosystem widget (hand-roll) |

## Status line hyperlinks: move off Claude Code's statusline, into the TUI

Separate finding, worth recording since it changes the layout sketch below.

**The problem**: cspace's statusline (`lib/runtime/scripts/statusline.sh`)
emits port URLs as OSC 8 terminal hyperlinks via a `link()` helper
(`statusline.sh:133`, a correctly-formed `\033]8;;URL\033\\TEXT\033]8;;\033\\`
sequence) — these haven't been working in Ghostty. The sequence itself
isn't the issue; the rendering path is three layers deep and cspace only
controls the first one: Ghostty (outer terminal) ← `container exec -it`'s
PTY ← Claude Code's own interactive TUI ← Claude Code's `statusLine` hook
feature, which takes the script's stdout and renders it inside its own
status-bar region (wired in `cspace-entrypoint.sh:103-106` — a native
Claude Code CLI feature, not cspace's own rendering). The script's own
comments (`statusline.sh:288-291`) already record that **two earlier
iterations of this were reverted** because a row of full URLs crowds out
the line, and OSC 8 needs its open/close sequence to stay byte-adjacent to
the exact text it wraps — any truncation or reflow Claude Code's renderer
does to fit the single line can clip mid-escape-sequence and break it.
That's a structural fragility of "single-line, width-constrained, rendered
by someone else's closed-source pipeline," not a one-off bug, and not
worth debugging further from cspace's side — Claude Code's statusline
renderer isn't our code to fix.

**The fix: don't fight that renderer, own the render path instead.**
Checked ratatui-core directly rather than assume this was possible: it has
a real, tested mechanism for embedding OSC 8 (and even inline image
escapes) into a cell —
[`Cell::set_symbol`](https://docs.rs/ratatui-core) paired with
`CellDiffOption::ForcedWidth(n)`, which tells the diff/repaint engine the
correct *visual* width to reserve since the escape bytes themselves are
zero-width. Both are public API (`ratatui-core-0.1.2/src/buffer/cell.rs`),
and ratatui's own test suite covers exactly this case — an OSC 8 link
split across multiple cells, confirmed to diff/repaint correctly
(`buffer.rs:1217-1251`). This isn't a hack riding on undocumented
internals; it's the mechanism the crate was built to support, just not
exposed as a one-line `Span::hyperlink(url)` convenience.

**Plan**: drop the OSC 8 port-link trick from the single-line statusline
(simplify it back to plain text there) and render port/URL links in the
TUI's sidebar or footer instead, where there's room and cspace owns every
byte from its own buffer straight to the terminal — no intermediate
renderer to reflow or truncate them. Still unverified: actual Ghostty
rendering of an OSC 8 sequence emitted this way, since this exploration
has no macOS/Ghostty access — worth an empirical check once this lands in
`mux-prototype`.

## Persistence: proven, via `persist-prototype/`

The one item repeatedly flagged as "the real structural gap" — closing
the multiplexer's window killed every pane's process, since panes were
direct children of that process. This was thought to require adopting a
heavyweight architecture like rmux's mandatory daemon split. It doesn't:
`persist-prototype/` proves persistence with the same self-daemonizing
pattern cspace's own `cspace daemon serve` already uses (per CLAUDE.md:
auto-spawned by `cspace up`, "detached with `Setsid`"), just applied to
our own hand-rolled multiplexer binary instead of a new dependency.

**Architecture**: the same binary runs in two modes.

- `persist-prototype daemon` — detaches from the invoking terminal
  (`src/daemonize.rs`: fork, then the child calls `setsid()` to leave the
  session entirely — the actual mechanism that makes this work, since a
  closed terminal delivers `SIGHUP` to its session's foreground process
  group, and a process that has left that session can't receive it —
  ignores `SIGHUP` as defense in depth, redirects stdio to `/dev/null`),
  then owns a registry of named panes (PTY + child process each, same
  `portable-pty` primitives as `mux-prototype`) behind a Unix domain
  socket.
- `persist-prototype attach <name>` — a thin client: connects to the
  socket (spawning the daemon first if none is running), sends input,
  and renders output through the *exact same* `vt100`+`tui-term`
  rendering path as `mux-prototype`. All terminal emulation stays
  client-side; the daemon only relays raw bytes (`src/protocol.rs`, a
  deliberately dumb length-prefixed frame, not a structured state-sync
  protocol) and never touches vt100 at all.

**What's proven, not just argued** — two automated tests, both driving
the real compiled binary as a genuine separate OS process (not an
in-test `fork()`, which is unsafe inside Rust's multithreaded test
harness — this is also a more honest proof, since it's exactly how a
user would exercise it):

- `daemon_survives_sighup` — sends `SIGHUP` directly to the daemon's PID
  and confirms it's still accepting connections afterward.
- `pane_survives_client_disconnect_and_reattach` — the core claim, proven
  by asking the shell itself: client #1 attaches, asks the shell to
  print its own PID (`printf 'FIRST_PID=%s\n' $$`), then disconnects
  (simulating the terminal window closing — there's no explicit detach
  message, EOF on the socket is the only signal, same as reality). After
  a pause, a *second*, independent client connection attaches to the
  same pane name, asks for the PID again, and the test asserts the two
  PIDs match — proof it reconnected to the surviving shell rather than
  getting a fresh one.

One real bug worth recording since it'll bite again elsewhere: the first
version of this test matched the shell's *echoed input line* (a PTY
echoes back what you type before executing it) instead of its actual
output, because both happen to contain the literal marker text. Fixed by
scanning forward past any marker occurrence not immediately followed by
digits — any test that greps raw PTY output for text that also appears
in the command it sent needs this.

**Deliberately left cheap** — matching the "cheap prototype" scope:

- **No snapshot-on-reattach.** A newly-attached client only sees output
  from that point forward, not a repaint of what was on screen before —
  tmux/screen solve this by keeping a server-side virtual terminal to
  redraw from on attach; this prototype proves the process survives, not
  the full reattach UX. A real integration would give the daemon its own
  `vt100::Parser` per pane purely to answer "what does the screen look
  like right now."
- **Single attached client at a time**, tmux-without-`-d`-protection
  style — a new attach silently takes over output delivery from whoever
  had it (guarded against a narrow generation-counter race, not against
  this by design).
- **No multi-pane in the client** — one pane per `attach` invocation;
  proving the daemon/client split works came first, re-adding
  `mux-prototype`'s sidebar/tabs/multi-pane UX on top of a socket instead
  of local PTYs is straightforward once this is the accepted direction.

## Multi-pane grid: proven, in `mux-prototype`

The other item flagged as "not actually blocked, just undecided" — now
built. `Ctrl+b m` toggles between the original single-focused-pane view
and a grid showing every pane simultaneously.

**Confirms what was already argued**: the rendering call was never the
risk. `draw_grid_panes` (`src/main.rs`) is the same `PseudoTerminal`
widget rendered into an arbitrary sub-`Rect` that the single-pane path
already proved out — just called once per pane instead of once total,
into N smaller rects instead of one full-area rect. No new rendering
capability was needed.

**What's actually new**: two pure functions, `grid_dims(n)` (picks a
balanced `rows x cols` — `cols` is the smallest value with
`cols*cols >= n`) and `grid_rects(area, n)` (splits an area into exactly
`n` rects in row-major order, stretching a partially-filled last row
across the full width instead of leaving empty cells). Both are
deterministic layout math with no PTY/terminal dependency, so they're
covered by real unit tests — `grid_rects_covers_every_pane_without_overlap_or_overflow`
checks exact rect count, in-bounds, and no pairwise overlap across
awkward pane counts (1, primes, a perfect square); `grid_dims_is_at_least_as_big_as_pane_count`
checks the capacity invariant holds for 1 through 20 panes.

**The bookkeeping this exposed, as expected**: each visible pane now
resizes to its own grid cell (`draw_grid_panes` calls `pane.resize(...)`
per cell) instead of the shared `content_size` the single-pane path uses.
No extra machinery was needed to keep these consistent when toggling
modes or resizing the terminal — `Pane::resize` already no-ops when the
size is unchanged, so whichever mode's draw function runs next simply
corrects any pane that's now the wrong size, including the one harmless
redundant resize call when a terminal-resize event fires while in grid
mode (the event handler still calls the single-pane `resize_all_panes`
first; the very next `draw_grid_panes` call immediately re-corrects each
pane to its real cell size).

**Deliberately still a fixed grid, not tmux-style splits** — matching
what was scoped from the start: no split tree, no resizable dividers, no
mouse. A grid cell that shrinks below ~3 rows/cols just stops resizing
(the existing `rows == 0 || cols == 0` guard in `Pane::resize`) rather
than rendering garbage, an acceptable rough edge for a lot of panes in a
small terminal.

## Remote access: SSH passthrough proven, browser rendering is a different path

Prompted by "how hard would it be to target a remote instance instead of
the container running on the local computer" — split into two genuinely
different questions with very different costs.

**(A) Same sandbox, viewed/typed from a different machine — cheap,
proven.** `persist-prototype`'s client is just a normal program reading
and writing a real TTY; nothing about it assumes the terminal is local.
Proved this concretely rather than just asserting it: stood up a real
`sshd` + keypair in this exploration's environment, started the daemon,
then drove the *actual* interactive `attach` client through
`ssh -tt localhost` by piping keystrokes into SSH's own stdin (which SSH
forwards to the remote PTY exactly as if typed) and capturing the
rendered output SSH streamed back. The round trip held end to end: local
keystrokes → SSH → remote PTY → the real client → the Unix socket → the
daemon → the shell → vt100/ratatui rendering → back out through SSH →
captured locally, landing on a real shell PID (`SSH_PROOF=1174`) followed
by a live cursor — not a mock.

Two things surfaced along the way, both confirming existing findings
rather than new problems:
- **The remote PTY's window size is whatever the local side's real
  terminal propagates** — this exploration's own sandboxed environment
  has no controlling terminal, so the first attempt negotiated a `0x0`
  pty (confirmed directly via `stty size` over the same SSH hop) and
  nothing ever rendered. Forcing an explicit size (`stty rows 30 cols
  100`) before launching the client — what a real terminal app does
  automatically — fixed it immediately. Not a flaw in the SSH path or
  the client; purely an artifact of testing from an environment with no
  real terminal of its own.
- **`TERM` forwarding over SSH degraded exactly the way the main repo's
  own `cmd_attach.go` fix and the Ghostty-statusline finding both
  describe** — the remote session saw a plain `TERM=linux`, not
  whatever richer value a real terminal app would carry. Any real
  "attach to a remote sandbox over SSH" feature would need the same
  explicit `TERM`/`COLORTERM` forcing cspace already applies for local
  `container exec`, just applied to the SSH invocation instead.

A native TCP transport (instead of piggybacking on SSH) is a small
protocol change — `protocol.rs` already works over anything implementing
`Read`+`Write` — but reopens the `QUALITY_FINDINGS.md` §7 security gaps
far more seriously: "no auth, reachable from other local users" becomes
"no auth, reachable from the network" unless real authentication and
network confinement are added.

**(B) The sandbox itself hosted on a different machine** — a much bigger,
separate question, and blocked by something the Apple-Container-coupling
research already found: Apple Container has no remote mode at all — it
drives Apple's own local Virtualization.framework hypervisor directly,
with nothing like Docker's `DOCKER_HOST`/remote-context support. This
isn't a gap in cspace to fix; it falls entirely out of the Docker
substrate work already scoped separately, since Docker's remote-context
support (`ssh://`/`tcp://` daemons) already exists and would come along
with adopting it, not as new work of its own.

**Browser-rendered terminals are not the same path as either of the
above**, and not something SSH passthrough gets you toward. A browser
can't open a raw Unix/TCP socket or an SSH connection — only
HTTP/WebSocket — so a browser terminal always needs a server-side bridge
translating PTY bytes into WebSocket messages, and something has to
actually draw characters in the DOM/canvas, since there's no real
terminal app in the loop the way there is for SSH. That drawing is either:
- **A JS terminal emulator** (almost always `xterm.js`) fed over
  WebSocket — what most existing "web terminal" tools actually do. The
  backend can be Rust; the rendering itself is JavaScript, not Rust.
  (Correcting an earlier framing in this doc: t3code's browser terminal,
  `native/libghostty-vt`, is **Zig**, not Rust — Ghostty itself is
  written in Zig. Not an example of a Rust-rendered browser terminal
  either, despite looking similar.)
- **An actual Rust-compiled-to-WASM terminal core running client-side**
  — the genuine case. The concrete real example is
  [ratzilla](https://github.com/orhun/ratzilla), which compiles a
  `ratatui` app itself to run in a browser canvas via WASM — directly
  relevant here since that's the exact framework `mux-prototype` is
  built on.

`persist-prototype/src/protocol.rs`'s transport-agnostic design (already
proven above, over SSH rather than a direct socket) means a WebSocket
bridge would be additive, not a redesign. `vt100` itself is a
pure, dependency-light Rust crate with no OS calls, and should compile to
WASM without much friction — making a `ratzilla`-style client-side path
using our *own* existing vt100 parsing code a plausible, architecturally
consistent future direction, distinct from and complementary to (A)
above. Not attempted in either prototype.

## Rough feature/layout outline (draft — not a spec)

Sketch of where this is headed, roughly matching what's already built plus
what's discussed above:

```
┌─────────────────┬──────────────────────────────────────────────────┐
│  sandboxes       │  sandbox-1   sandbox-2   sandbox-3      (tabs)   │
│                  ├──────────────────────────────────────────────────┤
│ ● sandbox-1      │                                                  │
│ ○ sandbox-2      │                                                  │
│ ▲ sandbox-3      │           focused pane (or N visible panes       │
│ ✓ sandbox-4      │           in a grid, once multi-pane lands)      │
│ ✕ sandbox-5      │                                                  │
│                  │                                                  │
│ [attach status,  │                                                  │
│  ports, etc. —   │                                                  │
│  registry data   │                                                  │
│  not yet wired]  │                                                  │
├──────────────────┴──────────────────────────────────────────────────┤
│  footer: errors, scrollback indicator, paste/upload progress         │
└───────────────────────────────────────────────────────────────────────┘
```

- **Sidebar** (`List`, already built) — one row per sandbox, status glyph
  (●working / ○idle / ▲blocked / ✓done / ✕exited) + name. Future: pull
  from the real registry instead of an in-process `Vec<Pane>`, add ports /
  credential-staleness indicators (cspace already tracks this data,
  per CLAUDE.md's credential-baking section), and render each port as a
  clickable OSC 8 hyperlink (see "Status line hyperlinks" above) — this is
  where the links the statusline currently struggles with should live
  instead.
- **Tabs** (`Tabs`, already built) — mirrors sidebar selection, one label
  per open pane.
- **Main pane area** (`tui-term`'s `PseudoTerminal`, already built) — the
  live terminal. Today: one focused pane. Planned: N-up grid once the
  layout model above is decided.
- **Footer** (`Paragraph`, already built for errors only) — natural place
  to also surface scrollback depth, an in-progress image upload, or a
  credential-expiry warning, mirroring `cspace up`'s existing boot-time
  credential summary.
- **Terminal access** — today, "everything not caught by the leader key"
  already forwards straight to the focused pane's shell, so once the
  spawned command is `container exec -it <sandbox> claude` instead of a
  bare shell, running `git push` yourself is just typing it — no new
  mechanism needed beyond the exec-target swap and the TERM fix above.
- **Image paste flow** (not built) — sketch, following the t3code pattern
  adapted to cspace's simpler single-process-per-sandbox shape: browser or
  OS paste/drop → write bytes to a location the target process can read
  (for a headless `cspace send`-style session, that's a new
  `POST /attachment` route on the Bun supervisor writing into the
  sandbox's own filesystem; for an *interactive* attach/pane, there's no
  HTTP control surface today, so it'd mean the multiplexer itself writing
  the file into the sandbox's already-bind-mounted session directory and
  then typing/pasting the resulting path into the pane's PTY input).
- **Copy/selection** (not built) — click-drag over a pane using
  `vt100::Cell` contents per the table above, OS clipboard via `arboard`,
  OSC 52 as a documented-but-optional fallback.

## Open design questions

Not yet decided, worth resolving before investing further:

1. ~~**Persistence**~~ — resolved: `persist-prototype/` proves the cheap
   self-daemonizing path works (see "Persistence: proven" above). Open
   sub-question: fold this into `mux-prototype` directly, or keep them
   separate until snapshot-on-reattach and multi-pane are also ported?
2. ~~**Multi-pane arrangement model**~~ — resolved as a fixed grid, not
   tmux-style recursive splits (see "Multi-pane grid" above). Open
   sub-question: is a fixed grid good enough long-term, or does resizable
   splits become worth its cost (no free ecosystem widget, hand-rolled
   split-tree + divider dragging) once this is used daily?
3. **Status source**: for headless (`cspace send`-style) sessions, tail
   `events.ndjson`/poll `/status` for a real signal, same shape as the
   activity-derived working/idle already built. For interactive attach
   panes there's no structured signal at all today — same
   screen-manifest-heuristic tradeoff herdr has, unless Claude Code's own
   hook system is wired to report into a local socket.
4. **Where image uploads land for an interactive pane** specifically,
   since there's no HTTP control surface for those today (see "Image
   paste flow" above).
5. **Does Ghostty actually render an OSC 8 hyperlink emitted directly by
   our own TUI?** The ratatui mechanism is confirmed real and tested (see
   "Status line hyperlinks" above); what's unverified is the terminal end,
   since this exploration has no macOS/Ghostty access to check.
6. **Snapshot-on-reattach**: give the daemon its own per-pane
   `vt100::Parser` so a newly-attached client can be repainted with
   current screen state instead of only seeing output from the moment it
   attached (see "Persistence: proven" above).
7. ~~**Remote UI to a local sandbox**~~ — resolved as cheap/already
   possible via SSH passthrough (see "Remote access" above). Open
   sub-question: worth a native TCP transport for this, given the
   security hardening it would require, or is SSH passthrough sufficient
   indefinitely? **Remote sandbox hosting** (a different, larger
   question) stays gated on the Docker substrate work, unresolved here.
8. **Browser-rendered terminal**: a `ratzilla`-style WASM build of our
   own `vt100` parsing code is plausible (see "Remote access" above) but
   entirely unattempted — not prototyped in either direction (JS/xterm.js
   bridge vs. Rust/WASM client-side rendering).

## Running the prototypes

```sh
cd experiments/mux-prototype && cargo run       # hand-rolled, current direction
cd experiments/rmux-prototype && cargo run      # needs an `rmux`/`rmux-daemon` binary
                                                 # on PATH or $RMUX_SDK_DAEMON_BINARY;
                                                 # not published as a ready-to-run binary,
                                                 # built from github.com/Helvesec/rmux source
cd experiments/persist-prototype
cargo run -- attach sandbox-1                   # spawns the daemon automatically if
                                                 # none is running yet, then attaches
# close the window / Ctrl+C the client, then run the same command again —
# it reattaches to the same still-running shell, not a fresh one
```

All three have `cargo test` coverage for their core claims:
`mux-prototype` covers spawn/write/parse, the activity-derived status and
exited-pane-visibility behavior, and the grid layout math (exact rect
count, in-bounds, no overlap, capacity, across awkward pane counts);
`rmux-prototype` covers spawn/write/snapshot against a real daemon;
`persist-prototype` covers daemon survival across `SIGHUP` and — the
actual persistence claim — a pane surviving a client disconnect and a
different client reattaching to the same still-running shell.
