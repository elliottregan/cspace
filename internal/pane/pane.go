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
