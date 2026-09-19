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
