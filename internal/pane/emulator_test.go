package pane

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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

// stripANSI removes styling so a test can compare screens as text. x/vt's
// Render always emits SGR and hyperlink escapes — there is no plain mode —
// and ansi.Strip is what the rest of this repo's view tests already use.
func stripANSI(s string) string { return ansi.Strip(s) }
