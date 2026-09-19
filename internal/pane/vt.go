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

// kittyStackLimit is the kitty keyboard protocol's own cap on its flag
// stack: a push past this discards the oldest entry rather than growing
// forever.
const kittyStackLimit = 16

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
	// x/vt clamps the cursor to width-1/height-1, so a zero dimension leaves
	// CursorPosition at -1 — clamp to 1 to keep the interface's "zero-based,
	// screen-relative" promise true at any size.
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	e.term.Resize(cols, rows)
}

// CursorPosition reads both coordinates from one call. Two calls would take
// two separate read locks and could be torn across a concurrent Write, which
// is how the spike's cursor occasionally landed on a row it was never on.
func (e *vtEmulator) CursorPosition() (int, int) {
	pos := e.term.CursorPosition()
	return pos.X, pos.Y
}

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
		if len(e.kittyStack) >= kittyStackLimit {
			// The kitty spec caps the stack at 16 entries and discards the
			// oldest on overflow; nothing in x/vt enforces that.
			copy(e.kittyStack, e.kittyStack[1:])
			e.kittyStack[len(e.kittyStack)-1] = flags
		} else {
			e.kittyStack = append(e.kittyStack, flags)
		}
		e.kittyFlags, e.kittyOn = flags, true
		e.mu.Unlock()
		return true
	})
	e.term.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(params ansi.Params) bool {
		flags, _, _ := params.Param(0, 0)
		mode, _, _ := params.Param(1, 1) // mode defaults to 1, set-all, when absent
		e.mu.Lock()
		switch mode {
		case 2: // set-bits: OR the given flags into the current set
			e.kittyFlags |= flags
		case 3: // reset-bits: clear the given flags from the current set
			e.kittyFlags &^= flags
		default: // 1: set-all, replace the current set outright
			e.kittyFlags = flags
		}
		e.kittyOn = e.kittyFlags != 0
		if len(e.kittyStack) > 0 {
			// Keep the stack's top in sync so a later pop restores this
			// value rather than whatever was pushed before the set.
			e.kittyStack[len(e.kittyStack)-1] = e.kittyFlags
		}
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
	s.e.emuMu.RLock()
	defer s.e.emuMu.RUnlock()
	return s.e.term.ScrollbackLen()
}

func (s vtScrollback) Line(i int) string {
	if i < 0 {
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
