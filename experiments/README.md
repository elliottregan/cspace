# cspace multiplexer/UI exploration (draft)

Status: exploratory, not wired into the Go CLI or `make check`. This
document is a snapshot of an in-progress design conversation, not a spec —
expect it to be wrong in places and to change as the prototypes evolve.

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
| Multi-pane simultaneous display | **No** — rendering is already proven, the size is in the layout model | `PseudoTerminal` already renders into an arbitrary sub-`Rect` (we already do this for the single focused pane); showing N panes is mechanically the same call N times into N smaller rects. What's actually undecided is the **arrangement model**: a fixed 2-up/4-up grid is cheap; tmux-style arbitrary recursive splits with resizable dividers is a real data structure + interaction model, and nothing in the ratatui ecosystem gives you resizable splits for free (confirmed via the widget survey) |
| Session persistence / detach-reattach | **This is the real structural one** | Panes are child processes of the multiplexer itself; closing the window kills every pane's process (including a future `container exec -it <sandbox> claude`). This is the one thing herdr/tmux/rmux all solve via a daemon/client split, which is exactly the architecture we rejected for rmux above. Needs a deliberate decision, not a bolt-on: either accept the limitation (same as `cspace attach` has today), or build cspace's *own* lightweight daemon split (reusing patterns from `cspace daemon serve` rather than adopting rmux's) |
| Image paste (clipboard → sandbox) | Not started | Needs: browser/OS clipboard paste event → write bytes somewhere the target process can read → hand the agent a path or inline it. See "Image paste flow" below for the shape |
| Real cspace integration | Not started | Both prototypes spawn a local `$SHELL`/session as a stand-in for `container exec -it <sandbox> claude`; neither talks to the registry, DNS, or `events.ndjson` yet |
| TERM/COLORTERM forwarding for real sandbox execs | Not started, but solved elsewhere to copy from | `portable-pty`'s `CommandBuilder` just inherits the multiplexer's own env verbatim — fine today since we spawn `$SHELL` directly. Breaks the moment the child becomes `container exec -it <sandbox> claude`, because Apple Container's TTY defaults to a bare `TERM=xterm` with no `COLORTERM` (documented in the main repo's CLAUDE.md). `cmd_attach.go`'s `terminalEnvArgs` already solves this for `cspace attach`; the multiplexer would need to replicate the same explicit `-e TERM=... -e COLORTERM=...` flags on its own `container exec` invocations |
| Mouse support generally (click-to-focus sidebar, draggable splits) | Not started | Standard crossterm capability, not enabled; draggable splits specifically have no ecosystem widget (hand-roll) |

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
  per CLAUDE.md's credential-baking section).
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

1. **Persistence**: accept the "closing the window kills your panes"
   limitation (matches `cspace attach` today), or build a minimal
   cspace-owned daemon split for this specifically? This is the biggest
   lever on scope of everything else.
2. **Multi-pane arrangement model**: fixed grid (cheap) vs. tmux-style
   recursive resizable splits (real feature, no free ecosystem widget)?
3. **Status source**: for headless (`cspace send`-style) sessions, tail
   `events.ndjson`/poll `/status` for a real signal, same shape as the
   activity-derived working/idle already built. For interactive attach
   panes there's no structured signal at all today — same
   screen-manifest-heuristic tradeoff herdr has, unless Claude Code's own
   hook system is wired to report into a local socket.
4. **Where image uploads land for an interactive pane** specifically,
   since there's no HTTP control surface for those today (see "Image
   paste flow" above).

## Running the prototypes

```sh
cd experiments/mux-prototype && cargo run     # hand-rolled, current direction
cd experiments/rmux-prototype && cargo run    # needs an `rmux`/`rmux-daemon` binary
                                               # on PATH or $RMUX_SDK_DAEMON_BINARY;
                                               # not published as a ready-to-run binary,
                                               # built from github.com/Helvesec/rmux source
```

Both have `cargo test` coverage for their core spawn/write/parse (or
spawn/write/snapshot) round-trip; `mux-prototype` additionally covers the
activity-derived status and exited-pane-visibility behavior.
