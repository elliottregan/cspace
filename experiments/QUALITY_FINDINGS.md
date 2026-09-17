# Productionization audit: prototype → real product (draft)

Status: identification only, not a design or fix list. Requested as prep
for turning the `experiments/` prototypes into cspace's real multiplexer
layer — a subagent audited `mux-prototype/`, `persist-prototype/`, and
`rmux-prototype/` (source only, not the rendered UI) purely to flag
maintenance and performance risks. Deliberately **no proposed fixes, no
module/crate boundaries, no code changes** — that's a separate pass once
these are triaged. Anything already documented in `README.md` as a known,
accepted simplification (single attached client, no snapshot-on-reattach,
fixed grid, thread-per-pane) is not re-reported here unless a genuinely
new angle on it turned up.

All citations below were verified by reading the actual source: `mux-prototype/src/main.rs`,
`persist-prototype/src/{main,daemon,daemonize,client,protocol}.rs` and
`tests/persistence.rs`, `rmux-prototype/src/main.rs`, plus each project's
`Cargo.toml`/`Cargo.lock`/`.gitignore`.

## 1. File/module organization (single-responsibility)

`mux-prototype/src/main.rs` is 777 lines with zero internal module split
— PTY spawning, vt100 wiring, status logic, ratatui rendering, input
translation, event-loop dispatch, and `main()` all live in one file.

- `struct Pane` (`main.rs:117-132`) bundles PTY `master`/`writer`
  ownership, the `vt100::Parser`, activity-timestamp tracking, exit
  detection, and manual status-override state in one type.
  `effective_status()` (`main.rs:241-257`) then computes UI-facing
  presentation logic directly as a method on that same PTY-owning struct
  — resource ownership and derived display logic are the same object,
  not separable concerns.
- `struct App` (`main.rs:270-281`) and its impl (`main.rs:283-455`) mix
  pane lifecycle (`new_pane`, `kill_focused`, `next_pane`, `prev_pane`),
  resize orchestration, layout-mode state, error state, AND all
  rendering (`draw`, `draw_sidebar`, `draw_tabs`, `draw_focused_pane`,
  `draw_grid_panes`, `draw_footer`) in one impl block.
- Rendering has side effects on model state: `draw_focused_pane`
  (`main.rs:399-418`) and `draw_grid_panes` (`main.rs:424-444`) each call
  `pane.resize(...)` and lock the pane's vt100-parser mutex as part of
  *drawing* — PTY/parser mutation and frame rendering are interleaved
  rather than separated, and the resize-during-draw pattern is duplicated
  identically in both functions instead of shared.
- Input handling is a single large inline `match` in `run()`
  (`main.rs:561-623`) mixing terminal-resize handling, 8-way leader-key
  dispatch, raw key forwarding, and paste-event handling — no separate
  "input router" from "event loop."
- `pane_content_rect()` (`main.rs:487-496`) duplicates the exact
  layout-split constraints used inside `App::draw` (`main.rs:348-358`) —
  the same sidebar width and vertical row split written out twice with no
  shared source of truth. A change to one and not the other would
  silently desync "what size panes are resized to" from "what size
  they're actually rendered into."

`persist-prototype/src/daemon.rs` (155 lines) interleaves wire-protocol
dispatch with direct resource control: `handle_connection`
(`daemon.rs:87-125`)'s `match frame.tag` arms call
`pane.writer.lock().unwrap().write_all(...)` and
`pane.master.lock().unwrap().resize(...)` directly inline — no separation
between "decode a frame" and "apply it to a PTY." `get_or_create_pane`
(`daemon.rs:36-85`) mixes PTY spawning, reader-thread spawning, and
registry bookkeeping in one function.

`persist-prototype/src/client.rs`: `run()` (`client.rs:23-99`) mixes raw-mode
terminal setup/teardown, socket I/O, vt100 parsing, key translation, and
an inline `terminal.draw()` closure (`client.rs:56-75`) that itself
computes a resize decision, sends a protocol frame, mutates the parser's
size, AND renders — four concerns in one closure.

## 2. Duplication across the three prototypes

- `key_to_bytes` (`mux-prototype/src/main.rs:459-482`) and
  `persist-prototype/src/client.rs:101-122` are near-verbatim duplicates
  — identical Ctrl-key and Enter/Backspace/Tab/Esc/arrow logic.
  `rmux-prototype/src/main.rs:216-234` (`key_to_token`) duplicates the
  same responsibility a third time, with a different output shape.
- PTY-spawn setup is duplicated near-identically between `spawn_pane`
  (`mux-prototype/src/main.rs:134-192`) and `get_or_create_pane`
  (`persist-prototype/src/daemon.rs:36-85`): same
  `native_pty_system()` → `openpty` → shell-env fallback →
  `CommandBuilder` → `spawn_command` → `drop(pair.slave)` →
  `try_clone_reader()`/`take_writer()` → an 8192-byte reader loop.
- `SCROLLBACK_LINES: usize = 10_000` is independently redefined in
  `mux-prototype/src/main.rs:45` and `persist-prototype/src/client.rs:21`.
- `vt100::Parser::new(rows, cols, SCROLLBACK_LINES)` construction and the
  `screen_mut().set_size(...)` resize call are duplicated between
  `mux-prototype/src/main.rs:158`/`:205` and
  `persist-prototype/src/client.rs:31`/`:67`.
- The `event::poll(Duration::from_millis(33))` main-loop tick is
  duplicated in `mux-prototype/src/main.rs:557` and
  `persist-prototype/src/client.rs:77`, and an equivalent appears a third
  time in `rmux-prototype/src/main.rs:250-253`.
- Per-pane state shape (title + status-ish state + PTY/driver handle) is
  duplicated three different ways: `Pane` (mux), `DaemonPane` (persist
  daemon) plus the client-side bare `parser`/`writer` locals with no
  named struct at all (persist client), and `PaneEntry` (rmux). Each
  carries a different subset of the same concerns with no shared shape.

## 3. Hardcoded constants that would need to be configurable

- `mux-prototype/src/main.rs:45` `SCROLLBACK_LINES = 10_000`
- `mux-prototype/src/main.rs:47` `SCROLL_STEP = 10`
- `mux-prototype/src/main.rs:49` `IDLE_THRESHOLD = Duration::from_secs(2)`
- `mux-prototype/src/main.rs:51` `DEFAULT_PANE_SIZE = (24, 80)`
- `mux-prototype/src/main.rs:167` reader buffer `[0u8; 8192]` (duplicated
  at `persist-prototype/src/daemon.rs:68`)
- `mux-prototype/src/main.rs:145` shell fallback `"/bin/bash"`
  (duplicated at `persist-prototype/src/daemon.rs:46`)
- `mux-prototype/src/main.rs:350` and `:490` sidebar width
  `Constraint::Length(24)` (duplicated in two places, see §1)
- `mux-prototype/src/main.rs:357`/`:494` tabs height `Length(3)`, footer
  height `Length(1)`
- `mux-prototype/src/main.rs:557` event-poll interval `33ms` (duplicated
  at `persist-prototype/src/client.rs:77`, and effectively again at
  `rmux-prototype/src/main.rs:251`)
- `persist-prototype/src/client.rs:21` `SCROLLBACK_LINES = 10_000`
  (separate copy of the mux constant)
- `persist-prototype/src/daemon.rs:44` new-pane PTY size hardcoded to
  `rows: 24, cols: 80` regardless of the attaching client's actual
  terminal size (unlike mux-prototype, which sizes new panes to the live
  `content_size`)
- `persist-prototype/src/main.rs:28` socket path hardcoded to
  `std::env::temp_dir().join("cspace-persist-proto.sock")` — one fixed,
  predictable, global path shared by every invocation on the host
  (overridable only via `PERSIST_PROTO_SOCKET`)
- `persist-prototype/src/main.rs:41-47` daemon-start wait: hardcoded 50
  retries × 50ms = 2.5s timeout
- `rmux-prototype/src/main.rs:304` / test at `:333` — `default_timeout`
  hardcoded to 5s (main) / 10s (test), inconsistent between the two
- `rmux-prototype/src/main.rs:91` sessions created at fixed `24x80` and,
  per the file's own header comment (`main.rs:14-17`), never resized
  afterward

## 4. Error handling — silent discards that would hide real failures

- `mux-prototype/src/main.rs:199` `let _ = self.master.resize(...)` — a
  failed PTY resize is invisible; the pane silently keeps stale
  dimensions.
- `mux-prototype/src/main.rs:213` `let _ = self.writer.write_all(bytes)`
  — a failed write of user keystrokes to the PTY is silently dropped;
  input simply vanishes with no footer/error surfaced, unlike the
  "spawn failed" path which does set `last_error`.
- `mux-prototype/src/main.rs:320` `let _ = pane.child.kill()` — a failed
  kill is unreported; combined with no explicit `.wait()`/reap call
  anywhere on a killed or exited child, a kill that doesn't actually
  terminate the process has no operator-visible signal.
- `persist-prototype/src/daemon.rs:104` and `:108-113` — same pattern:
  silent write and resize failures.
- `persist-prototype/src/daemon.rs:150`
  `let _ = handle_connection(stream, registry)` — every per-connection
  error (protocol violation, I/O error, malformed frame) inside
  `serve()`'s accept loop is fully discarded with no log line at all.
- **There is no logging framework and no `eprintln!`/`log`/`tracing`
  call anywhere in the error paths of any of the three prototypes** — a
  production daemon with zero operator-visible diagnostics on connection
  failure is a gap distinct from any individual `let _ =`.
- `persist-prototype/src/protocol.rs:54-61` `decode_resize` returns
  `None` on a too-short payload, and the caller (`daemon.rs:107`)
  silently no-ops on `None` — a malformed resize frame is
  indistinguishable from a benign one, with no error path at all.

## 5. Concurrency / locking patterns

- **Mutex-per-pane, but a full-frame lock sweep in grid mode**:
  `draw_grid_panes` (`mux-prototype/src/main.rs:424-444`) acquires every
  visible pane's `parser` mutex, once each, serially, inside a single
  draw call, and that draw call runs on essentially every loop iteration
  (`main.rs:555`, gated only by the 33ms poll). As pane count grows, one
  frame's total lock-wait time is the *sum* of however long each reader
  thread happens to be holding its own pane's lock — a single busy pane
  can stall the whole frame's redraw for every other pane, since locks
  are acquired one after another rather than concurrently.
- Every pane's parser/activity/exited state is guarded by
  `.lock().unwrap()` throughout both prototypes — a panic anywhere while
  holding one of these locks (e.g. inside `vt100::Parser::process` on
  malformed input) poisons that `Mutex`, and every subsequent
  `.lock().unwrap()` on it (from the UI thread, the reader thread, or a
  future draw) then panics too. One bad byte stream can escalate from
  "one pane misbehaves" to "the whole process crashes."
- `persist-prototype`'s generation-counter guard against the
  single-attached-client race is **narrowed, not closed** (see §7 for
  the concrete interleaving).

## 6. Thread-per-pane model — ceiling and cleanup

- Both `mux-prototype::spawn_pane` (`main.rs:165-179`) and
  `persist-prototype::get_or_create_pane`'s reader thread
  (`daemon.rs:66-81`) spawn one OS reader thread per pane that loops
  forever until `read()` returns `0`/`Err`. Neither result of
  `std::thread::spawn` is retained anywhere — no `JoinHandle` is stored,
  so there is no `.join()` call and no way for the rest of the program to
  observe or wait on reader-thread termination; termination is entirely
  implicit.
- In `mux-prototype`, a killed or exited pane's reader thread does
  self-terminate (EOF on the dropped master), so threads don't accumulate
  for panes that are actually gone — but each new pane created over a
  long session spawns a brand-new thread with no reuse/pool, so thread
  creation scales linearly with total panes ever created in the
  session's lifetime, not with panes currently open.
- **`persist-prototype/src/daemon.rs`'s pane registry never removes
  entries.** `get_or_create_pane` (`daemon.rs:36-85`) inserts into `map`
  on first creation (`daemon.rs:83`) but nothing anywhere in the file
  ever calls `map.remove(...)`. Once a pane's shell process exits, its
  `DaemonPane` (dead handles included) stays in the `HashMap` forever,
  and a subsequent `attach` to that same name reconnects to the stale
  dead entry rather than spawning a fresh shell — a genuine leak/staleness
  gap with no cleanup path at all, distinct from the reader-thread
  self-termination above.

## 7. `persist-prototype` security-relevant gaps

- **No socket permission restriction at all.** `daemon::serve`
  (`daemon.rs:138-142`) calls `UnixListener::bind(socket_path)` with no
  follow-up `set_permissions`/`chmod` call anywhere (confirmed via
  search — zero hits for `set_permissions`/`chmod`/`umask` in any `.rs`
  file). The resulting socket's access mode is whatever the process
  umask leaves, which on many systems is group/world-connectable — any
  other local user able to reach the socket path can attach, inject
  arbitrary keystrokes into the shell (arbitrary command execution as
  the daemon's user), and read all PTY output.
- **Hardcoded, predictable socket path in a world-writable directory.**
  `main.rs:28` defaults to a single fixed name under `/tmp` shared by
  every invocation on the host. `daemon::serve` (`daemon.rs:139-141`)
  unconditionally removes and rebinds whatever file currently exists at
  that path with no ownership check, and `ensure_daemon_running`
  (`main.rs:34-36`) treats "a connect to that path succeeds" as proof the
  real daemon is up. On a shared multi-user host this is a classic
  predictable-path race: another local user can pre-create a file/socket
  at that exact path, and either get it silently deleted and replaced
  (denial of service/confusion), or run their own listener there first so
  the real user's `attach` connects to the attacker's process and sends
  it every keystroke typed in that session.
- **Unbounded, unauthenticated length-prefixed allocation.**
  `protocol::read_frame` (`protocol.rs:32-45`) reads a client-supplied
  `u32` length with no upper bound and immediately allocates
  `vec![0u8; len]` (`protocol.rs:42`) before validating anything about
  the sender — a single connection can request up to ~4 GiB in one
  frame; the exposure is a memory-exhaustion DoS against the daemon
  process (which, being a persistent background daemon by design, has an
  outsized blast radius) from any peer that can reach the socket at all
  (which, per the point above, may be broader than intended).
- **Single-attached-client generation-counter race is narrowed, not
  fully closed** — see §5. The code's own comment
  (`daemon.rs:28-31`) frames this as a guard against "a just-superseded
  connection's cleanup clobbering a newer attach," and it does correctly
  prevent that specific clobber-on-disconnect case (verified by reading
  the `generation.load() == my_generation` check at `daemon.rs:121`) —
  but it does not make attach-ordering itself atomic, so which of two
  concurrently-attaching clients actually receives output is not
  deterministic, and the loser gets a false-positive `TAG_ATTACHED` ack
  with no further data.
- The pane name from an attaching client is used as a raw `HashMap` key
  with no length cap (bounded only by the frame-length issue above) and
  no character validation — not a path-traversal risk here since it's an
  in-memory key, but it means an unbounded number of distinct pane names
  could be registered by a single misbehaving client, compounding the
  "registry entries are never removed" gap in §6.

## 8. Testing gaps beyond what the README already accepts

- **`key_to_bytes`/`key_to_token`** — the function most duplicated across
  all three prototypes (§2) has zero unit tests in any of them; no test
  asserts a single key (arrow key, Ctrl+letter, Enter) maps to the
  expected byte sequence or token.
- **No resize edge-case tests anywhere.** `Pane::resize`'s
  `rows == 0 || cols == 0` guard (`mux-prototype/main.rs:196`) is
  exercised only implicitly via normal runtime sizing, never with an
  explicit zero/degenerate size; `protocol::decode_resize`'s
  short-payload `None` path (`protocol.rs:54-61`) has no test; nothing
  exercises a `TAG_RESIZE` frame with a malformed payload reaching the
  daemon.
- **No adversarial/malformed-frame tests for the wire protocol.**
  `persist-prototype`'s only tests (`tests/persistence.rs`) drive the
  protocol through the real, well-formed `RawClient` helper — there is
  no test sending an oversized length prefix, a truncated frame, an
  unknown tag byte, or an empty/huge pane name, despite `read_frame`'s
  unbounded allocation being a live concern (§7).
- **No concurrent-attach test.** The exact race the code comments in
  `daemon.rs:28-31` describe is never exercised by any test — the
  existing `pane_survives_client_disconnect_and_reattach` test is
  strictly sequential (first client fully detaches, a 300ms sleep, then
  the second attaches), so it cannot and does not catch the
  generation-ordering race in §5/§7.
- **No test for re-attaching to a pane whose shell has already exited.**
  Given the registry-never-cleans-up gap (§6), there's no test asserting
  what happens when `attach` targets a pane name whose underlying
  process died.
- `mux-prototype` has no test for `kill_focused`'s index-adjustment
  logic, and no test that drives the leader-key dispatch table through
  the actual event-loop `match` in `run()` — the existing tests call
  `App`/`Pane` methods directly, bypassing the input-routing code
  entirely.
- `rmux-prototype` has exactly one test (the spawn/write/snapshot
  roundtrip); its own file-header comment documents that resize is
  unwired, and there is no test — not even a documented gap — covering
  that resize path at all.

## 9. Directory/repo-integration friction

- All three are independent, flat `Cargo.toml` projects directly under
  `experiments/<name>/` with **no top-level `Cargo.toml` workspace**
  tying them together (confirmed: no `experiments/Cargo.toml`, and none
  of the three declare a `[workspace]`). A single `cargo build`/`cargo
  test` from the repo root builds none of them; each needs its own
  separate invocation, and `make check`/`ci.yml` currently reference
  `cargo` and `experiments` nowhere at all (confirmed by grep) — the Rust
  code has zero CI coverage today.
- **Confirmed dependency-version drift already exists** between just
  these three independently-resolved lockfiles: `mux-prototype` and
  `persist-prototype` both pin `ratatui = "0.30.2"`, but `rmux-prototype`
  pins `ratatui = "0.29.0"` — a real, already-existing version skew, not
  a hypothetical one.
- `persist-prototype/Cargo.lock` alone resolved three different versions
  of the `nix` crate transitively (`0.28.0`, `0.29.0`, `0.31.3`) even
  though its own `Cargo.toml` requests only `0.31.3` directly — version
  fragmentation exists within a single project's dependency tree before
  any cross-project merge is even considered.
- Each project resolved its own lockfile independently against the same
  crates (`portable-pty 0.9.0`, `tui-term 0.3.4`, `vt100 0.16.2` happen to
  match today between `mux-prototype` and `persist-prototype`, but
  nothing enforces that going forward since there's no shared lock).
- Each subdirectory carries its own `.gitignore` (correctly excluding
  `target/`), but this per-project scaffolding is itself three parallel,
  independently-maintained build setups inside one Go repository whose
  `Makefile`/`ci.yml` have no concept of a Rust toolchain at all.

## 10. Other observations

- `mux-prototype/src/main.rs:555-559`: the main loop calls
  `terminal.draw()` unconditionally on every pass, then polls for 33ms —
  a full redraw of every visible pane (all of them, in grid mode) happens
  at up to ~30Hz continuously, regardless of whether any pane's content
  actually changed. There is no dirty/damage tracking; render cost scales
  with pane count × ~30 times per second even when every pane is idle.
- `persist-prototype/src/daemon.rs:73-78`: when no client is currently
  attached to a pane, output read from the PTY is dropped on the floor.
  This is already an accepted simplification per the README's
  persistence section (no snapshot-on-reattach), so not re-reported as
  new, but worth noting precisely where in code it lives: this is also
  where a future per-pane `vt100::Parser` would need to hook in to buffer
  state while unattached, and today that buffering literally does not
  exist — output produced while nobody is attached is unrecoverably
  lost, not merely un-repainted.
- `persist-prototype/tests/persistence.rs:29-40`: `spawn_daemon` calls
  `.wait()` on the intermediate launcher process (correct per the
  fork/detach model), but the real daemon PID is then read from a PID
  file on disk (`daemon_pid`, `persistence.rs:52-63`) with no
  verification that the PID in that file is still the same process by
  the time the test acts on it — a PID-reuse race is theoretically
  possible on a long-idle CI box, though low-probability in practice.
