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
	"golang.org/x/sys/unix"
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
// entry shadows an inherited one. An empty Dir inherits the caller's. Path
// need not be absolute: a bare name with no path separator (e.g. "sh") is
// resolved against $PATH with exec.LookPath, the same as exec.Command does —
// the *exec.Cmd this package builds internally is constructed by hand and
// gets no such resolution for free.
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
	cmd  endable
	ptmx *os.File

	// kill sends a signal to a pid — negative for a process group, exactly
	// as syscall.Kill takes it. Production panes get syscall.Kill itself
	// (set in open); a test substitutes a recording stub to prove endChild
	// and killGroup send nothing to an already-reaped child, which cannot
	// be observed by inspecting a real process group after the fact.
	kill func(pid int, sig syscall.Signal) error

	// signalled is set by endChild, only from shutdown's single goroutine,
	// when it actually sent a signal to the child's process group during
	// THIS Close — as opposed to finding the child already reaped and
	// taking its early return. killGroup reads it (same goroutine, so no
	// lock is needed) to decide whether it has any business signalling the
	// group at all; see killGroup's doc.
	signalled bool

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
	// closing is set by shutdown before it ever signals the child, so the
	// waiter can tell a death Close itself caused (endChild's SIGHUP or
	// SIGKILL, or killGroup's follow-up) apart from a foreign signal death.
	// Guarded by mu because shutdown's goroutine and the waiter's are
	// different goroutines.
	closing bool

	closeOnce sync.Once
	closeErr  error
}

// endable is what the teardown handshake needs from the running child:
// enough to learn its pid and reap it. *exec.Cmd satisfies it through
// cmdHandle below; a test substitutes one whose Wait never returns to
// exercise Close honouring its context against a child that has gone into
// an uninterruptible wait — a documented Apple Container failure mode that
// cannot be reproduced with a real process, because SIGKILL cannot be
// trapped.
type endable interface {
	Pid() int // 0 if the process never started
	Wait() error
}

// cmdHandle adapts *exec.Cmd to endable.
type cmdHandle struct{ *exec.Cmd }

func (c cmdHandle) Pid() int {
	if c.Process == nil {
		return 0
	}
	return c.Process.Pid
}

// Open starts cmd on a new pseudo-terminal of the given size and begins
// interpreting its output.
func Open(cmd Command, cols, rows int) (*Pane, error) {
	if err := validateOpen(cmd, cols, rows); err != nil {
		return nil, err
	}
	// Validate before building the emulator: x/vt's constructor allocates a
	// fixed 4 MiB parser buffer eagerly, and a bad Command or size should
	// fail before paying for it.
	return open(cmd, cols, rows, newVTEmulator(cols, rows))
}

// validateOpen is Open's precondition check. open re-runs it for a caller
// that builds its own emulator and skips Open.
func validateOpen(cmd Command, cols, rows int) error {
	if cmd.Path == "" || len(cmd.Args) == 0 {
		return errors.New("pane: empty command")
	}
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane: bad size %dx%d", cols, rows)
	}
	return nil
}

// open is Open with the emulator injected, so a replacement implementation
// can be driven through the whole engine by a test.
func open(cmd Command, cols, rows int, emu Emulator) (*Pane, error) {
	if err := validateOpen(cmd, cols, rows); err != nil {
		_ = emu.Close()
		return nil, err
	}

	path := cmd.Path
	if !strings.ContainsRune(path, os.PathSeparator) {
		// Built by hand below rather than through exec.Command, so it gets
		// none of the implicit $PATH resolution a bare name like "sh" would
		// otherwise get.
		resolved, err := exec.LookPath(path)
		if err != nil {
			_ = emu.Close()
			return nil, fmt.Errorf("pane: look up %s: %w", path, err)
		}
		path = resolved
	}

	child := &exec.Cmd{Path: path, Args: cmd.Args, Dir: cmd.Dir}
	child.Env = append(os.Environ(), cmd.Env...)

	ptmx, err := pty.StartWithSize(child, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		_ = emu.Close()
		return nil, fmt.Errorf("pane: start %s: %w", path, err)
	}

	p := &Pane{
		emu:        emu,
		cmd:        cmdHandle{child},
		kill:       syscall.Kill,
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
// screen it is about to repaint is already the new shape. It signals Dirty
// itself: the reflow changes what Render returns even before the child
// paints anything new.
//
// The error is the ioctl's — including one for a pane that has already been
// Closed, which setWinsize's own doc explains. A pane whose pty has gone
// returns one here; the caller fans a resize out over every pane and must
// not let one failure stop the rest.
func (p *Pane) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pane: bad size %dx%d", cols, rows)
	}
	p.emu.Resize(cols, rows)
	p.markDirty()
	return setWinsize(p.ptmx, cols, rows)
}

// setWinsize performs the resize ioctl through the pty's own SyscallConn
// rather than creack/pty's Setsize. Setsize reaches the raw descriptor
// through (*os.File).Fd(), which — unlike Read and Write, and unlike
// SyscallConn.Control — bypasses the reference-counted synchronization
// os.File normally uses to protect a syscall from a concurrent Close.
// -race caught the result directly: Setsize's ioctl racing
// poll.FD.destroy(), which does not necessarily run inside Close itself —
// it runs on whichever goroutine's blocked Read or Write happens to release
// the file's last reference, which, per Close's own doc, can be seconds
// after Close returned, on the output pump's goroutine. A lock around
// Close's own call to ptmx.Close() (this package's first attempt) cannot
// close that gap, because it is not holding the lock either.
// SyscallConn.Control uses the exact same incref/decref pair Read and Write
// do, so it is properly serialized against Close regardless of which
// goroutine ends up running the real close, and returns an error
// deterministically once the file is closed rather than racing. creack/pty
// ships this shape too, as the unexported and unused ioctlNonblock.
func setWinsize(f *os.File, cols, rows int) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	ws := unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &ws)
	}); err != nil {
		return err
	}
	return ioctlErr
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
// while the child is still running. err is non-nil when the wait itself
// failed or the child died by a signal Close did not send itself (code is
// -1 for the latter, naming the signal), but never for an ordinary
// non-zero exit — that is what code is for. Code -1 with a nil error means
// the pane was closed by Close: endChild's own SIGHUP or SIGKILL, or
// killGroup's follow-up, is not a failure worth reporting as one, and
// without this distinction every pane the operator closed would render as
// "killed by hangup".
func (p *Pane) Exited() (code int, err error, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitCode, p.exitErr, p.exited
}

// Dropped is how many bytes of input were thrown away because the child was
// not reading. Non-zero means a key or a paste was lost.
func (p *Pane) Dropped() uint64 { return p.dropped.Load() }

// Close runs the teardown handshake and joins every goroutine, honouring
// ctx: a join that does not complete before ctx is done makes Close return
// an error naming the goroutine that would not join. The rest of the
// teardown that goroutine was blocking is skipped, but Close still releases
// what it safely can before returning — the pty and the emulator's own pipe
// — so a wedged pane does not go on holding the master descriptor and x/vt's
// 4 MiB parser buffer for the rest of the process's life; only the
// goroutines themselves may stay parked, until the child eventually dies on
// its own. Close is idempotent: the first call's result, success or error,
// is what every later call returns too, via sync.Once — a Close that gave
// up is not retried by calling it again.
//
// The order is: stop accepting input, end the child's process group and
// reap it, kill whatever is left of that group, close the pty, then close
// the emulator and join the drain.
//
// What actually returns a writer parked in ptmx.Write, or the output pump
// parked in ptmx.Read, is the CHILD DYING — reaped just above — not the pty
// close that follows it. creack/pty's master descriptor is a plain blocking
// fd outside Go's runtime poller, so ptmx.Close() only marks it closed: a
// probe against this host found a goroutine parked in Write and one parked
// in Read still blocked 4s after Close returned, and both released the
// instant the child was killed. ptmx.Close() here is cleanup of the master
// descriptor, not the unblocking mechanism — but it still has to run, and
// be joined, before the emulator's Close, so that no pump call into
// emu.Write is still in flight when that runs. On Linux, which has no
// tty-revoke equivalent to macOS's, a setsid grandchild that ignored SIGHUP
// and still holds the pty's slave side open could leave the pump parked
// even after the direct child is reaped — which is what killGroup's second,
// unconditional SIGKILL to the group is for.
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
	// Stop accepting new input. closing is set before endChild ever signals
	// the child, so the waiter can tell a death Close itself caused apart
	// from a foreign one — see waiter's doc.
	close(p.stopWriter)
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()

	p.endChild(ctx)
	if err := p.join(ctx, p.waitDone, "waiter"); err != nil {
		// The child was never confirmed reaped, so killGroup has no safe
		// pid to signal (see its doc) — but the pty and the emulator can
		// still be released best-effort; nothing about a stuck waiter makes
		// that unsafe, and it is what keeps a wedged pane from holding both
		// for the rest of the process's life.
		p.closeDescriptors()
		return err
	}

	// The direct child is reaped, but a group member that ignored the
	// SIGHUP endChild sent it — bash gives a backgrounded job SIG_IGN for
	// SIGHUP, for instance — is still alive and could still be holding the
	// pty's slave side open. SIGKILL cannot be ignored, so this closes that
	// gap.
	p.killGroup()

	_ = p.ptmx.Close()
	if err := p.join(ctx, p.writerDone, "writer"); err != nil {
		_ = p.emu.Close()
		return err
	}
	if err := p.join(ctx, p.pumpDone, "pump"); err != nil {
		_ = p.emu.Close()
		return err
	}

	emuErr := p.emu.Close()
	if err := p.join(ctx, p.drainDone, "drain"); err != nil {
		return err
	}
	return emuErr
}

// closeDescriptors releases the pty and the emulator's own pipe best-effort.
// It exists for a Close that is giving up on a join and returning early: the
// goroutines it was waiting on may stay parked until the child dies, but
// nothing should keep holding the master descriptor or x/vt's 4 MiB parser
// buffer on that account. Errors are discarded deliberately — there is
// nothing left to do differently with them once Close has already decided
// to give up.
func (p *Pane) closeDescriptors() {
	_ = p.ptmx.Close()
	_ = p.emu.Close()
}

// join waits for one teardown goroutine to finish, or for ctx to expire
// first — which is what keeps a wedged, unkillable child (a documented
// Apple Container failure mode: a container exec stuck in an
// uninterruptible wait) from freezing the caller, often the UI goroutine,
// forever.
func (p *Pane) join(ctx context.Context, done <-chan struct{}, who string) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pane: close: %s did not stop: %w", who, ctx.Err())
	}
}

// endChild hangs the child up and then insists, and it signals the process
// GROUP rather than the process: pty.StartWithSize starts the child with
// Setsid, so it leads its own session and group and its own children — a
// shell's jobs, a container exec's helpers — are in that group. SIGHUP can
// be ignored, though — bash gives a backgrounded job SIG_IGN for it, for
// instance — so this alone does not guarantee every group member dies;
// shutdown's killGroup, run once the direct child is confirmed reaped, is
// what closes that gap with a signal nothing can ignore. Setting
// p.signalled here — never inside the early return below — is what tells
// killGroup whether this call sent anything at all; see its doc.
func (p *Pane) endChild(ctx context.Context) {
	if p.cmd.Pid() == 0 {
		return
	}
	// Already reaped: cmd.Wait has returned, so the kernel is free to hand
	// that pid to somebody else, and -pgid would signal a stranger. An
	// exited pane keeps its last screen until the control plane closes it,
	// which can be minutes — plenty of time for the pid to be reused. Note
	// that p.signalled is NOT set on this path: nothing was sent.
	select {
	case <-p.waitDone:
		return
	default:
	}
	pgid := p.cmd.Pid() // Setsid makes the child its own group leader
	p.signalled = true
	_ = p.kill(-pgid, syscall.SIGHUP)
	select {
	case <-p.waitDone:
		return
	case <-ctx.Done():
	case <-time.After(killGrace):
	}
	_ = p.kill(-pgid, syscall.SIGKILL)
}

// killGroup sends one final, unconditional SIGKILL to the child's process
// group — but only when endChild actually signalled it during this Close.
// When the child was already reaped before Close ran, endChild takes its
// early return without touching the group, and killGroup must not either:
// signalling a pid this Close never touched is exactly the stranger-
// signalling hazard endChild's own doc describes, and a bare "pid != 0"
// check does not know the difference. p.signalled does.
//
// When it does run, it closes a real gap: endChild's SIGHUP can be ignored,
// but SIGKILL cannot. It still carries a pid-reuse risk — the kernel is
// free to recycle a pid the instant cmd.Wait reaps it, which is confirmed
// to have just happened by the join in shutdown before this runs — but that
// window is the microseconds between our own reap and this call, not the
// unbounded one a signal fired long after Close had already finished would
// carry. ESRCH — nothing left to signal — is the expected outcome once the
// reap really did leave the group empty, and is ignored the same way every
// other signal-delivery failure in this file is.
func (p *Pane) killGroup() {
	if !p.signalled {
		return
	}
	if pgid := p.cmd.Pid(); pgid != 0 {
		_ = p.kill(-pgid, syscall.SIGKILL)
	}
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
//
// A signal death (endChild's own SIGHUP/SIGKILL, or anything else) is not an
// ordinary exit: exec.ExitError.ExitCode() already reports -1 for one, but
// folding it into the nil-error, code-only shape Exited() otherwise reports
// would lose which signal it was. WaitStatus.Signaled()/Signal() recover it
// — except when p.closing is already set, meaning Close itself caused this
// death (see shutdown), in which case that is not news worth reporting as
// an error: it is reported the same way an ordinary exit is, code -1 with a
// nil error, which Exited's doc names as meaning exactly that.
func (p *Pane) waiter() {
	defer close(p.waitDone)
	err := p.cmd.Wait()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			p.mu.Lock()
			closing := p.closing
			p.mu.Unlock()
			if closing {
				code, err = -1, nil
			} else {
				code, err = -1, fmt.Errorf("pane: child killed by %v", ws.Signal())
			}
		} else {
			code, err = exitErr.ExitCode(), nil
		}
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
