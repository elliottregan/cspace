# Panes, part A: the `internal/pane` engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/pane` — a PTY plus a terminal emulator behind a small interface, with the key overlay, the bounded writer and the teardown handshake the control-plane design calls for — plus the commands each pane kind runs, the two step-3 follow-ups that must land before a sandbox name can arrive from a pane picker, and a repo-resident pty smoke harness. Nothing in this plan changes `cspace tui`: it lands the engine that plan 4b wires into the dashboard.

**Architecture:** `internal/pane` owns one child process on a PTY and the emulator that interprets its output. Four goroutines per pane — output pump (PTY → emulator), response drain (emulator → PTY), writer (a bounded channel → PTY), and process waiter — so a child that stops reading stalls only its own queue. The emulator sits behind an `Emulator` interface with exactly one implementation, an adapter over `github.com/charmbracelet/x/vt`; the adapter carries the key overlay for the modified keys x/vt drops and tracks the kitty keyboard protocol through a CSI `u` handler. The package knows nothing about cspace: it takes a `Command` and returns rendered screens.

**Tech Stack:** Go 1.26; `github.com/charmbracelet/x/vt` (pinned pseudo-version), `github.com/creack/pty`, `github.com/charmbracelet/ultraviolet` (already in the graph, promoted to direct), `github.com/charmbracelet/x/ansi`; `internal/{control,registry,cli}` for the two follow-ups; Python 3 stdlib for the smoke harness.

**Spec:** `docs/superpowers/specs/2026-09-17-control-plane-design.md` — this plan implements the engine half of **rollout step 4, "Panes"**. The other half (tabs, focus, leader dispatch, the detach protocol in the UI, the supervisor view, live verification) is `docs/superpowers/plans/2026-09-18-control-plane-4b-panes-in-the-dashboard.md`, which consumes every interface this plan produces.

## Global Constraints

- **Scope is the engine.** `internal/controlplane` is not touched by any task in this plan. No tabs, no focus model, no leader dispatch, no supervisor view, no detach protocol in the UI — those are 4b. No mouse and no image paste at all: those are rollout step 5.
- **`cspace tui` is never broken.** Every task in this plan leaves the dashboard exactly as step 3 shipped it. The pane engine lands behind the dashboard and is wired in by 4b.
- **Dependency direction**, verified with `go list -deps` in Task 3:
  - `internal/pane` imports **neither** `internal/controlplane` **nor** `internal/cli`.
  - `internal/controlplane` imports `internal/pane` and `internal/control` only (4b).
  - `internal/control` imports none of them.
- **Exact module pins.** These are the only modules this plan adds; nothing else:
  - `github.com/charmbracelet/x/vt v0.0.0-20260913004009-c615ff2f7805` — untagged and self-described as experimental. Pin this pseudo-version; do not `go get -u` it.
  - `github.com/creack/pty v1.1.24`
  - `github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886` — **already in `go.mod` as an indirect**; Task 1 makes it direct at the same version. This is not a new module and not a version bump.
  - Already present and unchanged: `charm.land/bubbletea/v2 v2.0.9`, `charm.land/lipgloss/v2 v2.0.6`, `charm.land/bubbles/v2 v2.2.1`, `charm.land/huh/v2 v2.0.3`, `github.com/charmbracelet/x/ansi v0.11.8`. `github.com/charmbracelet/bubbletea v1.3.10` and `github.com/charmbracelet/lipgloss v1.1.0` stay for `internal/overlay`.
  - `charm.land/glamour/v2 v2.0.1` belongs to plan 4b (the supervisor view). Do not add it here.
  - `x/vt`'s own `go.mod` asks for `x/ansi v0.11.7` and `ultraviolet v0.0.0-20260303162955-0b88c25f3fff`; both are below what this repo already pins, so MVS keeps ours and **no version moves**.
- **Concurrency rules**, and they are the reason this package exists:
  - Nothing outside the pane engine touches an `Emulator` except through `Pane`'s methods.
  - `Render`, `Cursor` and `ScrollbackView` are called only from the UI goroutine.
  - The writer channel is bounded and enqueued onto without blocking: a child that stops reading loses input rather than stalling its caller.
  - Exactly one goroutine writes the PTY (the writer) and exactly one reads it (the pump).
- **Always build through `make`.** `internal/assets/embedded/` is gitignored and populated by `make sync-embedded`, which `make vet`, `make lint` and `make test` all run first. A bare `go build` on a clean checkout embeds an empty asset tree.
- **`make check` must be green after every task**: `make fmt-check vet lint test test-scripts`. The pane package's race check is separate and run by hand — `make test-race` (added in Task 3) — because a race build is slow and `make check` is the fast gate.
- **Preserve these known behaviours; do not "fix" them:**
  - `.cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md` — `control.Down` reports any `warning:` text as failure. Unchanged.
  - `.cspace/context/findings/2026-07-20-tui-browser-row-orphaned-when-project-has-no-registry-entry.md` — `Correlate` derives projects from registry entries only. Unchanged; Task 6 depends on exactly that, since a kept registry entry is what puts a stopped sandbox back on screen.
- **Findings this plan resolves** (append a timestamped entry under the finding's `## Updates` and put `(cs-finding:<slug>)` in that task's commit message):
  - Task 5 → `2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins`
  - Task 6 → `2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard`
- **Worktree:** `/Users/elliott/Projects/cspace-control-plane-4`, branch `control-plane-4a-pane-engine`, starting at `9be75b8` (the commit that added these two plans). Do not touch `/Users/elliott/Projects/cspace` or the sibling worktrees. Plan 4b is executed on `control-plane-4b-panes`, stacked on this branch's final head, in its own worktree `/Users/elliott/Projects/cspace-control-plane-4b`.
- **Module path:** `github.com/elliottregan/cspace`. Commit messages are short imperative sentences ("Add the pane engine's teardown handshake"), and every commit ends with the two-line trailer this branch's commits carry:

  ```
  Co-Authored-By: <the model doing the work> <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
  ```

---

## File Structure

New package `internal/pane` (all files `package pane`):

| File | Responsibility | Task |
|---|---|---|
| `pane.go` | package doc, `Command`, `HostShell`, `Pane`, `Open`, the four goroutines, the bounded writer, the dirty signal, resize, exit state, the teardown handshake, `ScrollbackView` | 3 |
| `emulator.go` | `Emulator`, `Scrollback`, `KeyEvent`, `KeyMod`, the key-code constants | 1 |
| `vt.go` | `newVTEmulator` — the x/vt adapter, kitty tracking, the teardown-safe `Close` | 1 |
| `keys.go` | `encodeKey` and its three tables (kitty CSI-u, xterm final, xterm tilde) | 2 |

Tests (same package):

| File | Covers | Task |
|---|---|---|
| `emulator_test.go` | `runEmulatorSuite` — the conformance suite every `Emulator` must pass (render, CPR, bracketed paste, resize, scrollback, close) — plus the two x/vt-specific probes (kitty tracking, `InputPipe`) | 1 |
| `keys_test.go` | one case per row of the key tables, both kitty states, and the degradations | 2 |
| `pane_test.go` | the engine against a real pty: output, keys, resize, drop-not-block, exit code, teardown | 3 |
| `race_test.go` | interleaved writes/resizes/teardowns, run under `-race` | 3 |

Modified elsewhere:

| File | Change | Task |
|---|---|---|
| `go.mod`, `go.sum` | `x/vt`, `ultraviolet` (Task 1), `creack/pty` (Task 3) | 1, 3 |
| `Makefile` | `test-race` target | 3 |
| `internal/control/argv.go`, `argv_test.go` | `ShellAttach` | 4 |
| `internal/cli/cmd_up.go`, `cmd_up_test.go` | `validateSandboxName` grows a shape check | 5 |
| `internal/cli/cmd_down.go`, `cmd_down_test.go` | name validation; `--keep-state` keeps the registry entry | 5, 6 |
| `internal/registry/registry.go`, `registry_test.go` | `MarkStopped` | 6 |
| `internal/control/correlate_test.go` | a `"stopped"` entry renders as a stopped row | 6 |
| `scripts/tui-smoke/tuilib.py`, `smoke.py` | **New.** The pty harness | 7 |
| `CLAUDE.md` | `make test-race`, the smoke harness | 3, 7 |
| `docs/superpowers/specs/2026-09-17-control-plane-design.md` | the teardown paragraph and `CursorPosition` on the `Emulator` interface | 3 |

---

### Task 1: The `Emulator` seam and the x/vt adapter

`internal/pane`'s whole reason for existing is that the terminal emulator is replaceable: `x/vt` is untagged, experimental, and has two known gaps this package works around. So the emulator sits behind an interface with a conformance suite, and the one implementation is an adapter.

Two facts read out of the pinned module drive this task's design, and both are load-bearing:

1. **`x/vt` answers queries through an unbuffered `io.Pipe`.** `Emulator.Read` is `e.pr.Read(p)` and every reply — CPR, device attributes, mode reports, and the bytes `SendKey`/`Paste`/`SendText` push in — is `io.WriteString(e.pw, …)`. There is no buffer. A `Write` that produces a reply **blocks until something calls `Read`**. Every test and the engine itself must have a reader running first.
2. **`vt.SafeEmulator` does not wrap `Close`, and `Emulator.Read` reads the same unexported `closed` bool that `Close` writes** — a genuine data race that `-race` reports, and which the 2026-09-17 spike shipped with. The mitigation is in `vtEmulator.Close` below: close the input pipe (synchronized by `io.Pipe` itself) instead of calling x/vt's `Close`, which touches the unguarded bool.

**Files:**
- Create: `internal/pane/emulator.go`
- Create: `internal/pane/vt.go` — every method is final here **except `SendKey`**, which hands every key straight to x/vt for now; Task 2 replaces it with the version that consults the key overlay. This task must not reference `encodeKey`: it does not exist yet, and `internal/pane` has to compile and pass `make check` at the end of every task.
- Test: `internal/pane/emulator_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type KeyMod uint16` with `ModShift`, `ModAlt`, `ModCtrl`, `ModMeta` (values 1, 2, 4, 8 — bit-for-bit `ultraviolet`'s, so the control plane converts a `tea.KeyPressMsg`'s `Mod` with a cast)
  - `type KeyEvent struct { Code rune; Mod KeyMod; Text string }`
  - the key-code constants `KeyUp KeyDown KeyRight KeyLeft KeyHome KeyEnd KeyPgUp KeyPgDown KeyInsert KeyDelete KeyEnter KeyTab KeyBackspace KeyEscape KeySpace KeyF1 … KeyF12` (all `rune`, aliased from `ultraviolet`)
  - `type Scrollback interface { Len() int; Line(i int) string }`
  - `type Emulator interface { Write([]byte) (int, error); Read([]byte) (int, error); Resize(cols, rows int); Render() string; CursorPosition() (x, y int); SendKey(KeyEvent); Paste(string); Scrollback() Scrollback; Close() error }`
  - `func newVTEmulator(cols, rows int) *vtEmulator` (unexported; `Pane` is the only caller)
  - `func (e *vtEmulator) kittyEnabled() bool` (unexported; asserted by this task's own probe and consulted by Task 2's overlay)
  - `func (e *vtEmulator) SendKey(k KeyEvent)` — the **interim** version, a straight hand-off to x/vt. Task 2 replaces its body; nothing else in `vt.go` moves.

- [ ] **Step 1: Add the dependencies**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go get github.com/charmbracelet/x/vt@v0.0.0-20260913004009-c615ff2f7805
go get github.com/charmbracelet/ultraviolet@v0.0.0-20260811164956-006e29f97886
git diff go.mod
```
Expected: `x/vt` appears in `go.mod` at exactly that pseudo-version and `ultraviolet` stays at the version it already had. **Both are still marked `// indirect` at this point, and that is correct:** `go get` records a module the main module does not yet import as indirect, and re-getting `ultraviolet` at the version already required prints nothing and changes nothing at all. Step 8's `go mod tidy` is what corrects the markers, once Steps 5-6 have written the imports. No version moves: if `x/ansi` or any `charm.land` version does, stop — something is wrong with the pin.

- [ ] **Step 2: Write the failing conformance suite**

The spec asks for "an interface test suite any `Emulator` implementation must
pass", so this file is parameterized by a constructor and never names a
concrete type in the cases themselves. The x/vt-specific probes — kitty flag
tracking, and the `InputPipe` assumption `Close` is built on — sit outside the
suite, because they are assertions about the adapter rather than about the
interface.

**The suite deliberately asserts nothing about `SendKey`.** `SendKey` is in
the interface, but *what bytes a modified key produces* is the key overlay's
contract, and the overlay is Task 2 — so the case that proves a modified key
reaches it lives in `keys_test.go`, next to the table it belongs to. Do not
add a `SendKey` case here: it would encode Task 2's behaviour into Task 1's
suite and fail before the overlay exists.

Create `internal/pane/emulator_test.go`:

```go
package pane

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// newEmulator is the constructor an implementation hands the suite. Keeping
// the suite behind one of these is what makes it a conformance suite rather
// than one implementation's tests: a vendored vt10x or a go-libghostty
// binding passes this file by adding four lines, not by editing it.
type newEmulator func(cols, rows int) Emulator

// responses collects everything an emulator wants written back to its child.
//
// Draining is not optional. x/vt answers queries by writing into an
// unbuffered io.Pipe, so a Write that produces a reply blocks until someone
// Reads — a test that skips this hangs rather than fails.
type responses struct {
	mu   sync.Mutex
	buf  strings.Builder
	done chan struct{}
}

func (r *responses) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// waitFor polls until the collected responses contain want, or the deadline
// passes. The emulator replies from the goroutine that parses the write, so
// the answer is ordered after the Write but not synchronous with it.
func (r *responses) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("emulator never answered %q; got %q", want, r.String())
}

// newTestEmulator builds an emulator with its response drain already running
// and its teardown registered.
func newTestEmulator(t *testing.T, newEmu newEmulator, cols, rows int) (Emulator, *responses) {
	t.Helper()
	e := newEmu(cols, rows)
	r := &responses{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		buf := make([]byte, 4096)
		for {
			n, err := e.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.buf.Write(buf[:n])
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = e.Close()
		<-r.done
	})
	return e, r
}

// plainScreen is the rendered screen with its styling removed and every
// line's trailing blanks trimmed, which is what a test wants to compare.
func plainScreen(e Emulator) string {
	lines := strings.Split(stripANSI(e.Render()), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

// runEmulatorSuite is the conformance suite: everything an Emulator has to
// do no matter what interprets the bytes. Each case is a subtest so a
// failure names the behaviour rather than the implementation.
func runEmulatorSuite(t *testing.T, newEmu newEmulator) {
	t.Helper()

	t.Run("renders what the child wrote", func(t *testing.T) {
		e, _ := newTestEmulator(t, newEmu, 20, 4)
		if _, err := e.Write([]byte("hello\r\nworld")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got := plainScreen(e)
		if !strings.HasPrefix(got, "hello\nworld") {
			t.Errorf("screen = %q, want it to start with hello/world", got)
		}
		if x, y := e.CursorPosition(); x != 5 || y != 1 {
			t.Errorf("cursor = (%d,%d), want (5,1)", x, y)
		}
	})

	t.Run("answers a cursor position report", func(t *testing.T) {
		e, r := newTestEmulator(t, newEmu, 20, 4)
		// Two columns of text, then the query: the answer must describe the
		// pane's own geometry, not the host terminal's.
		if _, err := e.Write([]byte("hi\x1b[6n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		r.waitFor(t, "\x1b[1;3R")
	})

	t.Run("brackets a paste only when the child asked for it", func(t *testing.T) {
		e, r := newTestEmulator(t, newEmu, 20, 4)
		e.Paste("plain")
		r.waitFor(t, "plain")
		if got := r.String(); strings.Contains(got, "\x1b[200~") {
			t.Fatalf("paste was bracketed before the child enabled it: %q", got)
		}

		if _, err := e.Write([]byte("\x1b[?2004h")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		e.Paste("bracketed")
		r.waitFor(t, "\x1b[200~bracketed\x1b[201~")
	})

	t.Run("resize changes the rendered width", func(t *testing.T) {
		e, _ := newTestEmulator(t, newEmu, 10, 3)
		if _, err := e.Write([]byte("0123456789abcdef")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := plainScreen(e); !strings.HasPrefix(got, "0123456789\nabcdef") {
			t.Errorf("at 10 columns the screen = %q, want a wrap after 10", got)
		}
		e.Resize(20, 3)
		if got, want := len(strings.Split(plainScreen(e), "\n")), 3; got != want {
			t.Errorf("after resize the screen has %d lines, want %d", got, want)
		}
	})

	t.Run("scrollback keeps lines that scrolled off", func(t *testing.T) {
		e, _ := newTestEmulator(t, newEmu, 20, 3)
		if _, err := e.Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		sb := e.Scrollback()
		if sb.Len() < 2 {
			t.Fatalf("scrollback holds %d lines, want at least the two that scrolled off", sb.Len())
		}
		if got := stripANSI(sb.Line(0)); !strings.HasPrefix(got, "one") {
			t.Errorf("oldest scrollback line = %q, want one", got)
		}
		if got := sb.Line(-1); got != "" {
			t.Errorf("out-of-range scrollback line = %q, want empty", got)
		}
	})

	t.Run("close is idempotent and ends the drain", func(t *testing.T) {
		// Built without newTestEmulator: this case owns the drain it asserts
		// on, and must not have a Cleanup closing the emulator first.
		e := newEmu(20, 4)
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 64)
			for {
				if _, err := e.Read(buf); err != nil {
					return
				}
			}
		}()
		if err := e.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not unblock the reader")
		}
		if err := e.Close(); err != nil {
			t.Errorf("second Close: %v, want nil", err)
		}
	})
}

// TestVTEmulatorConformance is the one implementation there is, run through
// the suite. This is the whole cost of adding a second one.
func TestVTEmulatorConformance(t *testing.T) {
	runEmulatorSuite(t, func(cols, rows int) Emulator { return newVTEmulator(cols, rows) })
}

// TestVTEmulatorTracksTheKittyKeyboardProtocol is x/vt-specific: the kitty
// flag stack is the adapter's own bookkeeping (x/vt parses the sequences and
// has nowhere to put them), so it is asserted against the concrete type
// rather than through the interface.
func TestVTEmulatorTracksTheKittyKeyboardProtocol(t *testing.T) {
	e := newVTEmulator(20, 4)
	r := &responses{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		buf := make([]byte, 4096)
		for {
			n, err := e.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.buf.Write(buf[:n])
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = e.Close()
		<-r.done
	})

	if e.kittyEnabled() {
		t.Error("kitty is on before the child asked for it")
	}

	// Push flags 1 (disambiguate escape codes), which is what Claude Code
	// sends when it starts.
	if _, err := e.Write([]byte("\x1b[>1u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !e.kittyEnabled() {
		t.Error("kitty is off after the child pushed flags 1")
	}

	// The query must be answered in the report form, CSI ? flags u — the
	// set form (CSI = flags ; mode u) is a different sequence and a child
	// parsing the reply would not recognize it.
	if _, err := e.Write([]byte("\x1b[?u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	r.waitFor(t, "\x1b[?1u")

	if _, err := e.Write([]byte("\x1b[<1u")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if e.kittyEnabled() {
		t.Error("kitty is still on after the child popped the stack")
	}
}

// TestVTInputPipeIsAnIOCloser locks the assumption Close is built on: x/vt's
// InputPipe hands back the io.PipeWriter itself, so closing it is what
// unblocks a blocked Read without touching the unguarded `closed` bool that
// x/vt's own Close writes. If this ever fails, Close falls back to x/vt's
// Close and the data race comes back.
func TestVTInputPipeIsAnIOCloser(t *testing.T) {
	e := newVTEmulator(10, 2)
	defer func() { _ = e.Close() }()
	if _, ok := e.term.InputPipe().(interface{ Close() error }); !ok {
		t.Fatalf("InputPipe() is %T, not an io.Closer", e.term.InputPipe())
	}
}
```

- [ ] **Step 3: Add the test helper the suite needs**

`stripANSI` is used by several tests here and by the engine's tests in Task 3. Put it at the bottom of `internal/pane/emulator_test.go`:

```go
// stripANSI removes styling so a test can compare screens as text. x/vt's
// Render always emits SGR and hyperlink escapes — there is no plain mode —
// and ansi.Strip is what the rest of this repo's view tests already use.
func stripANSI(s string) string { return ansi.Strip(s) }
```

and add `"github.com/charmbracelet/x/ansi"` to that file's imports.

- [ ] **Step 4: Run the suite to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/...`
Expected: FAIL to build — `undefined: newVTEmulator`, `undefined: Emulator`.

- [ ] **Step 5: Write `emulator.go`**

```go
// Package pane runs one child process on a pseudo-terminal behind a terminal
// emulator, and renders its screen.
//
// It knows nothing about cspace: a pane is a Command, a size, and the bytes
// that flow both ways. Everything cspace-shaped — which container, which tmux
// session, which sandbox — is decided by the caller and arrives as a Command.
//
// The emulator sits behind the Emulator interface because the one
// implementation, an adapter over github.com/charmbracelet/x/vt, is pinned to
// an untagged pseudo-version of a package its own author calls experimental.
// emulator_test.go is the conformance suite a replacement has to pass.
package pane

import uv "github.com/charmbracelet/ultraviolet"

// KeyMod is the set of modifiers held with a key.
//
// The values are ultraviolet's, bit for bit, and deliberately so: bubbletea
// v2's tea.KeyPressMsg carries a uv.KeyMod, so the control plane converts one
// with a cast — pane.KeyMod(msg.Mod) — instead of a lookup table that could
// drift. internal/controlplane owns a test that locks the two together.
type KeyMod uint16

const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
	ModMeta
)

// KeyEvent is one keypress on its way to the child.
//
// Code is the key itself: a printable rune for an ordinary key, or one of the
// Key* constants below for a special one. Text is what the key produced when
// it produced printable text, and is empty for special keys. Both come
// straight off bubbletea v2's tea.KeyPressMsg.
type KeyEvent struct {
	Code rune
	Mod  KeyMod
	Text string
}

// The key codes a KeyEvent's Code can carry, aliased from ultraviolet so the
// control plane, this package and the emulator all agree on one numbering.
// Special keys sit above unicode.MaxRune; the four "legacy" keys below are
// their C0/DEL bytes, which is what makes them addressable in both the
// classic and the kitty encodings.
const (
	KeyUp     = uv.KeyUp
	KeyDown   = uv.KeyDown
	KeyRight  = uv.KeyRight
	KeyLeft   = uv.KeyLeft
	KeyInsert = uv.KeyInsert
	KeyDelete = uv.KeyDelete
	KeyPgUp   = uv.KeyPgUp
	KeyPgDown = uv.KeyPgDown
	KeyHome   = uv.KeyHome
	KeyEnd    = uv.KeyEnd

	KeyBackspace = uv.KeyBackspace // DEL, 0x7f
	KeyTab       = uv.KeyTab       // HT, 0x09
	KeyEnter     = uv.KeyEnter     // CR, 0x0d
	KeyEscape    = uv.KeyEscape    // ESC, 0x1b
	KeySpace     = uv.KeySpace

	KeyF1  = uv.KeyF1
	KeyF2  = uv.KeyF2
	KeyF3  = uv.KeyF3
	KeyF4  = uv.KeyF4
	KeyF5  = uv.KeyF5
	KeyF6  = uv.KeyF6
	KeyF7  = uv.KeyF7
	KeyF8  = uv.KeyF8
	KeyF9  = uv.KeyF9
	KeyF10 = uv.KeyF10
	KeyF11 = uv.KeyF11
	KeyF12 = uv.KeyF12
)

// Scrollback is the history above the visible screen, oldest line first. Each
// line comes back rendered, with styling encoded as ANSI escapes, the same
// way Emulator.Render returns the visible screen. An out-of-range index is
// the empty string rather than a panic: the UI computes indices from a scroll
// offset that a concurrent write can invalidate between two calls.
type Scrollback interface {
	Len() int
	Line(i int) string
}

// Emulator interprets a child's output and renders its screen.
//
// This is the design's interface, plus CursorPosition: bubbletea's tea.View
// places the terminal cursor from it and there is no other source for where
// the child left it. Everything else is exactly as the design declares it.
//
// Read is the half that is easy to get wrong. The emulator answers terminal
// queries — cursor position reports, device attributes, mode reports — and
// hands the answers back through Read, along with everything SendKey and
// Paste encode. x/vt's implementation carries no buffer, so a Write that
// produces an answer BLOCKS until a Read consumes it. Every user of an
// Emulator must have a reader running before the first Write.
type Emulator interface {
	// Write feeds the child's output to the parser.
	Write([]byte) (int, error)
	// Read yields the bytes the emulator wants sent to the child.
	Read([]byte) (int, error)
	Resize(cols, rows int)
	// Render returns the visible screen as styled text.
	Render() string
	// CursorPosition is the child's cursor, zero-based, screen-relative.
	CursorPosition() (x, y int)
	// SendKey encodes a keypress and queues it for the child.
	SendKey(KeyEvent)
	// Paste queues text, bracketed when the child asked for bracketing.
	Paste(string)
	Scrollback() Scrollback
	// Close releases the emulator and makes an in-flight Read return io.EOF.
	// It is idempotent.
	Close() error
}
```

- [ ] **Step 6: Write `vt.go`**

```go
package pane

import (
	"io"
	"strconv"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// scrollbackLines is how much history one pane keeps. The design budgets
// panes as opened on demand rather than one per sandbox, and x/vt already
// costs a fixed 4 MiB parser buffer each (its NewEmulator allocates it
// eagerly and it is not configurable), so this is the cheap part of a pane.
const scrollbackLines = 2000

// vtEmulator adapts github.com/charmbracelet/x/vt to Emulator.
//
// It carries three things x/vt does not do for us:
//
//   - The key overlay (keys.go). x/vt's SendKey compares whole key structs
//     against literals and its default branch emits nothing unless Mod == 0,
//     so every modified special key — Ctrl+Right, Shift+Enter, Alt+Home — is
//     silently dropped. x/vt's source marks the gap in its own comment.
//   - Kitty keyboard tracking. x/vt's parser dispatches CSI > u / = u / < u /
//     ? u but nothing in the package tracks or exposes the flags, and no
//     built-in handler claims final byte 'u', so the four registrations below
//     are unopposed.
//   - A teardown-safe Close. See Close.
//   - A lock around the scrollback. See emuMu.
type vtEmulator struct {
	term *vt.SafeEmulator

	// emuMu serializes the calls that MUTATE the emulator against the calls
	// that read its scrollback, and it exists because vt.SafeEmulator wraps
	// the accessor but not the object: Scrollback() takes a read lock only
	// long enough to hand back the raw *vt.Scrollback, whose own Len and
	// Line then read s.lines with no synchronization at all — while
	// Emulator.Write pushes onto that same slice from the pane's output
	// pump. That is a genuine data race and `make test-race` reports it.
	//
	// Render and CursorPosition are NOT taken under this lock: SafeEmulator
	// already wraps both, and adding a second lock would buy nothing. Read
	// is not either, and must not be — it blocks inside the input pipe, so a
	// writer waiting on a lock the reader held would deadlock.
	emuMu sync.RWMutex

	// mu guards the kitty state. The handlers that write it run on whichever
	// goroutine called Write (the parser is synchronous inside Write);
	// kittyEnabled is read by whichever goroutine sends a key. Those are
	// different goroutines in the engine, so this is a real lock and not a
	// formality.
	mu         sync.Mutex
	kittyStack []int
	kittyFlags int
	kittyOn    bool

	closeOnce sync.Once
	closeErr  error
}

var _ Emulator = (*vtEmulator)(nil)

// newVTEmulator builds the adapter. Handlers are registered before it is
// returned — and therefore before the engine starts any goroutine — because
// x/vt's RegisterCsiHandler appends to a plain map with no lock of its own.
func newVTEmulator(cols, rows int) *vtEmulator {
	e := &vtEmulator{term: vt.NewSafeEmulator(cols, rows)}
	e.term.SetScrollbackSize(scrollbackLines)
	e.registerKitty()
	return e
}

func (e *vtEmulator) Read(p []byte) (int, error) { return e.term.Read(p) }
func (e *vtEmulator) Render() string             { return e.term.Render() }
func (e *vtEmulator) Paste(s string)             { e.term.Paste(s) }

// Write and Resize are the two calls that can push a line into the
// scrollback, so both take emuMu for write; the scrollback accessors take it
// for read. Nothing else needs it.
func (e *vtEmulator) Write(p []byte) (int, error) {
	e.emuMu.Lock()
	defer e.emuMu.Unlock()
	return e.term.Write(p)
}

func (e *vtEmulator) Resize(cols, rows int) {
	e.emuMu.Lock()
	defer e.emuMu.Unlock()
	e.term.Resize(cols, rows)
}

// CursorPosition reads both coordinates from one call. Two calls would take
// two separate read locks and could be torn across a concurrent Write, which
// is how the spike's cursor occasionally landed on a row it was never on.
func (e *vtEmulator) CursorPosition() (int, int) {
	pos := e.term.CursorPosition()
	return pos.X, pos.Y
}

// SendKey hands the key to x/vt.
//
// This is the interim version and it is what makes this task's package
// compile on its own: Task 2 adds the key overlay and replaces this body
// with the version that consults it. Until then a modified special key
// produces no bytes, which is exactly the gap Task 2 exists to close.
//
// Text is deliberately not forwarded: x/vt matches whole key structs, so a
// non-empty Text makes every special key fall through its switch.
func (e *vtEmulator) SendKey(k KeyEvent) {
	e.term.SendKey(uv.KeyPressEvent{Code: k.Code, Mod: uv.KeyMod(k.Mod)})
}

func (e *vtEmulator) Scrollback() Scrollback { return vtScrollback{e} }

// Close ends the emulator by closing its input pipe rather than calling
// x/vt's own Close, and that is this package's whole mitigation for the
// upstream race the design names.
//
// vt.Emulator.Close does two things: it sets an unexported `closed` bool and
// closes the pipe writer. vt.SafeEmulator does not wrap Close, and
// vt.Emulator.Read reads that same bool with no lock — so a Close that runs
// while the response drain sits in Read is a data race, which is exactly what
// -race reported against the 2026-09-17 spike after its own sync.Once fixed
// the double-close. Locking cannot fix it from outside: Read blocks inside
// the pipe, so a Close waiting on a write lock would wait for a Read that
// only returns once Close has run.
//
// Closing the pipe writer has the effect the caller actually needs — the
// blocked Read returns io.EOF — and io.Pipe synchronizes that internally, so
// there is no race to report. Nothing leaks by skipping the bool: the
// emulator holds no operating-system resource but this pipe, and a Write
// after close is answered by the closed pipe rather than by the flag.
func (e *vtEmulator) Close() error {
	e.closeOnce.Do(func() {
		if c, ok := e.term.InputPipe().(io.Closer); ok {
			e.closeErr = c.Close()
			return
		}
		// x/vt changed shape under us. Fall back to its own Close, which is
		// correct and reintroduces the race above; TestVTInputPipeIsAnIOCloser
		// is what tells us this happened.
		e.closeErr = e.term.Close()
	})
	return e.closeErr
}

// registerKitty tracks the kitty keyboard protocol's flag stack, which the
// overlay in keys.go consults to decide between CSI-u and the classic
// encodings. x/vt parses these sequences and dispatches them; it just has
// nowhere to put them.
func (e *vtEmulator) registerKitty() {
	e.term.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(params ansi.Params) bool {
		flags, _, _ := params.Param(0, 0) // CSI > u with no param means 0
		e.mu.Lock()
		e.kittyStack = append(e.kittyStack, flags)
		e.kittyFlags, e.kittyOn = flags, true
		e.mu.Unlock()
		return true
	})
	e.term.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(params ansi.Params) bool {
		flags, _, _ := params.Param(0, 0)
		e.mu.Lock()
		e.kittyFlags, e.kittyOn = flags, flags != 0
		e.mu.Unlock()
		return true
	})
	e.term.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 1)
		e.mu.Lock()
		for i := 0; i < n && len(e.kittyStack) > 0; i++ {
			e.kittyStack = e.kittyStack[:len(e.kittyStack)-1]
		}
		e.kittyFlags, e.kittyOn = 0, len(e.kittyStack) > 0
		if e.kittyOn {
			e.kittyFlags = e.kittyStack[len(e.kittyStack)-1]
		}
		e.mu.Unlock()
		return true
	})
	e.term.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(ansi.Params) bool {
		e.mu.Lock()
		flags := e.kittyFlags
		if !e.kittyOn {
			flags = 0
		}
		e.mu.Unlock()
		// The report form is CSI ? flags u. ansi.KittyKeyboard builds the
		// SET form (CSI = flags ; mode u), which is a different sequence —
		// the spike replied with it and a child parsing the answer would not
		// have recognized its own flags.
		//
		// This writes into the emulator's input pipe from inside the parse,
		// so both x/vt's own write lock and this adapter's emuMu are held
		// while it blocks. That does not deadlock only because the response
		// drain's Read takes neither: vt.SafeEmulator.Read is deliberately
		// unlocked and vtEmulator.Read leaves it that way. Do not "fix"
		// either of them.
		//
		// The invariant, in one line: nothing on the response-drain path
		// may take emuMu.
		_, _ = io.WriteString(e.term.InputPipe(), "\x1b[?"+strconv.Itoa(flags)+"u")
		return true
	})
}

// kittyEnabled reports whether the child has the kitty keyboard protocol on
// with a non-zero flag set — the condition under which a modified key should
// be encoded as CSI-u instead of degraded.
func (e *vtEmulator) kittyEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.kittyOn && e.kittyFlags != 0
}

// vtScrollback adapts x/vt's history to Scrollback.
//
// It holds the adapter, not the *vt.Scrollback, and takes emuMu on every
// call. Handing out the raw buffer is what the race is: see emuMu. The cost
// is one read lock per line rendered, which is nothing beside the render
// itself.
type vtScrollback struct{ e *vtEmulator }

func (s vtScrollback) Len() int {
	if s.e == nil {
		return 0
	}
	s.e.emuMu.RLock()
	defer s.e.emuMu.RUnlock()
	return s.e.term.ScrollbackLen()
}

func (s vtScrollback) Line(i int) string {
	if s.e == nil || i < 0 {
		return ""
	}
	s.e.emuMu.RLock()
	defer s.e.emuMu.RUnlock()
	sb := s.e.term.Scrollback()
	if sb == nil || i >= sb.Len() {
		return ""
	}
	line := sb.Line(i)
	if line == nil {
		return ""
	}
	return line.Render()
}
```

- [ ] **Step 7: Run the suite**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/... -v -run 'TestVTEmulator|TestVTInputPipe'`
Expected: PASS for all six conformance subtests under `TestVTEmulatorConformance`, plus `TestVTEmulatorTracksTheKittyKeyboardProtocol` and `TestVTInputPipeIsAnIOCloser`. (The regexp has to name both prefixes: `-run` is an unanchored match on the test name, and `TestVTInputPipeIsAnIOCloser` does not contain `TestVTEmulator`.)

If the cursor-position-report subtest hangs rather than fails, the drain goroutine is not running — that is the unbuffered-pipe trap, and the fix is in the test, not the adapter.

- [ ] **Step 8: Tidy the module graph and verify the whole gate**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go mod tidy
git diff go.mod
make check
```
Expected: `go mod tidy` moves `github.com/charmbracelet/x/vt` and `github.com/charmbracelet/ultraviolet` out of the `// indirect` block and into the direct `require` block — the imports Steps 5-6 wrote are what make them direct — and **no version moves**. `github.com/charmbracelet/x/ansi` is already direct and stays put. Then `make check` is all green.

- [ ] **Step 9: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
git add go.mod go.sum internal/pane
git commit -m "$(cat <<'EOF'
Add the pane package's emulator seam and its x/vt adapter

The adapter closes the emulator's input pipe instead of calling x/vt's own
Close, which writes an unguarded bool that Read reads: the race -race
reported against the design spike. Closing the pipe unblocks a blocked Read
through io.Pipe's own synchronization and touches nothing unguarded.

Kitty keyboard flags are tracked through CSI u handlers because x/vt parses
those sequences but has nowhere to put them, and the query is answered in the
report form rather than the set form.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 2: The key overlay

`x/vt`'s `SendKey` switches on whole `uv.Key` struct values. Every case but `Ctrl+<letter>` and `Shift+Tab` requires `Mod == 0`, and the default branch emits nothing at all unless `Mod == 0`. So a modified special key — Ctrl+Right, Shift+Enter, Alt+Home, Shift+PageUp — produces **zero bytes**: not a degraded key, no key. The overlay is the ~170 lines that fix exactly that, and it is the difference between Claude Code being usable in a pane and not.

The design's rule: encode modified keys as CSI-u when the child has kitty on, xterm modifier forms otherwise, and degrade a modified legacy key to its plain byte when neither applies — which is what a real terminal does.

**Files:**
- Create: `internal/pane/keys.go`
- Modify: `internal/pane/vt.go` — `SendKey`'s body only: Task 1 left it handing every key straight to x/vt, and Step 4 below replaces it with the version that asks the overlay first. Nothing else in the file moves.
- Test: `internal/pane/keys_test.go`

**Interfaces:**
- Consumes: `KeyEvent`, `KeyMod`, `Mod*` and the `Key*` constants (Task 1); `vtEmulator.SendKey`, `vtEmulator.kittyEnabled`, and the `newTestEmulator`/`responses` helpers in `emulator_test.go` (Task 1).
- Produces: `func encodeKey(k KeyEvent, kitty bool) (string, bool)` — the bytes to send and whether the overlay owns this key. `false` means "x/vt's own SendKey gets this right"; the adapter falls through to it.

- [ ] **Step 1: Write the failing table test**

Create `internal/pane/keys_test.go`:

```go
package pane

import "testing"

// Every row of the overlay's tables, and every rule that decides between
// them. The cases are written as the bytes a real terminal sends, so a
// mismatch reads as "this is not what Ghostty would have sent".
func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name  string
		key   KeyEvent
		kitty bool
		want  string
		ours  bool
	}{
		// x/vt already gets every unmodified key right, including the ones
		// whose encoding depends on the child's own mode (DECCKM arrows,
		// application keypad), which is precisely why the overlay must not
		// take them.
		{"plain rune", KeyEvent{Code: 'z', Text: "z"}, false, "", false},
		{"plain enter", KeyEvent{Code: KeyEnter}, false, "", false},
		{"plain up", KeyEvent{Code: KeyUp}, false, "", false},
		{"plain f1", KeyEvent{Code: KeyF1}, false, "", false},
		{"plain delete", KeyEvent{Code: KeyDelete}, false, "", false},
		{"ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl}, false, "", false},
		{"alt+x", KeyEvent{Code: 'x', Mod: ModAlt, Text: "x"}, false, "", false},
		{"shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, false, "", false},

		// The xterm modifier forms: CSI 1 ; mod <final> for the arrows, Home,
		// End and F1-F4, CSI n ; mod ~ for the tilde family. The modifier
		// parameter is 1 + shift(1) + alt(2) + ctrl(4) + meta(8).
		{"ctrl+right", KeyEvent{Code: KeyRight, Mod: ModCtrl}, false, "\x1b[1;5C", true},
		{"shift+up", KeyEvent{Code: KeyUp, Mod: ModShift}, false, "\x1b[1;2A", true},
		{"alt+down", KeyEvent{Code: KeyDown, Mod: ModAlt}, false, "\x1b[1;3B", true},
		{"ctrl+shift+left", KeyEvent{Code: KeyLeft, Mod: ModCtrl | ModShift}, false, "\x1b[1;6D", true},
		{"ctrl+home", KeyEvent{Code: KeyHome, Mod: ModCtrl}, false, "\x1b[1;5H", true},
		{"ctrl+end", KeyEvent{Code: KeyEnd, Mod: ModCtrl}, false, "\x1b[1;5F", true},
		{"shift+f1", KeyEvent{Code: KeyF1, Mod: ModShift}, false, "\x1b[1;2P", true},
		{"shift+f4", KeyEvent{Code: KeyF4, Mod: ModShift}, false, "\x1b[1;2S", true},
		{"shift+insert", KeyEvent{Code: KeyInsert, Mod: ModShift}, false, "\x1b[2;2~", true},
		{"ctrl+delete", KeyEvent{Code: KeyDelete, Mod: ModCtrl}, false, "\x1b[3;5~", true},
		{"shift+pgup", KeyEvent{Code: KeyPgUp, Mod: ModShift}, false, "\x1b[5;2~", true},
		{"shift+pgdown", KeyEvent{Code: KeyPgDown, Mod: ModShift}, false, "\x1b[6;2~", true},
		{"ctrl+f5", KeyEvent{Code: KeyF5, Mod: ModCtrl}, false, "\x1b[15;5~", true},
		{"ctrl+f12", KeyEvent{Code: KeyF12, Mod: ModCtrl}, false, "\x1b[24;5~", true},
		{"meta+up", KeyEvent{Code: KeyUp, Mod: ModMeta}, false, "\x1b[1;9A", true},

		// Kitty on: the four legacy keys keep their codepoints (13, 9, 127,
		// 27) and take the CSI-u form, which is the only way Shift+Enter can
		// be told from Enter. This is the probe the design's evidence table
		// names.
		{"kitty shift+enter", KeyEvent{Code: KeyEnter, Mod: ModShift}, true, "\x1b[13;2u", true},
		{"kitty shift+tab", KeyEvent{Code: KeyTab, Mod: ModShift}, true, "\x1b[9;2u", true},
		{"kitty ctrl+backspace", KeyEvent{Code: KeyBackspace, Mod: ModCtrl}, true, "\x1b[127;5u", true},
		{"kitty alt+escape", KeyEvent{Code: KeyEscape, Mod: ModAlt}, true, "\x1b[27;3u", true},
		{"kitty ctrl+shift+c", KeyEvent{Code: 'c', Mod: ModCtrl | ModShift, Text: "c"}, true, "\x1b[99;6u", true},
		// Ctrl+C with kitty ON is the overlay's, not x/vt's: rule 3 claims
		// every modified printable key, so the interrupt takes the CSI-u
		// form the child itself asked for. Pinned rather than incidental,
		// because a Claude pane always has kitty on and this is the key the
		// live verification presses.
		{"kitty ctrl+c", KeyEvent{Code: 'c', Mod: ModCtrl, Text: "c"}, true, "\x1b[99;5u", true},
		// Kitty still leaves the unmodified keys alone: a real terminal only
		// switches to CSI-u once there is something to disambiguate.
		{"kitty plain enter", KeyEvent{Code: KeyEnter}, true, "", false},

		// Kitty off: a modified legacy key degrades to its plain byte, which
		// is what a terminal without CSI-u does. x/vt would emit nothing.
		{"shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModShift}, false, "\r", true},
		{"shift+backspace degrades", KeyEvent{Code: KeyBackspace, Mod: ModShift}, false, "\x7f", true},
		{"alt+enter keeps its esc", KeyEvent{Code: KeyEnter, Mod: ModAlt}, false, "\x1b\r", true},
		{"ctrl+shift+enter degrades", KeyEvent{Code: KeyEnter, Mod: ModCtrl | ModShift}, false, "\r", true},

		// A printable key with only Shift held is its own text. x/vt's
		// default branch drops it because Mod != 0; the terminal that
		// produced it already applied the shift.
		{"shift+a", KeyEvent{Code: 'A', Mod: ModShift, Text: "A"}, false, "A", true},
		// ...but with no text there is nothing to send, so leave it to x/vt
		// rather than inventing bytes.
		{"shift with no text", KeyEvent{Code: 'a', Mod: ModShift}, false, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ours := encodeKey(tc.key, tc.kitty)
			if ours != tc.ours {
				t.Fatalf("encodeKey ownership = %v, want %v (got %q)", ours, tc.ours, got)
			}
			if ours && got != tc.want {
				t.Errorf("encodeKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestModParam(t *testing.T) {
	cases := []struct {
		mod  KeyMod
		want int
	}{
		{0, 1},
		{ModShift, 2},
		{ModAlt, 3},
		{ModCtrl, 5},
		{ModCtrl | ModShift, 6},
		{ModMeta, 9},
		{ModShift | ModAlt | ModCtrl | ModMeta, 16},
	}
	for _, tc := range cases {
		if got := modParam(tc.mod); got != tc.want {
			t.Errorf("modParam(%d) = %d, want %d", tc.mod, got, tc.want)
		}
	}
}

// TestVTEmulatorSendsAModifiedKeyThroughTheOverlay is the wiring, not the
// table: the table above proves encodeKey is right, and this proves
// vtEmulator.SendKey actually asks it. Task 1 shipped SendKey as a straight
// hand-off to x/vt, whose switch drops Ctrl+Right entirely — zero bytes, not
// a degraded key — so this case fails until Step 4 replaces that body.
//
// It reuses the drain helpers from emulator_test.go: x/vt answers through an
// unbuffered pipe, so nothing a SendKey pushes is observable without a
// reader already running.
func TestVTEmulatorSendsAModifiedKeyThroughTheOverlay(t *testing.T) {
	e, r := newTestEmulator(t, func(cols, rows int) Emulator { return newVTEmulator(cols, rows) }, 20, 4)

	// The overlay owns this one: kitty is off, so it is the xterm modifier
	// form, and x/vt on its own would have emitted nothing.
	e.SendKey(KeyEvent{Code: KeyRight, Mod: ModCtrl})
	r.waitFor(t, "\x1b[1;5C")

	// ...and an unmodified key still falls through to x/vt, which encodes it
	// mode-sensitively. The overlay must not claim it.
	e.SendKey(KeyEvent{Code: KeyEnter})
	r.waitFor(t, "\r")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/... -run 'TestEncodeKey|TestModParam|TestVTEmulatorSendsAModifiedKey'`
Expected: FAIL to build — `undefined: encodeKey`, `undefined: modParam`.

- [ ] **Step 3: Write `keys.go`**

```go
package pane

import (
	"strconv"
	"unicode"
)

// encodeKey is the overlay: the keys this package encodes itself because
// x/vt's SendKey does not.
//
// x/vt switches on whole key structs. Every case but Ctrl+<letter> and
// Shift+Tab requires Mod == 0, and its default branch emits nothing unless
// Mod == 0 — so a modified special key produces no bytes at all, which is
// worse than a wrong encoding because the keystroke simply vanishes. Its
// source marks the gap in its own comment ("Support Kitty, CSI u, and XTerm
// modifyOtherKeys").
//
// The rules, in the order they are applied:
//
//  1. Nothing modified goes through here. x/vt encodes unmodified keys
//     correctly AND mode-sensitively — DECCKM decides between ESC O A and
//     ESC [ A for Up, the application keypad decides the keypad forms — and
//     none of that state is reachable from outside the package.
//  2. Shift+Tab with kitty off is x/vt's, which emits ESC [ Z.
//  3. With kitty on, a modified key takes the CSI-u form: the four legacy
//     keys by their fixed codepoints, printable keys by their rune.
//  4. Without kitty, a modified arrow / Home / End / F1-F4 takes the xterm
//     CSI 1 ; mod <final> form, and the tilde family CSI n ; mod ~.
//  5. Without kitty, a modified legacy key degrades to its plain byte (with
//     an ESC prefix when Alt is held), which is what a terminal that cannot
//     express the modifier does.
//  6. A printable key held with Shift alone is its own text: the terminal
//     already applied the shift, and x/vt would drop it for Mod != 0.
//
// ok == false means "x/vt has this one"; the adapter falls through to
// SendKey.
func encodeKey(k KeyEvent, kitty bool) (string, bool) {
	if k.Mod == 0 {
		return "", false // rule 1
	}
	if !kitty && k.Mod == ModShift && k.Code == KeyTab {
		return "", false // rule 2
	}

	if kitty { // rule 3
		if cp, ok := kittyCodepoint(k.Code); ok {
			return csiU(cp, k.Mod), true
		}
		if isPrintable(k.Code) {
			return csiU(int(k.Code), k.Mod), true
		}
	}

	if final, ok := xtermFinal[k.Code]; ok { // rule 4
		return "\x1b[1;" + strconv.Itoa(modParam(k.Mod)) + string(final), true
	}
	if n, ok := xtermTilde[k.Code]; ok {
		return "\x1b[" + strconv.Itoa(n) + ";" + strconv.Itoa(modParam(k.Mod)) + "~", true
	}

	if b, ok := legacyByte[k.Code]; ok { // rule 5
		if k.Mod&ModAlt != 0 {
			return "\x1b" + b, true
		}
		return b, true
	}

	if k.Mod == ModShift && k.Text != "" { // rule 6
		return k.Text, true
	}
	return "", false
}

// modParam is the xterm/kitty modifier parameter: 1 plus a bitmask of
// shift 1, alt 2, ctrl 4, meta 8. Both encodings agree on these four bits.
func modParam(mod KeyMod) int {
	n := 1
	if mod&ModShift != 0 {
		n += 1
	}
	if mod&ModAlt != 0 {
		n += 2
	}
	if mod&ModCtrl != 0 {
		n += 4
	}
	if mod&ModMeta != 0 {
		n += 8
	}
	return n
}

func csiU(code int, mod KeyMod) string {
	return "\x1b[" + strconv.Itoa(code) + ";" + strconv.Itoa(modParam(mod)) + "u"
}

// isPrintable reports whether a key code is an ordinary character rather than
// one of the special keys. ultraviolet puts every special key above
// unicode.MaxRune, so the test is exact rather than a guess; C0 and DEL are
// excluded because the legacy table owns those.
func isPrintable(code rune) bool {
	return code >= 0x20 && code != 0x7f && code <= unicode.MaxRune
}

// kittyCodepoint is the stable codepoint the kitty protocol reports for the
// four keys whose classic encoding is a bare control byte. They are the only
// keys whose modified form is otherwise unrepresentable, which is why
// Shift+Enter is the probe the design's evidence table records.
func kittyCodepoint(code rune) (int, bool) {
	switch code {
	case KeyEnter:
		return 13, true
	case KeyTab:
		return 9, true
	case KeyBackspace:
		return 127, true
	case KeyEscape:
		return 27, true
	}
	return 0, false
}

// legacyByte is what each of those four keys degrades to when the child has
// no way to express the modifier. This is what a real terminal does; x/vt
// sends nothing at all.
var legacyByte = map[rune]string{
	KeyEnter:     "\r",
	KeyTab:       "\t",
	KeyBackspace: "\x7f",
	KeyEscape:    "\x1b",
}

// xtermFinal is the CSI 1 ; mod <final> family: the arrows, Home, End, and
// F1-F4 — which migrate out of their SS3 forms (ESC O P) into the CSI form
// the moment a modifier is present, exactly as xterm does.
var xtermFinal = map[rune]byte{
	KeyUp:    'A',
	KeyDown:  'B',
	KeyRight: 'C',
	KeyLeft:  'D',
	KeyHome:  'H',
	KeyEnd:   'F',
	KeyF1:    'P',
	KeyF2:    'Q',
	KeyF3:    'R',
	KeyF4:    'S',
}

// xtermTilde is the CSI n ; mod ~ family: Insert, Delete, the page keys, and
// F5-F12.
var xtermTilde = map[rune]int{
	KeyInsert: 2,
	KeyDelete: 3,
	KeyPgUp:   5,
	KeyPgDown: 6,
	KeyF5:     15,
	KeyF6:     17,
	KeyF7:     18,
	KeyF8:     19,
	KeyF9:     20,
	KeyF10:    21,
	KeyF11:    23,
	KeyF12:    24,
}
```

- [ ] **Step 4: Modify `internal/pane/vt.go` to call the overlay**

The overlay exists now, so `SendKey` stops handing everything to x/vt.
Replace the whole `SendKey` method in `internal/pane/vt.go` — Task 1's
interim body, the two-line one whose comment says Task 2 replaces it — with
exactly this:

```go
// SendKey hands x/vt what it encodes correctly and encodes the rest here.
// keys.go owns that decision; this is the only caller.
//
// Task 1 shipped this as a straight hand-off to x/vt, which drops every
// modified special key — Ctrl+Right, Shift+Enter, Alt+Home — because its
// switch matches whole key structs and its default branch emits nothing
// unless Mod is zero. encodeKey's second return is what says "x/vt has this
// one"; when it does, the fall-through below is unchanged from Task 1.
func (e *vtEmulator) SendKey(k KeyEvent) {
	if seq, ok := encodeKey(k, e.kittyEnabled()); ok {
		e.term.SendText(seq)
		return
	}
	// Text is deliberately not forwarded: x/vt matches whole key structs, so
	// a non-empty Text makes every special key fall through its switch.
	e.term.SendKey(uv.KeyPressEvent{Code: k.Code, Mod: uv.KeyMod(k.Mod)})
}
```

`vt.go`'s import block does not move: `uv` is still used by the
fall-through, and `encodeKey` is in this package.

- [ ] **Step 5: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/... -run 'TestEncodeKey|TestModParam|TestVTEmulatorSendsAModifiedKey' -v`
Expected: PASS, one subtest per row of the table (36), plus `TestModParam` and `TestVTEmulatorSendsAModifiedKeyThroughTheOverlay`. If the last one hangs rather than fails, the drain goroutine is not running — that is the unbuffered-pipe trap, and the fix is in the test.

- [ ] **Step 6: Run the whole gate**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && make check`
Expected: green — including Task 1's conformance suite, which this task's change to `SendKey` must not disturb (it asserts nothing about keys).

- [ ] **Step 7: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
git add internal/pane
git commit -m "$(cat <<'EOF'
Encode the modified keys x/vt drops

x/vt's SendKey matches whole key structs and its default branch emits nothing
unless Mod is zero, so Ctrl+Right, Shift+Enter and every other modified
special key produced no bytes at all. The overlay encodes them as CSI-u when
the child has the kitty protocol on, as xterm modifier forms otherwise, and
degrades a modified legacy key to its plain byte when neither applies — what
a real terminal does instead of swallowing the key.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 3: The PTY engine

One `Pane` is a child on a pty plus an `Emulator`, and four goroutines that keep the two fed without ever letting either block the UI.

The teardown handshake is the part to get exactly right. The design's order is "stop accepting input, cancel the response drain and wait for it, close the PTY, wait for the process, then Close the emulator once". The drain cannot be cancelled before the emulator is closed — it is blocked inside a pipe read that only the close unblocks — so the order below is input, child, pty, emulator, drain, and the property the design was buying (a `Close` that cannot race a `Read`) is bought instead by Task 1's `vtEmulator.Close`, which closes the pipe rather than the flag. `make test-race` is the proof.

**Files:**
- Create: `internal/pane/pane.go`
- Test: `internal/pane/pane_test.go`, `internal/pane/race_test.go`
- Modify: `go.mod`, `go.sum`, `Makefile`, `CLAUDE.md`, `docs/superpowers/specs/2026-09-17-control-plane-design.md`

**Interfaces:**
- Consumes: `Emulator`, `newVTEmulator`, `KeyEvent` (Tasks 1-2).
- Produces:
  - `type Command struct { Path string; Args []string; Env []string; Dir string }`
  - `func HostShell() Command` — the `Command` for the operator's own login shell, `$SHELL -l`, with no container in the picture. Defined in `pane.go` by this task (Step 5) and consumed by plan 4b's Tasks 2 and 7; nothing in this plan calls it.
  - `func Open(cmd Command, cols, rows int) (*Pane, error)`
  - `func (p *Pane) Dirty() <-chan struct{}`
  - `func (p *Pane) SendKey(k KeyEvent)`
  - `func (p *Pane) Paste(text string)`
  - `func (p *Pane) Resize(cols, rows int) error`
  - `func (p *Pane) Render() string`
  - `func (p *Pane) Cursor() (x, y int)`
  - `func (p *Pane) ScrollbackLen() int`
  - `func (p *Pane) ScrollbackView(offset, rows int) string`
  - `func (p *Pane) Exited() (code int, err error, ok bool)`
  - `func (p *Pane) Dropped() uint64`
  - `func (p *Pane) Close(ctx context.Context) error`

- [ ] **Step 1: Add creack/pty**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go get github.com/creack/pty@v1.1.24
git diff go.mod
```
Expected: one added line at exactly that version, marked `// indirect` — nothing imports it yet, which is what `go get` records. Step 11's `go mod tidy` — "Tidy the module graph, run the whole gate and commit" — promotes it once `pane.go` exists. Nothing else moves.

- [ ] **Step 2: Write the failing engine tests**

Create `internal/pane/pane_test.go`:

```go
package pane

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// shell is the interpreter the engine's tests drive. bash because two probes
// need its `read -s -d`; both macOS and the Linux CI runners have it.
func shell(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	return path
}

// openTestPane starts a pane running `bash -c script` and registers its
// teardown.
func openTestPane(t *testing.T, script string, cols, rows int) *Pane {
	t.Helper()
	sh := shell(t)
	p, err := Open(Command{
		Path: sh,
		Args: []string{"bash", "-c", script},
		Env:  []string{"TERM=xterm-256color", "COLORTERM=truecolor", "PS1="},
	}, cols, rows)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return p
}

// waitForScreen polls the rendered screen until it contains want. The pane
// signals on Dirty, but a test that only waited on that signal would race the
// child's own scheduling; polling the thing being asserted is simpler and
// does not flake.
func waitForScreen(t *testing.T, p *Pane, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = stripANSI(p.Render())
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("screen never contained %q; last screen:\n%s", want, last)
	return ""
}

func TestPaneRendersItsChildsOutput(t *testing.T) {
	p := openTestPane(t, `printf 'hello pane'; sleep 30`, 40, 6)
	waitForScreen(t, p, "hello pane")
}

func TestPaneSignalsOutput(t *testing.T) {
	p := openTestPane(t, `sleep 0.2; printf 'late'; sleep 30`, 40, 6)
	select {
	case <-p.Dirty():
	case <-time.After(10 * time.Second):
		t.Fatal("the pane never signalled that it had output")
	}
}

func TestPaneSendsKeysToItsChild(t *testing.T) {
	p := openTestPane(t, `read -r line; printf 'GOT[%s]' "$line"; sleep 30`, 40, 6)
	// Give bash time to reach the read before typing at it.
	time.Sleep(300 * time.Millisecond)
	for _, r := range "hi" {
		p.SendKey(KeyEvent{Code: r, Text: string(r)})
	}
	p.SendKey(KeyEvent{Code: KeyEnter})
	waitForScreen(t, p, "GOT[hi]")
}

func TestPaneAnswersACursorPositionReportFromThePane(t *testing.T) {
	// The child asks the terminal where the cursor is. The answer must come
	// from this pane's own emulator, describing this pane's geometry.
	p := openTestPane(t,
		`printf '\033[6n'; read -r -s -d R reply; printf 'CPR[%s]' "${reply#$'\033'[}"; sleep 30`,
		40, 6)
	waitForScreen(t, p, "CPR[1;1")
}

func TestPaneResizeReachesTheChild(t *testing.T) {
	p := openTestPane(t, `sleep 0.5; stty size; sleep 30`, 40, 6)
	if err := p.Resize(90, 20); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	waitForScreen(t, p, "20 90")
}

func TestPaneDropsInputForAChildThatNeverReads(t *testing.T) {
	// A child in raw mode that never drains its input is the exact shape the
	// design exists to survive: the queue fills, input is dropped, and the
	// pane stays renderable and closable.
	p := openTestPane(t, `printf 'ALIVE'; sleep 30`, 40, 6)
	waitForScreen(t, p, "ALIVE")

	// Paste is the flood: it is the only public way to push bulk input, and
	// it takes the same route a key does — into the emulator, out through
	// the response drain, onto the bounded queue. The child enabled no
	// bracketing, so these are 8 MiB of plain bytes.
	chunk := strings.Repeat("x", 4096)
	for i := 0; i < writeQueue*4; i++ {
		p.Paste(chunk)
	}
	if p.Dropped() == 0 {
		t.Error("nothing was dropped; the queue is not bounded")
	}
	// Still alive: rendering and teardown must not have been taken hostage.
	if got := stripANSI(p.Render()); !strings.Contains(got, "ALIVE") {
		t.Errorf("screen after the flood = %q", got)
	}

	// ...and closable, which is the half that is easy to get wrong. The
	// writer is parked inside ptmx.Write on a child that will not read for
	// another thirty seconds, so a teardown that joined the writer BEFORE
	// closing the pty would sit here until the sleep ended. In the control
	// plane this call is on the UI goroutine.
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closed <- p.Close(ctx)
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close blocked on a child that never reads its input")
	}
}

func TestPaneReportsTheChildsExitCode(t *testing.T) {
	p := openTestPane(t, `exit 3`, 40, 6)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if code, err, ok := p.Exited(); ok {
			if err != nil {
				t.Fatalf("Exited err = %v", err)
			}
			if code != 3 {
				t.Fatalf("exit code = %d, want 3", code)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the pane never reported the child's exit")
}

func TestPaneCloseIsIdempotentAndLeavesNoGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	sh := shell(t)
	p, err := Open(Command{Path: sh, Args: []string{"bash", "-c", "sleep 30"}}, 40, 6)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Close joins all four, so the count comes back down. The short settle
	// loop is for the runtime's own bookkeeping goroutines, not for the
	// pane's: a leak shows as a count that stays four higher forever.
	after := runtime.NumGoroutine()
	for i := 0; i < 50 && after > before; i++ {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before {
		t.Errorf("goroutines went %d -> %d; the handshake left some running", before, after)
	}
}

func TestPaneScrollbackViewWalksBackThroughHistory(t *testing.T) {
	p := openTestPane(t, `for i in 1 2 3 4 5 6 7 8; do printf 'line%s\r\n' "$i"; done; sleep 30`, 20, 4)
	waitForScreen(t, p, "line8")
	if p.ScrollbackLen() < 4 {
		t.Fatalf("scrollback holds %d lines, want the ones that scrolled off", p.ScrollbackLen())
	}
	if got := stripANSI(p.ScrollbackView(0, 4)); !strings.Contains(got, "line8") {
		t.Errorf("offset 0 = %q, want the live screen", got)
	}
	// The largest meaningful offset is ScrollbackLen(), so ask for that
	// rather than a literal. Eight lines into a four-row screen scroll FIVE
	// off, not four — the first three only move the cursor down — so a
	// hard-coded 4 lands on line2 and this assertion would fail against a
	// correct implementation.
	back := stripANSI(p.ScrollbackView(p.ScrollbackLen(), 4))
	if !strings.Contains(back, "line1") {
		t.Errorf("offset %d = %q, want the top of the history", p.ScrollbackLen(), back)
	}
	// The offset is clamped, so an over-scroll shows the oldest lines rather
	// than an empty screen.
	if got := stripANSI(p.ScrollbackView(1000, 4)); !strings.Contains(got, "line1") {
		t.Errorf("over-scrolled view = %q, want the oldest lines", got)
	}
}
```

- [ ] **Step 3: Write the race exerciser**

Create `internal/pane/race_test.go`:

```go
package pane

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPanesUnderConcurrentUse is the port of the design spike's race driver:
// several panes, input and resizes and renders interleaved, then teardown —
// which is the shape that produced every data race the spike found. It is an
// ordinary test; `make test-race` is what makes it mean something.
func TestPanesUnderConcurrentUse(t *testing.T) {
	const panes = 3
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var ps []*Pane
	for i := 0; i < panes; i++ {
		ps = append(ps, openTestPane(t, `while :; do printf 'tick '; sleep 0.05; done`, 60, 10))
	}

	var wg sync.WaitGroup
	for _, p := range ps {
		p := p
		wg.Add(1)
		go func() { // input
			defer wg.Done()
			for i := 0; i < 50; i++ {
				p.SendKey(KeyEvent{Code: 'a', Text: "a"})
				p.SendKey(KeyEvent{Code: KeyUp, Mod: ModCtrl})
				p.Paste("pasted text")
				time.Sleep(time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() { // resize
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = p.Resize(60+i%5, 10+i%3)
				time.Sleep(2 * time.Millisecond)
			}
		}()
		wg.Add(1)
		go func() { // the UI goroutine's job
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = p.Render()
				_, _ = p.Cursor()
				_ = p.ScrollbackView(i%3, 10)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	for _, p := range ps {
		if err := p.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	// One last render after teardown must not panic and must still show the
	// child's last screen: an exited pane keeps what it painted.
	if got := stripANSI(ps[0].Render()); !strings.Contains(got, "tick") {
		t.Errorf("screen after close = %q, want the last frame", got)
	}
}
```

- [ ] **Step 4: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/... -run TestPane`
Expected: FAIL to build — `undefined: Open`, `undefined: Command`, `undefined: writeQueue`.

- [ ] **Step 5: Write `pane.go`**

```go
package pane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	// writeQueue is how many chunks of pending input one pane holds before it
	// starts dropping. Chunks, not bytes: a chunk is at most responseChunk
	// bytes, so the ceiling is a couple of megabytes of queued input for a
	// child that has stopped reading entirely.
	writeQueue = 512

	// outputChunk and responseChunk are the two read buffers. The output side
	// is large because a full-screen repaint from a program like Claude Code
	// arrives in one go; the response side is small because it only ever
	// carries query answers and encoded keys.
	outputChunk   = 64 * 1024
	responseChunk = 4 * 1024

	// killGrace is how long a child gets between the hangup and the kill.
	killGrace = 2 * time.Second
)

// Command is the child one pane runs.
//
// Args is an argv in the exec convention — Args[0] is the program's name, not
// its path — which is what control.AttachArgv already produces for a
// container exec. Env is appended to this process's environment, so a later
// entry shadows an inherited one. An empty Dir inherits the caller's.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// HostShell is the Command for a pane running the operator's own login shell
// on the host, with no container in the picture. $SHELL is what the person
// chose; /bin/bash is the fallback, because a macOS without it is not a
// machine cspace runs on.
func HostShell() Command {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	name := shell
	if i := strings.LastIndex(shell, "/"); i >= 0 {
		name = shell[i+1:]
	}
	return Command{Path: shell, Args: []string{name, "-l"}}
}

// Pane is one child process on a pseudo-terminal behind an Emulator.
//
// Four goroutines run per pane, and the split is the whole design:
//
//   - pump:   pty -> emulator. The only reader of the pty.
//   - drain:  emulator -> writes. Carries the emulator's own answers (cursor
//     position reports, device attributes, mode reports) and everything
//     SendKey and Paste encode.
//   - writer: writes -> pty. The only writer of the pty, so a child that has
//     stopped reading blocks this goroutine and nothing else.
//   - waiter: cmd.Wait, which is where the exit code comes from.
//
// Enqueueing onto the writes channel never blocks: the channel is bounded and
// a full one drops. That is deliberate — the caller is often the UI
// goroutine, and a wedged child must cost its own input, not the window.
type Pane struct {
	emu  Emulator
	cmd  *exec.Cmd
	ptmx *os.File

	writes chan []byte
	dirty  chan struct{}

	stopWriter chan struct{}
	writerDone chan struct{}
	pumpDone   chan struct{}
	drainDone  chan struct{}
	waitDone   chan struct{}

	dropped atomic.Uint64

	mu       sync.Mutex
	exited   bool
	exitCode int
	exitErr  error

	closeOnce sync.Once
	closeErr  error
}

// Open starts cmd on a new pseudo-terminal of the given size and begins
// interpreting its output.
func Open(cmd Command, cols, rows int) (*Pane, error) {
	return open(cmd, cols, rows, newVTEmulator(cols, rows))
}

// open is Open with the emulator injected, so a replacement implementation
// can be driven through the whole engine by a test.
func open(cmd Command, cols, rows int, emu Emulator) (*Pane, error) {
	if cmd.Path == "" || len(cmd.Args) == 0 {
		_ = emu.Close()
		return nil, errors.New("pane: empty command")
	}
	if cols <= 0 || rows <= 0 {
		_ = emu.Close()
		return nil, fmt.Errorf("pane: bad size %dx%d", cols, rows)
	}

	child := &exec.Cmd{Path: cmd.Path, Args: cmd.Args, Dir: cmd.Dir}
	child.Env = append(os.Environ(), cmd.Env...)

	ptmx, err := pty.StartWithSize(child, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		_ = emu.Close()
		return nil, fmt.Errorf("pane: start %s: %w", cmd.Path, err)
	}

	p := &Pane{
		emu:        emu,
		cmd:        child,
		ptmx:       ptmx,
		writes:     make(chan []byte, writeQueue),
		dirty:      make(chan struct{}, 1),
		stopWriter: make(chan struct{}),
		writerDone: make(chan struct{}),
		pumpDone:   make(chan struct{}),
		drainDone:  make(chan struct{}),
		waitDone:   make(chan struct{}),
	}
	go p.writer()
	go p.drain()
	go p.pump()
	go p.waiter()
	return p, nil
}

// Dirty signals that the pane has new output worth drawing. It carries one
// slot: a burst of writes between two reads collapses into one signal, which
// is the coalescing the design asks for. Reading it is how the control plane
// schedules a redraw.
func (p *Pane) Dirty() <-chan struct{} { return p.dirty }

// SendKey encodes a keypress and queues it for the child.
func (p *Pane) SendKey(k KeyEvent) { p.emu.SendKey(k) }

// Paste queues text, bracketed when the child asked for bracketing.
func (p *Pane) Paste(text string) { p.emu.Paste(text) }

// Resize retells both halves: the emulator, so Render reflows, and the pty,
// so the child gets SIGWINCH and re-queries its size. The emulator goes
// first deliberately — by the time the child reacts to the signal, the
// screen it is about to repaint is already the new shape.
//
// The error is the ioctl's. A pane whose pty has gone returns one here; the
// caller fans a resize out over every pane and must not let one failure stop
// the rest.
func (p *Pane) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane: bad size %dx%d", cols, rows)
	}
	p.emu.Resize(cols, rows)
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Render is the visible screen as styled text. UI goroutine only.
func (p *Pane) Render() string { return p.emu.Render() }

// Cursor is the child's cursor within the screen, zero-based.
func (p *Pane) Cursor() (int, int) { return p.emu.CursorPosition() }

// ScrollbackLen is how far back the history goes, which is also the largest
// meaningful scroll offset.
func (p *Pane) ScrollbackLen() int { return p.emu.Scrollback().Len() }

// ScrollbackView renders rows lines of the pane ending offset lines above the
// live screen: offset 0 is the live screen itself, and the offset is clamped
// to the history that exists, so an over-scroll shows the oldest lines rather
// than blank ones.
func (p *Pane) ScrollbackView(offset, rows int) string {
	if rows <= 0 {
		return ""
	}
	screen := strings.Split(p.emu.Render(), "\n")
	sb := p.emu.Scrollback()
	history := sb.Len()
	total := history + len(screen)

	if offset < 0 {
		offset = 0
	}
	if offset > history {
		offset = history
	}
	end := total - offset
	start := end - rows
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, rows)
	for i := start; i < end; i++ {
		if i < history {
			out = append(out, sb.Line(i))
			continue
		}
		out = append(out, screen[i-history])
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// Exited reports the child's exit once the waiter has seen it. ok is false
// while the child is still running. err is non-nil only when the wait itself
// failed, never for an ordinary non-zero exit — that is what code is for.
func (p *Pane) Exited() (code int, err error, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode, p.exitErr, p.exited
}

// Dropped is how many bytes of input were thrown away because the child was
// not reading. Non-zero means a key or a paste was lost.
func (p *Pane) Dropped() uint64 { return p.dropped.Load() }

// Close runs the teardown handshake and joins every goroutine. It is
// idempotent and safe to call on a pane whose child has already exited.
//
// The order is: stop accepting input, end the child's process group and reap
// it, close the pty — which both returns a writer parked on a child that
// stopped reading AND gives the output pump its EOF — then close the
// emulator, which is what returns the drain's blocked Read, and join the
// drain.
//
// The design asks for the drain to be stopped BEFORE the emulator is closed,
// so that no Read is in flight when Close runs. That is not reachable from
// outside x/vt: the drain is blocked inside the emulator's pipe and only the
// close unblocks it. The property the ordering was buying — a Close that
// cannot race a Read — is bought instead by vtEmulator.Close, which closes
// the pipe (synchronized by io.Pipe) rather than x/vt's unguarded flag. See
// vt.go, and `make test-race`.
func (p *Pane) Close(ctx context.Context) error {
	p.closeOnce.Do(func() { p.closeErr = p.shutdown(ctx) })
	return p.closeErr
}

func (p *Pane) shutdown(ctx context.Context) error {
	// Stop accepting new input, then end the child and reap it.
	close(p.stopWriter)

	p.endChild(ctx)
	<-p.waitDone

	// The pty closes BEFORE the writer is joined, and that order is
	// load-bearing rather than cosmetic. writer() only observes stopWriter
	// between two writes; once it is parked inside ptmx.Write on a child
	// that has stopped reading — the exact case this package exists to
	// survive — nothing returns it but the child dying or this Close.
	// Joining first would hang the whole teardown on a wedged pane, and in
	// the control plane that is the UI goroutine.
	_ = p.ptmx.Close()
	<-p.writerDone
	<-p.pumpDone

	err := p.emu.Close()
	<-p.drainDone
	return err
}

// endChild hangs the child up and then insists, and it signals the process
// GROUP rather than the process: pty.StartWithSize starts the child with
// Setsid, so it leads its own session and group and its own children — a
// shell's jobs, a container exec's helpers — are in that group. Signalling
// the process alone is what leaves them behind.
func (p *Pane) endChild(ctx context.Context) {
	if p.cmd.Process == nil {
		return
	}
	// Already reaped: cmd.Wait has returned, so the kernel is free to hand
	// that pid to somebody else, and -pgid would signal a stranger. An
	// exited pane keeps its last screen until the control plane closes it,
	// which can be minutes — plenty of time for the pid to be reused.
	select {
	case <-p.waitDone:
		return
	default:
	}
	pgid := p.cmd.Process.Pid // Setsid makes the child its own group leader
	_ = syscall.Kill(-pgid, syscall.SIGHUP)
	select {
	case <-p.waitDone:
		return
	case <-ctx.Done():
	case <-time.After(killGrace):
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// writer is the only goroutine that writes the pty. It stops on the teardown
// signal as well as on a write error, so a pane killed while its queue is
// idle does not leave it parked forever.
func (p *Pane) writer() {
	defer close(p.writerDone)
	for {
		select {
		case <-p.stopWriter:
			return
		case b := <-p.writes:
			if _, err := p.ptmx.Write(b); err != nil {
				return
			}
		}
	}
}

// drain carries the emulator's replies to the child: cursor position
// reports, device attributes, mode reports, and everything SendKey and Paste
// encode. It ends when the emulator is closed.
func (p *Pane) drain() {
	defer close(p.drainDone)
	buf := make([]byte, responseChunk)
	for {
		n, err := p.emu.Read(buf)
		if n > 0 {
			p.enqueue(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// pump feeds the child's output to the parser and schedules a redraw. It ends
// when the pty errors, which is the child exiting or the teardown closing it.
func (p *Pane) pump() {
	defer close(p.pumpDone)
	buf := make([]byte, outputChunk)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			_, _ = p.emu.Write(buf[:n])
			p.markDirty()
		}
		if err != nil {
			// The last frame before EOF still has to be drawn, and an exit
			// changes how the pane renders.
			p.markDirty()
			return
		}
	}
}

// waiter reaps the child and records how it went. It deliberately does NOT
// close the emulator: an exited pane keeps its last screen until the control
// plane closes it, which is what lets the UI show the exit reason over the
// frame the child left behind.
func (p *Pane) waiter() {
	defer close(p.waitDone)
	err := p.cmd.Wait()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code, err = exitErr.ExitCode(), nil
	}
	p.mu.Lock()
	p.exited, p.exitCode, p.exitErr = true, code, err
	p.mu.Unlock()
	p.markDirty()
}

// enqueue schedules bytes for the child without ever blocking. The copy is
// mandatory: drain and pump both reuse their buffers.
func (p *Pane) enqueue(b []byte) {
	if len(b) == 0 {
		return
	}
	cp := append([]byte(nil), b...)
	select {
	case p.writes <- cp:
	default:
		p.dropped.Add(uint64(len(b)))
	}
}

func (p *Pane) markDirty() {
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}
```

- [ ] **Step 6: Run the engine tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/pane/... -v`
Expected: PASS. If `TestPaneAnswersACursorPositionReportFromThePane` fails with an empty `CPR[]`, the drain is not reaching the pty — check that `enqueue` is called from `drain` and that `writer` is running.

- [ ] **Step 7: Add the race target and run it**

Add to the `.PHONY` line in `Makefile` (after `test-scripts`) the word `test-race`, and this target below `test-scripts`:

```make
# Race check for the pane engine: four goroutines per pane around an emulator
# whose upstream does not guard its own Close. Deliberately not part of
# `make check` — a race build is slow and this is the one package that needs
# it. Run it after touching internal/pane.
test-race: sync-embedded
	go test -race -count=1 ./internal/pane/...
```

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && make test-race`
Expected: PASS with **no** `WARNING: DATA RACE` anywhere in the output. A race here means Task 1's `Close` mitigation is not doing its job; fix it there, not by weakening the test.

- [ ] **Step 8: Verify the dependency direction**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go list -deps ./internal/pane | grep -E 'elliottregan/cspace/internal/(cli|controlplane|control)' && echo "LEAK" || echo "clean"
```
Expected: `clean`. `internal/pane` depends on no other cspace package.

- [ ] **Step 9: Document the race target**

In `CLAUDE.md`, inside the `## Development` fenced block, add after the `make test-scripts` line:

```
make test-race    # -race over internal/pane only; NOT part of make check
```

- [ ] **Step 10: Amend the spec's teardown paragraph and `Emulator` interface**

Two departures from the design have to be written down where the design is,
because the next plan reads the spec and not this file. Step 3 of the rollout
set the precedent (`2026-09-18-control-plane-3-dashboard.md` edits the spec's
`internal/controlplane` section) and 4b Task 7 does it again.

In `docs/superpowers/specs/2026-09-17-control-plane-design.md`, add one line
to the `Emulator` interface listing, immediately after `Render() string`:

```go
    CursorPosition() (x, y int)  // where the child left the cursor; tea.View places the terminal cursor from it
```

and replace the teardown paragraph — the one beginning "Teardown handshake:
stop accepting input, cancel the response drain" — with exactly:

```markdown
Teardown handshake: stop accepting input, end the child's process group and
reap it, close the PTY — which both returns a writer parked on a child that
stopped reading and gives the output pump its EOF — then `Close` the emulator
once via `sync.Once` and join the response drain. The drain cannot be
cancelled first: it is parked inside x/vt's unbuffered input pipe, and only
closing that pipe returns it. The property the original ordering was buying —
a `Close` that cannot race a `Read` — is bought instead by the adapter's
`Close`, which closes the emulator's input pipe (synchronized by `io.Pipe`)
rather than calling x/vt's own `Close`, which writes an unguarded `closed`
bool that `Read` reads. The scrollback is read under the adapter's own lock
for the same reason: `SafeEmulator` wraps the accessor but not the buffer it
returns. `make test-race` is the acceptance check for both.
```

- [ ] **Step 11: Tidy the module graph, run the whole gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go mod tidy
git diff go.mod
make check
git add go.mod go.sum Makefile CLAUDE.md internal/pane docs/superpowers/specs
git commit -m "$(cat <<'EOF'
Add the pane engine: a pty, an emulator and four goroutines

One goroutine reads the pty, one writes it, one carries the emulator's own
replies back, one reaps the child. Input is enqueued onto a bounded channel
without blocking, so a child that stops reading loses its own input rather
than stalling the caller — which is often the UI goroutine.

Teardown stops accepting input, hangs up the child's process group and then
kills it, and closes the pty BEFORE joining the writer — a writer parked in
a write to a child that stopped reading is exactly what this package exists
to survive, and joining it first would hang the close. make test-race covers
it; it is not part of make check.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 4: The commands each pane kind runs

Three of the four pane kinds run a process: the Claude pane and the shell pane run `container exec … tmux new-session -A …` (one argv, already built by `control.AttachArgv`), and the host-shell pane runs the operator's own login shell (Task 3's `pane.HostShell`). Only the Claude spec exists today — `control.ClaudeAttach` — so this task adds its shell sibling.

`control.SessionShell` and the argv shape have existed since rollout step 1; `argv_test.go` already asserts the exact argv for a `bash -l` shell session. Nothing here invents a new format.

**Files:**
- Modify: `internal/control/argv.go`
- Test: `internal/control/argv_test.go`

**Interfaces:**
- Consumes: `control.AttachSpec`, `control.SessionShell`, `control.AttachArgv` (all shipped in step 1).
- Produces: `func ShellAttach(container string, tmux bool) AttachSpec` — the spec for a login-shell pane, mirroring `ClaudeAttach`.

- [ ] **Step 1: Write the failing test**

Add to `internal/control/argv_test.go`:

```go
func TestShellAttachMirrorsClaudeAttach(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")

	spec := ShellAttach("cspace-demo-mercury", true)
	if spec.Session != SessionShell {
		t.Errorf("session = %q, want %q", spec.Session, SessionShell)
	}
	if got, want := spec.Command, []string{"bash", "-l"}; !reflect.DeepEqual(got, want) {
		t.Errorf("command = %v, want %v", got, want)
	}
	if spec.TERM != "xterm-256color" || spec.COLORTERM != "truecolor" {
		t.Errorf("terminal env = %q/%q, want the host's", spec.TERM, spec.COLORTERM)
	}

	// Without tmux the session is empty, which AttachArgv reads as "exec the
	// command directly" — the fallback for an image built before tmux.
	if got := ShellAttach("cspace-demo-mercury", false).Session; got != "" {
		t.Errorf("no-tmux session = %q, want empty", got)
	}

	_, argv, err := AttachArgv(spec)
	if err != nil {
		// container may not be on PATH in CI, exactly as TestAttachArgv
		// above already handles. The spec assertions are unconditional; only
		// the argv comparison needs the binary resolved.
		t.Skipf("container CLI not resolvable: %v", err)
	}
	want := []string{
		"container", "exec", "-it",
		"-e", "COLORTERM=truecolor",
		"-e", "TERM=xterm-256color",
		"cspace-demo-mercury",
		"tmux", "-f", "/usr/local/etc/cspace-tmux.conf",
		"new-session", "-A", "-s", "cspace-shell", "-c", "/workspace",
		"bash", "-l",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("argv =\n%v\nwant\n%v", argv, want)
	}
}
```

Add `"reflect"` to `argv_test.go`'s import block: at HEAD the file imports only `"strings"` and `"testing"` — the existing `TestAttachArgv` compares argv element by element rather than with `reflect.DeepEqual`.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/control/ -run TestShellAttach`
Expected: FAIL — `undefined: ShellAttach`.

- [ ] **Step 3: Implement it**

In `internal/control/argv.go`, immediately after `ClaudeAttach`:

```go
// ShellAttach returns the spec for an interactive login shell inside a
// sandbox — the control plane's shell pane, and the sibling of ClaudeAttach.
//
// `bash -l` because the sandbox image's user shell is /bin/bash and a login
// shell is what reads the profile the entrypoint seeds. The session is
// SessionShell, not SessionClaude: one tmux session per pane kind means a
// shell pane and a Claude pane on one sandbox never contend for a current
// window, and closing the shell never touches the agent's screen.
func ShellAttach(container string, tmux bool) AttachSpec {
	spec := AttachSpec{
		Container: container,
		Command:   []string{"bash", "-l"},
		TERM:      os.Getenv("TERM"),
		COLORTERM: os.Getenv("COLORTERM"),
	}
	if tmux {
		spec.Session = SessionShell
	}
	return spec
}
```

- [ ] **Step 4: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/control/ -run TestShellAttach -v`
Expected: PASS.

- [ ] **Step 5: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
make check
git add internal/control
git commit -m "$(cat <<'EOF'
Add the shell pane's attach spec

ShellAttach mirrors ClaudeAttach against the cspace-shell tmux session, so a
shell pane and a Claude pane on one sandbox never share a current window.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 5: Validate a sandbox name's shape in `up` and `down`

`cspace down` joins a caller-supplied sandbox name straight into two host paths and `os.RemoveAll`s both (`wipeSandboxState`, `cmd_down.go`). The step-3 plan deferred a shape check deliberately, on the grounds that every name the dashboard passed came off a `control.Row` built from the registry and `container ls` — never from a person typing.

That is still true after rollout step 4, and the check lands anyway. Plan 4b's new-pane picker chooses among four fixed `Kind`s and its boot action passes a registry-derived `control.Row`, so no free-text sandbox name reaches `up` or `down` there either. What has changed is that the dashboard now has a picker at all: the argument for deferring was that no UI could ever produce a name, and the honest version of it is "no UI does yet". Closing the exposure ahead of the first one that can is cheaper than remembering to.

This closes `.cspace/context/findings/2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins.md`.

**Files:**
- Modify: `internal/cli/cmd_up.go` (`validateSandboxName`)
- Modify: `internal/cli/cmd_down.go` (call it)
- Test: `internal/cli/cmd_up_test.go`, `internal/cli/cmd_down_test.go`
- Modify: `.cspace/context/findings/2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins.md`

**Interfaces:**
- Consumes: `validateSandboxName(project, name string) error` (exists, rejects only `"browser"`).
- Produces: the same function, now also rejecting anything that is not a single safe label. `cmd_down.go` calls it for every name it is about to tear down.

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/cmd_up_test.go`:

```go
func TestValidateSandboxNameShape(t *testing.T) {
	good := []string{"mercury", "issue-42", "agent_alice", "a", "A1", strings.Repeat("x", 63)}
	for _, name := range good {
		if err := validateSandboxName("demo", name); err != nil {
			t.Errorf("validateSandboxName(%q) = %v, want nil", name, err)
		}
	}

	bad := []string{
		"",                      // nothing to name
		"..",                    // the traversal this exists to stop
		"../../etc",             //
		"a/b",                   // a separator would escape the project dir
		"a\\b",                  //
		".hidden",               // a leading dot is a relative-path prefix
		"-lead",                 // a leading dash reads as a flag
		"has space",             //
		"dotted.name",           // a name is one DNS label
		"browser",               // reserved for the shared sidecar
		strings.Repeat("x", 64), // longer than a DNS label
	}
	for _, name := range bad {
		if err := validateSandboxName("demo", name); err == nil {
			t.Errorf("validateSandboxName(%q) = nil, want an error", name)
		}
	}
}
```

(add `"strings"` to that file's imports if it is not already there.)

And add to `internal/cli/cmd_down_test.go`:

```go
func TestDownRefusesAnUnsafeName(t *testing.T) {
	cmd := newDownCmd()
	cmd.SetArgs([]string{"../../etc"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("down accepted a traversal-shaped name")
	}
	if !strings.Contains(err.Error(), "sandbox name") {
		t.Errorf("error = %v, want it to name the sandbox name", err)
	}
}
```

Add `"io"` and `"strings"` to `cmd_down_test.go`'s import block. At HEAD that
file imports only `bytes`, `os`, `path/filepath`, `testing` and
`internal/control`, so neither is there.

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/cli/ -run 'TestValidateSandboxNameShape|TestDownRefusesAnUnsafeName'`
Expected: FAIL — the up test reports every `bad` case returning nil, and the down test fails because nothing rejects the name.

- [ ] **Step 3: Implement the check**

Replace `validateSandboxName` in `internal/cli/cmd_up.go`:

```go
// sandboxNamePattern is the shape a sandbox name must have: one label of
// letters, digits, underscores and dashes, starting with a letter or digit,
// at most 63 characters.
//
// It is deliberately narrower than "a string that happens to work". A sandbox
// name is joined into host paths that `cspace down` then removes outright
// (~/.cspace/clones/<project>/<name>, ~/.cspace/sessions/<project>/<name>),
// it becomes part of a container name, and it becomes a DNS label in
// <sandbox>.<project>.cspace.test. Dots are rejected for the last of those
// reasons: a dotted name would silently become a sub-subdomain nothing
// resolves.
var sandboxNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

// validateSandboxName rejects a name cspace will not take.
//
// Every name reaching `down` today comes off a registry entry or a
// `container ls` row, so the shape has never been in question. The check is
// here because `down`'s callers are no longer only cspace's own: the
// control plane grew a pane picker in rollout step 4, and the next thing
// that grows a text field is where a typed name would first reach
// wipeSandboxState's two os.RemoveAlls.
// (cs-finding:2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins)
func validateSandboxName(project, name string) error {
	if name == "browser" {
		return fmt.Errorf(
			`"browser" is reserved for the shared browser sidecar (browser.%s.cspace.test)`,
			project)
	}
	if !sandboxNamePattern.MatchString(name) {
		return fmt.Errorf(
			"invalid sandbox name %q: use letters, digits, dashes and underscores only, "+
				"starting with a letter or digit, at most 63 characters",
			name)
	}
	return nil
}
```

Add `"regexp"` to `cmd_up.go`'s imports.

- [ ] **Step 4: Call it from `down`**

In `internal/cli/cmd_down.go`, in the `RunE`, immediately after the `names` slice is built (after the `if all { … } else { names = []string{args[0]} }` block) and before `a := applecontainer.New()`:

```go
			// Every name here is about to be joined into two host paths that
			// wipeSandboxState removes outright. Registry-derived names are
			// no excuse to skip the check: the registry is a file on disk,
			// and this is the last point before the joins. `project` is the
			// cwd-derived one, used only for the "browser" message's wording
			// — the shape check itself does not depend on it, and the
			// per-name project resolution happens further down.
			//
			// Under --all a bad name is skipped rather than fatal. These
			// names come from registry entries, which were never shape-
			// checked before this change, so one legacy entry with a dot in
			// it would otherwise make `cspace down --all` refuse to tear
			// down anything at all. The single-name form still fails hard:
			// there the name came from the caller, and the callers are no
			// longer only cspace's own code.
			kept := names[:0]
			for _, name := range names {
				if err := validateSandboxName(project, name); err != nil {
					if !all {
						return err
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "[cspace] skipping %q: %v\n", name, err)
					continue
				}
				kept = append(kept, name)
			}
			names = kept
```

- [ ] **Step 5: Run the tests**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && go test ./internal/cli/ -run 'TestValidateSandboxName|TestDown' -v`
Expected: PASS. If an existing `down` test uses a name this now rejects, the test is naming something cspace could never have created — fix the test's name.

- [ ] **Step 6: Update the finding**

Append to the `## Updates` section of `.cspace/context/findings/2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins.md`, and change its frontmatter `status:` to `resolved`:

```markdown
### 2026-09-18 — status: resolved
`validateSandboxName` now enforces a single-label shape (letters, digits,
dashes, underscores; no dots, no separators, no leading dot or dash; at most
63 characters) and `cspace down` calls it for every name it is about to tear
down, `--all` included. The exposure closed is `wipeSandboxState`'s two
`os.RemoveAll`s.

To be accurate about the trigger: nothing in rollout step 4 actually types a
sandbox name into these paths. Its new-pane picker chooses among four fixed
pane kinds and its boot action passes a registry-derived row, so every name
still arrives from the registry or from `container ls`. The check lands
*ahead* of a UI that can type one rather than because of one — the step-3
deferral's premise was that no such UI would exist, and step 4 is where the
dashboard stopped being a pure reader of its own row set.
```

- [ ] **Step 7: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
make check
git add internal/cli .cspace/context/findings
git commit -m "$(cat <<'EOF'
Validate a sandbox name's shape before it is joined into a path

cspace down joins the name into two host directories and removes both. Every
name it sees still comes off a registry entry or a container list — rollout
step 4's picker chooses a pane kind, not a name — so this closes the
exposure ahead of the first UI that can type one rather than because of one.

(cs-finding:2026-09-18-sandbox-names-are-not-shape-validated-before-path-joins)

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 6: `--keep-state` keeps the registry entry

`cspace down --keep-state` promises to preserve a sandbox so the same name can be resumed later. It preserves the clone, the sessions and the volumes — and then unregisters the sandbox, which removes it from the dashboard entirely. A sandbox that is not in the registry has no row, so the dashboard's boot key (`u`, offered only on a `StateStopped` row) is unreachable through any cspace command: only an external `container stop` produces a bootable stopped row.

This closes `.cspace/context/findings/2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard.md`.

`Correlate` already does the right thing with a registry entry whose container is gone — it builds a selectable `RowSandbox` with `StateStopped` — with one exception: an entry still in the `"starting"` state renders `StateBooting` forever. So the entry is marked, not merely left alone.

**Files:**
- Modify: `internal/registry/registry.go` (`MarkStopped`)
- Modify: `internal/cli/cmd_down.go` (`teardownSandbox`, `wipeSandboxState`, `substrateDowner`, and the new `downSubstrate` seam)
- Test: `internal/registry/registry_test.go`, `internal/cli/cmd_down_test.go`, `internal/control/correlate_test.go`
- Modify: `.cspace/context/findings/2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard.md`

**Interfaces:**
- Consumes: `registry.Registry`, `control.Correlate` (both unchanged in shape).
- Produces:
  - `func (r *Registry) MarkStopped(project, name string) error` — sets an existing entry's `State` to `"stopped"`, a no-op when the entry is missing (the same contract `MarkReady` has).
  - `type downSubstrate interface { Stop; ListVolumes; RemoveVolume }` in `internal/cli` — the substrate surface `teardownSandbox` and `wipeSandboxState` use, so the tests below can inject a no-op. `*applecontainer.Adapter` satisfies it, so neither production caller changes.
  - `var stopSidecarContainer = stopBrowserSidecar` — the same seam for the browser sidecar's stop, which is a package function rather than an adapter method.

- [ ] **Step 1: Write the failing registry test**

Add to `internal/registry/registry_test.go`:

```go
func TestMarkStopped(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "registry.json")}
	if err := r.Register(Entry{Project: "demo", Name: "mercury", State: "starting"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.MarkStopped("demo", "mercury"); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	e, err := r.Lookup("demo", "mercury")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.State != "stopped" {
		t.Errorf("state = %q, want stopped", e.State)
	}
	// A missing entry is not an error: a racing `down` may have removed it.
	if err := r.MarkStopped("demo", "gone"); err != nil {
		t.Errorf("MarkStopped on a missing entry = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Write the failing correlate test**

Add to `internal/control/correlate_test.go`:

```go
func TestCorrelateShowsAKeptEntryAsStopped(t *testing.T) {
	now := time.Now()
	snap := Correlate(now, nil,
		[]registry.Entry{{Project: "demo", Name: "mercury", State: "stopped"}},
		nil, nil, nil, DaemonHealth{Reachable: true}, nil)

	var row Row
	for _, r := range snap.Rows {
		if r.Kind == RowSandbox && r.Name == "mercury" {
			row = r
		}
	}
	if row.Kind != RowSandbox {
		t.Fatalf("a kept entry produced no sandbox row: %+v", snap.Rows)
	}
	if row.State != StateStopped {
		t.Errorf("state = %v, want StateStopped — the dashboard offers boot only on those", row.State)
	}
	if !row.Selectable {
		t.Error("the row is not selectable, so u can never reach it")
	}
}
```

- [ ] **Step 3: Write the failing `down` test**

Add to `internal/cli/cmd_down_test.go`:

```go
// noSubstrate is the injected substrate: teardown is best-effort and has no
// error return, so a no-op is a faithful stand-in for "the container was
// already gone". It exists so `go test ./internal/cli/` never runs
// `container rm --force cspace-demo-mercury` against whatever a developer
// happens to have booted.
type noSubstrate struct{}

func (noSubstrate) Stop(context.Context, string) error { return nil }
func (noSubstrate) ListVolumes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (noSubstrate) RemoveVolume(context.Context, string) error { return nil }

// noSidecars silences the two stopBrowserSidecar calls for one test. Those
// run `container stop` and `container rm` by name and are not adapter
// methods, so they need their own seam.
func noSidecars(t *testing.T) {
	t.Helper()
	prev := stopSidecarContainer
	stopSidecarContainer = func(context.Context, string) {}
	t.Cleanup(func() { stopSidecarContainer = prev })
}

func TestTeardownKeepsTheRegistryEntryWithKeepState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // teardownSandbox resolves $HOME itself; see below
	noSidecars(t)
	reg := &registry.Registry{Path: filepath.Join(home, "sandbox-registry.json")}
	if err := reg.Register(registry.Entry{Project: "demo", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var out bytes.Buffer

	teardownSandbox(context.Background(), noSubstrate{}, reg, "demo", "mercury", &out, false /* wipeState */)

	e, err := reg.Lookup("demo", "mercury")
	if err != nil {
		t.Fatalf("--keep-state removed the registry entry: %v", err)
	}
	if e.State != "stopped" {
		t.Errorf("state = %q, want stopped", e.State)
	}
}

func TestTeardownUnregistersWithoutKeepState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // this one reaches wipeSandboxState's RemoveAlls
	noSidecars(t)
	reg := &registry.Registry{Path: filepath.Join(home, "sandbox-registry.json")}
	if err := reg.Register(registry.Entry{Project: "demo", Name: "mercury", State: "ready"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var out bytes.Buffer

	teardownSandbox(context.Background(), noSubstrate{}, reg, "demo", "mercury", &out, true /* wipeState */)

	if _, err := reg.Lookup("demo", "mercury"); err == nil {
		t.Error("the default teardown left the registry entry behind")
	}
}
```

`t.Setenv("HOME", home)` is not decoration and a temp dir alone is not
isolation: `teardownSandbox` resolves the home directory itself, through
`os.UserHomeDir()` in `removeControlPlaneDir` (unconditional) and in
`wipeSandboxState` (which the `wipeState: true` test reaches). Without it the
second test `os.RemoveAll`s real paths under the developer's `~/.cspace`. The
two tests already in this file set it at lines 19 and 46 for the same reason.

Add `"context"` and `github.com/elliottregan/cspace/internal/registry` to
`cmd_down_test.go`'s import block (`"io"` and `"strings"` went in for Task 5).
Do **not** import `internal/substrate/applecontainer` here: the point of
`noSubstrate` is that no real adapter is constructed.

Passing a real `applecontainer.New()` would make `go test ./internal/cli/`
shell out to `container rm --force cspace-demo-mercury` plus
`container stop`/`rm` for two browser sidecar names. Those are best-effort
and would fail harmlessly *only* because nobody happens to own a project
called `demo` with a sandbox called `mercury` — which is not a property a
unit test may rely on. The seam Step 6 adds is what removes the hazard;
what the two tests are actually about is the registry, and those assertions
are unchanged.

- [ ] **Step 4: Run all three to verify they fail**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go test ./internal/registry/ -run TestMarkStopped
go test ./internal/control/ -run TestCorrelateShowsAKeptEntry
go test ./internal/cli/ -run TestTeardown
```
Expected: the first fails to build (`undefined: MarkStopped`); the second passes already (`Correlate` needs no change — record that, it is the point of the test); the third fails to build too (`undefined: stopSidecarContainer`, and `teardownSandbox` does not accept a `noSubstrate`) — both halves of that are what Step 6 adds, and once it does, the kept-entry case is the one that fails on behaviour.

- [ ] **Step 5: Add `MarkStopped`**

In `internal/registry/registry.go`, after `MarkReady`:

```go
// MarkStopped transitions an existing entry's State to "stopped": the
// sandbox is registered and resumable, but nothing is running. `cspace down
// --keep-state` uses it in place of Unregister, so the sandbox keeps its row
// in the dashboard — a row the boot action is offered on — instead of
// vanishing. No-op if the entry is missing, for the same reason MarkReady is.
//
// The state matters as well as the entry: Correlate reads "starting" as
// booting, so a sandbox torn down mid-boot would otherwise show ◐ forever.
func (r *Registry) MarkStopped(project, name string) error {
	return r.withLock(func() error {
		m, err := r.load()
		if err != nil {
			return err
		}
		e, ok := m[key(project, name)]
		if !ok {
			return nil
		}
		e.State = "stopped"
		m[key(project, name)] = e
		return r.save(m)
	})
}
```

- [ ] **Step 6: Make the substrate injectable, and make `teardownSandbox` honour `wipeState`**

First the seam the tests need. In `internal/cli/cmd_down.go`, above
`substrateDowner`, add:

```go
// downSubstrate is the substrate surface cspace down uses: the container
// teardown, and the two volume calls behind the default (non-`--keep-state`)
// wipe. *applecontainer.Adapter satisfies it as written, so neither
// production caller — cmd_down.go's RunE and control_host.go — changes.
//
// It exists so a unit test can inject a no-op. `teardownSandbox` is
// best-effort and has no error return, which made it easy to call from a
// test with a real adapter; that test would then run
// `container rm --force cspace-<project>-<sandbox>` against the developer's
// own machine and be harmless only by luck of the names.
type downSubstrate interface {
	Stop(ctx context.Context, name string) error
	ListVolumes(ctx context.Context, prefix string) ([]string, error)
	RemoveVolume(ctx context.Context, name string) error
}

// stopSidecarContainer is stopBrowserSidecar behind a variable, for the same
// reason: it runs `container stop` and `container rm` by name and is a
// package function rather than an adapter method, so an interface cannot
// reach it. Only teardownSandbox goes through the variable; every other
// caller of stopBrowserSidecar is unchanged.
var stopSidecarContainer = stopBrowserSidecar
```

Then retype the three places that name the concrete adapter — nothing else
in their bodies moves:

- `type substrateDowner struct { adapter *applecontainer.Adapter }` → `adapter downSubstrate`
- `func teardownSandbox(ctx context.Context, a *applecontainer.Adapter, …)` → `a downSubstrate`
- `func wipeSandboxState(ctx context.Context, a *applecontainer.Adapter, …)` → `a downSubstrate`

and route `teardownSandbox`'s two sidecar stops through the variable:
`stopBrowserSidecar(ctx, browserContainerName(project, name))` →
`stopSidecarContainer(ctx, browserContainerName(project, name))`, and the
same for the `browserSingletonName(project)` call in the block replaced
below. `internal/cli` keeps its `applecontainer` import: `RunE` still calls
`applecontainer.New()`.

Then the behaviour change. Replace the unconditional unregister and the ref-count that follows it:

```go
	// Remove this instance from the registry BEFORE counting so it is not
	// included in the remaining-sandboxes tally.
	_ = r.Unregister(project, name)

	// Shared browser sidecar: ref-counted — stop it only when this was the last
	// sandbox in the project. Idempotent and a no-op when no singleton exists.
	if remaining, err := r.CountForProject(project); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: registry count during browser teardown: %v\n", err)
	} else if remaining == 0 {
		stopBrowserSidecar(ctx, browserSingletonName(project))
	}
```

with (note the sidecar stop now goes through the variable):

```go
	// The registry entry goes only when the state does. --keep-state promises
	// a sandbox that can be resumed under the same name, and a sandbox the
	// registry has forgotten is one the dashboard cannot show — let alone
	// offer its boot key on, which is only offered on stopped rows. Marking
	// it stopped rather than leaving it alone matters too: Correlate reads a
	// "starting" entry as booting, so a sandbox torn down mid-boot would
	// otherwise keep its ◐ forever.
	// (cs-finding:2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard)
	if wipeState {
		_ = r.Unregister(project, name)
	} else {
		_ = r.MarkStopped(project, name)
	}

	// Shared browser sidecar: ref-counted — stop it only when the project has
	// no sandbox left that could use it. The count is now by STATE rather
	// than by identity, because a kept entry holds no claim on the browser:
	// its container is gone. That is just as true of the siblings an earlier
	// `cspace down --all --keep-state` marked stopped, which is why
	// discounting only this one is not enough — CountForProject counts every
	// entry regardless of state (its own comment says so), so with three
	// sandboxes and --all --keep-state the tally never reaches zero and the
	// sidecar is left running for a project with nothing left to use it.
	// Idempotent and a no-op when no singleton exists.
	if entries, err := r.List(); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: registry list during browser teardown: %v\n", err)
	} else {
		live := 0
		for _, e := range entries {
			if e.Project == project && e.State != "stopped" {
				live++
			}
		}
		if live == 0 {
			stopSidecarContainer(ctx, browserSingletonName(project))
		}
	}
```

- [ ] **Step 7: Run the tests**

Run:
```bash
cd /Users/elliott/Projects/cspace-control-plane-4
go test ./internal/registry/ ./internal/control/ ./internal/cli/ -run 'TestMarkStopped|TestCorrelateShowsAKeptEntry|TestTeardown' -v
```
Expected: PASS for all four.

- [ ] **Step 8: Update the finding**

Append to `## Updates` in `.cspace/context/findings/2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard.md`, and set `status: resolved`:

```markdown
### 2026-09-18 — status: resolved
`teardownSandbox` now unregisters only when `wipeState` is true; with
`--keep-state` it calls the new `registry.MarkStopped`, so the sandbox stays
in the registry with `state: "stopped"`, `Correlate` gives it a selectable
`StateStopped` row, and the dashboard's boot key is reachable. The shared
browser's reference count is now taken over entries whose state is not
`"stopped"`, rather than over every entry: a kept entry's container is gone
and holds no claim on the sidecar, and counting by identity would have left
the browser running after `cspace down --all --keep-state`.
```

- [ ] **Step 9: Run the gate and commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
make check
git add internal/registry internal/cli internal/control .cspace/context/findings
git commit -m "$(cat <<'EOF'
Keep the registry entry when down keeps the state

--keep-state promises a resumable sandbox, then unregistered it — and a
sandbox the registry has forgotten has no dashboard row, so the boot key it
would have been offered on was unreachable by any cspace command. The entry
now stays, marked stopped, and the shared browser's reference count discounts
it.

(cs-finding:2026-09-18-keep-state-drops-the-registry-entry-so-a-stopped-sandbox-leaves-the-dashboard)

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

### Task 7: A repo-resident pty smoke harness

Rollout step 3's verification was driven by a throwaway Python harness that ran `cspace tui` under a pty, fed a terminal emulator, and asserted on the screen a person would have seen. It worked, and it was deleted. Step 4's verification needs it again, and so will step 5.

This promotes it into `scripts/tui-smoke/`: Python 3, standard library only (the throwaway vendored `pyte`; the screen model below is the ~120 lines of it this actually needs), not run by `make check`, and small.

The screen model was checked against three real captures from step 3's verification. With the scroll-region handling below it reproduces `pyte`'s final screen exactly, and without it every line below an inserted row is off by one. Two further things are load-bearing and were each verified against those captures: `feed` keeps a pending buffer, so a capture replayed in 512-byte chunks gives the same screen as the whole file (without it the footer renders as `d 38;2;74;74;74mtear down`), and the CSI pattern admits the intermediate byte `$`, so Bubble Tea's opening `CSI ? 2026 $ p` / `CSI ? 2027 $ p` are recognized and discarded rather than printed. Step 3 asserts the first of those.

**Files:**
- Create: `scripts/tui-smoke/tuilib.py`
- Create: `scripts/tui-smoke/smoke.py`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `tuilib.Tui(argv=None, cwd=..., env=None, rows=40, cols=120, bin_="./bin/cspace-go")` with `pump(seconds)`, `send(data, then=0.6)`, `type(text)`, `display()`, `save(path, label="")`, `save_raw(path)`, `quit(key=b"q", wait=3.0)`, `reap()`, `exit_code()`; and `tuilib.Screen(cols, rows)` with `feed(str)` and `display()`. Plan 4b's verification task drives the dashboard entirely through these.

- [ ] **Step 1: Write the harness**

Create `scripts/tui-smoke/tuilib.py`:

```python
"""A pty harness for driving `cspace tui` headlessly, Mac-only, stdlib only.

Not run by `make check`: it starts the real binary against the real host, so
it belongs to a person (or an agent) verifying a change, not to CI.

Two pieces. Screen is a small VT interpreter — enough of one for what Bubble
Tea paints — so `display()` is what a human would have seen rather than a
byte stream, and it is resumable, because pump feeds it one pty read at a
time and escape sequences straddle read boundaries. Tui forks a pty, runs the
binary in it, answers the terminal queries Bubble Tea makes at startup, and
keeps the stream drained.

The pump never stops while keys are being typed. The dashboard repaints a
120x40 screen on a one-second ticker — several KB a frame, far more than a
pty buffer holds — so a script that stops reading while it writes wedges the
child in write() and never gets its keystrokes read.
"""

import fcntl
import os
import pty
import re
import select
import signal
import struct
import termios
import time

# The parameter class carries the intermediates as well as the digits: Bubble
# Tea v2 opens with CSI ? 2026 $ p and CSI ? 2027 $ p, and a class without `$`
# does not match them — feed then falls through and paints "?2026$p" onto the
# grid. The final-byte class is widened for the same reason. A private-prefix
# sequence still draws nothing; _csi returns early on it.
CSI = re.compile(r"""\x1b\[([0-9;:?<>=!$"' ]*)([A-Za-z@`{|}~])""")

# PARTIAL_CSI is the same head with no final byte and nothing after it: an
# escape sequence cut in half by a read boundary. feed stashes those instead
# of printing them.
PARTIAL_CSI = re.compile(r"""\x1b\[[0-9;:?<>=!$"' ]*$""")


class Screen:
    """A cell grid fed with terminal output.

    Handles what Bubble Tea actually emits: cursor addressing, the erase
    family, a scroll region plus reverse index (which is how it inserts a
    line), tabs, and printable text. Styling is discarded — this answers
    "what does it say", and the raw stream is kept separately for the
    questions styling answers.

    feed is resumable. Tui.pump hands it one pty read at a time, and an
    escape sequence straddling two reads is the normal case, not an edge
    one: without the pending buffer below, a 512-byte split through the
    footer's SGR run paints "38;2;74;74;74m" onto the screen. pyte, which
    this replaces, is a resumable state machine for the same reason.
    """

    def __init__(self, cols, rows):
        self.cols, self.rows = cols, rows
        self.clear()

    def clear(self):
        self.grid = [self._blank() for _ in range(self.rows)]
        self.x = self.y = 0
        self.top, self.bot = 0, self.rows - 1
        self._pending = ""

    def _blank(self):
        return [" "] * self.cols

    def display(self):
        return "\n".join("".join(row).rstrip() for row in self.grid)

    def _index(self):
        """Line feed, honouring the scroll region."""
        if self.y == self.bot:
            del self.grid[self.top]
            self.grid.insert(self.bot, self._blank())
        elif self.y < self.rows - 1:
            self.y += 1

    def _reverse_index(self):
        """ESC M — the inverse, which is how Bubble Tea inserts a line."""
        if self.y == self.top:
            del self.grid[self.bot]
            self.grid.insert(self.top, self._blank())
        elif self.y > 0:
            self.y -= 1

    def feed(self, data):
        # Anything the previous call could not finish parsing leads this one.
        if self._pending:
            data, self._pending = self._pending + data, ""
        i, n = 0, len(data)
        while i < n:
            ch = data[i]
            if ch == "\x1b":
                if i + 1 >= n:  # a bare ESC at the tail: the rest is coming
                    self._pending = data[i:]
                    return
                if data.startswith("\x1b]", i):  # OSC: skip to BEL or ST
                    bel, st = data.find("\x07", i), data.find("\x1b\\", i)
                    if st != -1 and (bel == -1 or st < bel):
                        i = st + 2
                    elif bel != -1:
                        i = bel + 1
                    else:
                        self._pending = data[i:]  # unterminated OSC
                        return
                    continue
                m = CSI.match(data, i)
                if m:
                    self._csi(m.group(1), m.group(2))
                    i = m.end()
                    continue
                if PARTIAL_CSI.match(data, i):  # CSI cut off before its final
                    self._pending = data[i:]
                    return
                if data.startswith("\x1bM", i):
                    self._reverse_index()
                    i += 2
                    continue
                i += 2
                continue
            if ch == "\n":
                self._index()
            elif ch == "\r":
                self.x = 0
            elif ch == "\b":
                self.x = max(0, self.x - 1)
            elif ch == "\t":
                self.x = min(self.cols - 1, (self.x // 8 + 1) * 8)
            elif ch >= " ":
                if self.x < self.cols and self.y < self.rows:
                    self.grid[self.y][self.x] = ch
                self.x += 1
            i += 1

    def _csi(self, raw, final):
        if raw.startswith(("?", ">", "<", "!", "$", " ")):
            return  # private modes and device attributes draw nothing
        try:
            p = [int(x) if x else 1 for x in raw.split(";")] if raw else [1]
        except ValueError:
            # A colon sub-parameter is not an int: SGR truecolour written as
            # 38:2::74:74:74, or an underline style 4:3. lipgloss v2 emits
            # the semicolon form, so `cspace tui` never produces one — but a
            # pane running another program can, and an unhandled ValueError
            # here kills the harness instead of ignoring a sequence this
            # interpreter draws nothing for anyway.
            return
        if final in "Hf":
            row = p[0] if len(p) > 0 else 1
            col = p[1] if len(p) > 1 else 1
            self.y = min(self.rows - 1, max(0, row - 1))
            self.x = min(self.cols - 1, max(0, col - 1))
        elif final == "A":
            self.y = max(0, self.y - p[0])
        elif final == "B":
            self.y = min(self.rows - 1, self.y + p[0])
        elif final == "C":
            self.x = min(self.cols - 1, self.x + p[0])
        elif final == "D":
            self.x = max(0, self.x - p[0])
        elif final == "d":
            self.y = min(self.rows - 1, max(0, p[0] - 1))
        elif final in "G`":
            self.x = min(self.cols - 1, max(0, p[0] - 1))
        elif final == "r":
            top = p[0] if len(p) > 0 else 1
            bot = p[1] if len(p) > 1 else self.rows
            self.top = max(0, top - 1)
            self.bot = min(self.rows - 1, bot - 1)
            self.x, self.y = 0, self.top
        elif final == "J":
            mode = p[0] if raw else 0
            if mode in (2, 3):
                self.grid = [self._blank() for _ in range(self.rows)]
            elif mode == 0:
                for x in range(self.x, self.cols):
                    self.grid[self.y][x] = " "
                for y in range(self.y + 1, self.rows):
                    self.grid[y] = self._blank()
            else:
                for y in range(0, self.y):
                    self.grid[y] = self._blank()
                for x in range(0, self.x + 1):
                    self.grid[self.y][x] = " "
        elif final == "K":
            mode = p[0] if raw else 0
            span = range(self.x, self.cols) if mode == 0 else \
                range(0, self.x + 1) if mode == 1 else range(0, self.cols)
            for x in span:
                self.grid[self.y][x] = " "
        elif final == "X":
            for x in range(self.x, min(self.cols, self.x + p[0])):
                self.grid[self.y][x] = " "


class Tui:
    """One run of the binary under a pty."""

    def __init__(self, argv=None, cwd=".", env=None, rows=40, cols=120,
                 bin_="./bin/cspace-go"):
        self.raw = b""
        self.rows, self.cols = rows, cols
        self.screen = Screen(cols, rows)
        self.exited = False
        self.status = None
        argv = argv if argv is not None else ["tui"]
        path = bin_ if bin_.startswith("/") else os.path.join(cwd, bin_)

        pid, fd = pty.fork()
        if pid == 0:
            # Everything in this branch runs in the forked child, and it
            # must never raise: an exception would unwind back into the
            # caller's own code as a SECOND copy of the test script, both
            # halves printing. A missing binary, an unreadable cwd and a
            # failed execv all land here, so the child exits 127 — which
            # reap() then reports — rather than escaping.
            try:
                os.chdir(cwd)
                os.environ["TERM"] = "xterm-256color"
                os.environ["COLORTERM"] = "truecolor"
                for k, v in (env or {}).items():
                    os.environ[k] = v
                os.execv(path, [os.path.basename(path)] + argv)
            except BaseException:
                pass
            os._exit(127)  # execv only returns by failing
        self.pid, self.fd = pid, fd
        # The model renders nothing until it learns the window size.
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def pump(self, seconds):
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.2)
            if not r:
                continue
            try:
                chunk = os.read(self.fd, 65536)
            except OSError:
                return
            if not chunk:
                return
            self.raw += chunk
            self.screen.feed(chunk.decode("utf-8", "replace"))
            self._answer_queries(chunk)

    def _answer_queries(self, chunk):
        """Play terminal.

        Bubble Tea v2 probes at startup — cursor position, background colour,
        synchronized output, kitty keyboard — and holds its first render until
        the answers arrive or a ~5s timeout expires. A real terminal answers in
        microseconds; without this the dashboard looks like it hangs for five
        seconds and every timing assertion is wrong.
        """
        reply = b""
        if b"\x1b]11;?" in chunk:
            reply += b"\x1b]11;rgb:0000/0000/0000\x1b\\"
        if b"\x1b[6n" in chunk:
            reply += b"\x1b[1;1R"
        if b"\x1b[?2026$p" in chunk:
            reply += b"\x1b[?2026;2$y"
        if b"\x1b[?2027$p" in chunk:
            reply += b"\x1b[?2027;0$y"
        if b"\x1b[?u" in chunk:
            reply += b"\x1b[?0u"
        if b"\x1b[c" in chunk:
            reply += b"\x1b[?62;22c"
        if reply:
            try:
                os.write(self.fd, reply)
            except OSError:
                pass

    def send(self, data, then=0.6):
        if isinstance(data, str):
            data = data.encode()
        os.write(self.fd, data)
        self.pump(then)

    def type(self, text, per=0.05):
        for ch in text:
            self.send(ch.encode(), then=per)

    def resize(self, rows, cols):
        self.rows, self.cols = rows, cols
        self.screen = Screen(cols, rows)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def display(self):
        return self.screen.display()

    def save(self, path, label=""):
        with open(path, "w") as f:
            if label:
                f.write("=== %s ===\n" % label)
            f.write(self.display())
            f.write("\n")
        return self.display()

    def save_raw(self, path):
        with open(path, "wb") as f:
            f.write(self.raw)

    def quit(self, key=b"q", wait=3.0):
        try:
            os.write(self.fd, key)
        except OSError:
            pass
        self.pump(wait)
        return self.reap()

    def reap(self, tries=60):
        if self.exited:
            return self.status
        for _ in range(tries):
            done, st = os.waitpid(self.pid, os.WNOHANG)
            if done:
                self.exited, self.status = True, st
                return st
            self.pump(0.1)
        try:
            os.kill(self.pid, signal.SIGKILL)
            os.waitpid(self.pid, 0)
        except OSError:
            pass
        self.status = None
        return None

    def exit_code(self):
        if self.status is None:
            return "HUNG (killed)"
        return os.waitstatus_to_exitcode(self.status)
```

- [ ] **Step 2: Write the one-shot smoke script**

Create `scripts/tui-smoke/smoke.py`:

```python
#!/usr/bin/env python3
"""Boot `cspace tui` under a pty, look at it, quit.

    make build && python3 scripts/tui-smoke/smoke.py [needle ...]

Prints the screen, whether each needle appeared, and the exit code. A HUNG
exit means the quit key never reached tea.Quit.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from tuilib import Tui  # noqa: E402


def main():
    needles = sys.argv[1:] or ["sandboxes", "daemon"]
    repo = os.getcwd()
    t = Tui(cwd=repo)
    t.pump(12)
    print(t.display())
    print()
    t.send(b"?", then=1.0)
    help_screen = t.display()
    t.send(b"?", then=0.5)
    t.quit()

    text = t.raw.decode("utf-8", "replace")
    for needle in needles:
        print("%-24s %s" % (needle, needle in text))
    print("%-24s %s" % ("help overlay", "keys" in help_screen))
    print("%-24s %s" % ("exit", t.exit_code()))


if __name__ == "__main__":
    main()
```

- [ ] **Step 3: Prove the screen model is resumable** — *manual, skip if no host is available*

This step starts the real binary against the real host, the way Task 8 of
plan 4b does: it needs a Mac with Apple Container and a built
`bin/cspace-go`. A headless executor should skip it and say so rather than
fail the task; Step 5's `make check` is the gate that has to be green
everywhere.

The harness feeds `Screen` one pty read at a time, so the property that
matters is that a split escape sequence survives the boundary. Replay a real
capture both ways and compare:

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
make build
python3 - <<'PY'
import os, sys
sys.path.insert(0, "scripts/tui-smoke")
from tuilib import Tui, Screen

t = Tui(cwd=os.getcwd())
t.pump(12)
t.quit()
text = t.raw.decode("utf-8", "replace")

def replay(chunk):
    s = Screen(120, 40)
    if chunk is None:
        s.feed(text)
    else:
        for i in range(0, len(text), chunk):
            s.feed(text[i:i + chunk])
    return s.display()

whole = replay(None)
for chunk in (64, 512, 1024):
    assert replay(chunk) == whole, "chunked replay diverged at %d bytes" % chunk
print("resumable: OK (%d bytes)" % len(text))
PY
```
Expected: `resumable: OK` and a non-trivial byte count. A divergence means
`feed` mis-parsed a sequence cut in half by a read boundary — exactly what
the pending buffer exists to stop, and the difference between a harness that
agrees with `pyte` and one that only agrees when handed the whole capture at
once.

- [ ] **Step 4: Run it** — *manual, skip if no host is available*

Same condition as Step 3: the real binary, under a pty, against the real
host.

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
make build
python3 scripts/tui-smoke/smoke.py
```
Expected: the dashboard's screen prints as text (a sidebar on the left, a detail band on the right, a footer across the bottom), every needle reports `True`, the help overlay reports `True`, and `exit 0`. `exit HUNG (killed)` means `q` never reached `tea.Quit`.

- [ ] **Step 5: Confirm `make check` does not pick it up**

Run: `cd /Users/elliott/Projects/cspace-control-plane-4 && make check`
Expected: green, and the `test-scripts` output does not mention `tui-smoke` — it globs `scripts/*.test.sh`, which a `.py` file cannot match. `make lint`'s shellcheck globs `scripts/*.sh` and likewise ignores it.

- [ ] **Step 6: Document it**

In `CLAUDE.md`, inside the `## Development` fenced block, after the `make test-race` line added in Task 3 (Step 9 there):

```
python3 scripts/tui-smoke/smoke.py   # drive `cspace tui` under a pty (Mac, by hand)
```

and add this paragraph after the "Always build via `make`" one:

```markdown
**`scripts/tui-smoke/` drives the dashboard under a pty.** `tuilib.py` is a
stdlib-only Python harness — a small VT interpreter plus a pty fork that
answers the terminal queries Bubble Tea makes at startup — so a change to
`cspace tui` can be checked against the screen a person would have seen, with
captures. `smoke.py` is the one-shot version. Neither is run by `make check`:
they start the real binary against the real host.
```

- [ ] **Step 7: Commit**

```bash
cd /Users/elliott/Projects/cspace-control-plane-4
git add scripts/tui-smoke CLAUDE.md
git commit -m "$(cat <<'EOF'
Add a pty smoke harness for the dashboard

The step-3 verification's throwaway harness, made repo-resident and
stdlib-only: a small VT interpreter instead of a vendored pyte, and the
startup-query answers without which Bubble Tea's first render stalls five
seconds. The interpreter is resumable, because the pump feeds it one pty read
at a time and escape sequences straddle the boundaries. Not run by make check
— it starts the real binary against the real host.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01W64zstC3PZojywARTtnSW7
EOF
)"
```

---

## Self-review notes

Checked against the spec's `internal/pane` section, its Testing section, and rollout step 4's engine half, plus the two step-3 follow-ups the 4-panes brief requires before the UI half lands.

- **The `Emulator` interface** is the spec's, with one addition: `CursorPosition() (x, y int)`. `tea.View.Cursor` has to be placed from somewhere and the emulator is the only thing that knows where the child left it; the spike read it off the concrete type, which is not available behind an interface. It is declared in the interface rather than type-asserted so a replacement implementation is told it must supply it. Everything else — `Write`, `Read`, `Resize`, `Render`, `SendKey`, `Paste`, `Scrollback`, `Close` — is exactly as the design declares it, and `Scrollback` is the small two-method interface the design's `Scrollback` type implies. Task 3 Step 10 adds the `CursorPosition` line to the spec's own listing, so the next reader of the design sees the interface this package actually offers.
- **`emulator_test.go` is a suite, not one implementation's tests.** The spec asks for "an interface test suite any `Emulator` implementation must pass", so the six implementation-agnostic cases live inside `runEmulatorSuite(t, newEmulator)` and `TestVTEmulatorConformance` is four lines that call it. The kitty-flag test and `TestVTInputPipeIsAnIOCloser` sit outside it on purpose: they assert things about the x/vt adapter — its own bookkeeping, and the upstream shape `Close` is built on — not about the interface. `open(cmd, cols, rows, emu Emulator)` in Task 3 is the matching seam at the engine level.
- **The scrollback is read under the adapter's own lock**, which is the second upstream gap this package works around and the second reason `make test-race` is an acceptance criterion rather than a nicety. `vt.SafeEmulator` wraps the `Scrollback()` accessor but not the `*vt.Scrollback` it returns: that object's `Len` and `Line` read `s.lines` with no synchronization, while `Emulator.Write` pushes onto the same slice from the pane's output pump. `vtScrollback` therefore holds the adapter rather than the buffer and takes `emuMu` per call, and `Write`/`Resize` take it for write. `Render` and `CursorPosition` are left alone — `SafeEmulator` already wraps both — and `Read` must stay unlocked, since it blocks inside the pipe.
- **The teardown handshake deviates from the spec's order, deliberately, twice, and both deviations are the interesting part.** The spec says to cancel the response drain and wait for it *before* closing the emulator, so that no `Read` is in flight when `Close` runs. Read against the pinned module, that is unreachable: the drain is blocked inside x/vt's unbuffered `io.Pipe` and only closing it returns. So Task 3 orders teardown input → child → pty → emulator → drain, and Task 1 buys the actual property a different way — `vtEmulator.Close` closes the input pipe (which `io.Pipe` synchronizes) instead of calling x/vt's `Close` (which writes the unguarded `closed` bool that `Read` reads). The second deviation is that **the pty closes before the writer is joined**, not after: `writer()` only sees `stopWriter` between two writes, so a writer parked inside `ptmx.Write` on a child that stopped reading — the case this package exists to survive — is returned by nothing but the close, and joining first would hang `Close` past its own context. `TestPaneDropsInputForAChildThatNeverReads` now asserts that `Close` returns inside a deadline, which is the property its comment always claimed. `make test-race` is the acceptance criterion the spec sets, and it is a task step; Task 3 Step 10 rewrites the spec's teardown paragraph to match. The spec's fallback — a `replace` patch to x/vt — is not needed.
- **Four goroutines, and the bounded writer.** Task 3's `Pane` has exactly the design's four, with the drop-don't-block enqueue and a test (`TestPaneDropsInputForAChildThatNeverReads`) that floods a non-reading child with 8 MiB and asserts the pane stays renderable. Two things the spike got wrong are fixed: the writer now stops on a teardown signal as well as a write error (the spike leaked one parked goroutine per pane), and teardown signals the process **group**, since `pty.StartWithSize` uses `Setsid` and a bare `Process.Kill` leaves the child's own children behind.
- **The waiter does not close the emulator**, which is the other spike behaviour changed on purpose. An exited pane has to keep its last screen — the design's "exited pane: the last screen dimmed, with the exit reason and a restart key" — so `Close` is the only closer and the exit is reported through `Exited()` instead. The cost is one parked drain goroutine per exited-but-not-closed pane, which 4b's close path reaps.
- **The overlay is wired in by the task that creates it**, not by the one before. Task 1's `vt.go` hands every key straight to x/vt and Task 2 replaces that one method, so `internal/pane` compiles, tests and passes `make check` at the end of both tasks — the alternative, Task 1 calling an `encodeKey` that Task 2 creates, leaves an un-buildable package behind. The seam is asserted from both sides: Task 1's conformance suite deliberately says nothing about `SendKey`, and Task 2's `TestVTEmulatorSendsAModifiedKeyThroughTheOverlay` is the case that fails until the replacement lands.
- **The key overlay** is the spike's `keys.go` rewritten as data with a test per row, plus three rules it lacked: a modified legacy key keeps its ESC prefix when Alt is held (the spike re-dispatched with the modifier stripped and lost it), a printable key held with Shift alone sends its own text (x/vt drops it for `Mod != 0`), and with kitty on any modified printable key takes the CSI-u form rather than only the four legacy ones. `Ctrl+C` with kitty **off** is left to x/vt, which encodes it as the C0 byte; with kitty **on** rule 3 claims it like any other modified printable key and it takes the CSI-u form `ESC[99;5u` the child itself asked for. Both are table rows, because a Claude pane always has kitty on and that is the interrupt the live verification presses.
- **Kitty tracking** is the four CSI `u` registrations the design calls for, verified unopposed (no built-in x/vt handler claims final byte `u`). The query is answered in the report form `CSI ? flags u`; the spike answered with `ansi.KittyKeyboard`, which builds the *set* form, and a child parsing that reply would not have recognized its own flags.
- **Testing** matches the spec's list: CPR answered from the pane (both in-process in Task 1 and end-to-end through a real pty in Task 3), bracketed paste preserved only when the child asked, the key sequences with kitty off and on (36 rows), resize arithmetic reaching `stty size` inside the child, an 8 MiB flood into a non-reading child leaving the pane responsive **and closable inside a deadline**, and teardown under `-race`. The scrollback walk asks for `ScrollbackView(p.ScrollbackLen(), 4)` rather than a literal offset: eight lines into a four-row screen scroll five off, not four, because the first three only move the cursor down. Golden screens go through `ansi.Strip`, as the repo's other view tests do.
- **What is deliberately not here.** No `Kind` enum, no tab bookkeeping, no `PaneHost` seam, no supervisor view: those are 4b, and putting them here would mean `internal/pane` knowing about cspace. The `AttachSpec → pane.Command` conversion is 4b's too, in `internal/cli`, because `internal/control` must not import `internal/pane` and `internal/pane` must not import `internal/control`.
- **The two follow-ups** are each their own task with their own finding update, as the brief requires. Task 5's name check is a single-label pattern, narrower than strictly needed for the path joins, because the same string becomes a DNS label; the one behaviour it takes away is a dotted sandbox name, whose `<sandbox>.<project>.cspace.test` never resolved anyway. Task 6 makes the browser's reference count state-aware rather than entry-count-aware, which the finding does not mention: `CountForProject` counts every entry regardless of state, so discounting only the entry being torn down would still leave the sidecar running after `cspace down --all --keep-state`, whose earlier siblings are all kept as `"stopped"`. Task 6 also retypes `teardownSandbox`/`wipeSandboxState` onto a three-method `downSubstrate` and puts the browser-sidecar stop behind a variable — not refactoring for its own sake, but because those two tests are the first callers of `teardownSandbox` from a test, and a `go test ./internal/cli/` that runs `container rm --force` against a live machine is a hazard the registry assertions do not need.
- **The harness** is deliberately small and deliberately not in `make check`. Its screen model was validated against three of step 3's real captures and reproduces `pyte`'s final screen exactly. Three things in it are load-bearing rather than decorative: the scroll-region handling, because Bubble Tea inserts a line with `CSI 6;39r` / `ESC M` / `CSI 1;40r` and without it every row below the insertion is off by one; the pending buffer in `feed`, because `pump` hands it one pty read at a time and a sequence split across two reads is otherwise painted onto the grid (Step 3 replays a capture in 64/512/1024-byte chunks and asserts the screens match); and the `$` intermediate in the CSI pattern, because Bubble Tea opens with `CSI ? 2026 $ p` and `CSI ? 2027 $ p`, which a narrower class does not match.
