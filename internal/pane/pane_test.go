package pane

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
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

// groupMembers counts live processes named name in process group pgid, via
// pgrep. Used to poll for a child having actually forked its own
// descendants rather than guessing a fixed delay.
func groupMembers(t *testing.T, pgid int, name string) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-g", strconv.Itoa(pgid), name).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return 0 // pgrep's own "nothing matched" exit code
		}
		t.Fatalf("pgrep: %v", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
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
	//
	// `stty raw -echo` is load-bearing: without it the pty stays in the
	// default cooked/echo mode, and the kernel itself echoes the flood's
	// bytes back onto the screen (indistinguishable from real child output
	// to the output pump), scrolling "ALIVE" off a 6-row screen long before
	// any queue-based dropping is even relevant — verified empirically
	// against this host's pty: 8MiB of unterminated cooked-mode input never
	// once blocks ptmx.Write, while the same flood under `stty raw -echo`
	// blocks after ~1KiB with nothing echoed. Raw mode is what makes this a
	// test of the engine's own backpressure rather than of tty echo.
	p := openTestPane(t, `stty raw -echo; printf 'ALIVE'; sleep 30`, 40, 6)
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
	// another thirty seconds, but that is not what makes this finish inside
	// the timeout below: endChild kills the child (and the group) before
	// either the writer or the pump is joined, so by the time shutdown gets
	// there both have already been released by the child's death, not by
	// the close. This proves Close completes within its bound against a
	// child that stopped reading, which the naive "join then close" order
	// would not — that ordering is what the design document and this
	// package's own comments describe, checked directly in
	// TestPaneCloseHonoursContextAgainstAWaiterThatNeverFinishes. In the
	// control plane this call is on the UI goroutine.
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
	// Close joins all four of the pane's own goroutines, so none of their
	// frames should be on any goroutine's stack shortly afterward. A bare
	// count comparison (as this used to be) can pass by accident if the
	// runtime's own bookkeeping goroutines happen to shift by the same
	// amount in either direction; naming the frame is exact.
	deadline := time.Now().Add(time.Second)
	for {
		leaked := paneGoroutines()
		if leaked == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutines from this package are still running a second after Close:\n%s", leaked)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// paneGoroutines returns the stack traces of every currently running
// goroutine, other than the one calling this, whose stack mentions this
// package — or "" if there are none. paneGoroutines itself always appears on
// the calling goroutine's own stack, which is how its frame is told apart
// from an actual leak without matching goroutine IDs.
func paneGoroutines() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	var leaked []string
	for _, frame := range strings.Split(string(buf[:n]), "\n\n") {
		if !strings.Contains(frame, "cspace/internal/pane.") {
			continue
		}
		if strings.Contains(frame, "paneGoroutines") {
			continue // this call's own stack
		}
		leaked = append(leaked, frame)
	}
	return strings.Join(leaked, "\n\n")
}

func TestPaneCloseHonoursContextAgainstAWaiterThatNeverFinishes(t *testing.T) {
	// A wedged, unkillable direct child (a container exec stuck in an
	// uninterruptible wait is a documented Apple Container failure mode) is
	// what this proves Close survives. It can't be built from a real
	// process — SIGKILL cannot be trapped, so no child can be made to
	// actually ignore it — so this uses a real, killable process purely to
	// give endChild's group signal a safe, valid target, wrapped so its
	// Wait blocks until the test releases it regardless of what happens to
	// the real process underneath.
	sh := shell(t)
	real := &exec.Cmd{Path: sh, Args: []string{"bash", "-c", "sleep 30"}}
	ptmx, err := pty.StartWithSize(real, &pty.Winsize{Rows: 6, Cols: 40})
	if err != nil {
		t.Fatalf("start stub child: %v", err)
	}

	release := make(chan struct{})
	stub := newStubEmulator()
	p := &Pane{
		emu:        stub,
		cmd:        &neverWaits{Cmd: real, release: release},
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
	t.Cleanup(func() {
		// Drive the rest of the teardown by hand: Close already gave up and
		// sync.Once means calling it again cannot retry. Releasing the stub
		// lets the real (already-SIGKILLed, by endChild's own group signal)
		// process actually be reaped, which is what a real Close would have
		// waited for.
		close(release)
		_ = p.emu.Close()
		_ = p.ptmx.Close()
		<-p.waitDone
		<-p.writerDone
		<-p.pumpDone
		<-p.drainDone
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	closeErr := p.Close(ctx)
	elapsed := time.Since(start)

	if elapsed > 300*time.Millisecond {
		t.Errorf("Close took %s, want well under 300ms", elapsed)
	}
	if closeErr == nil || !strings.Contains(closeErr.Error(), "waiter") {
		t.Errorf("Close err = %v, want an error naming the waiter", closeErr)
	}
	if second := p.Close(context.Background()); second != closeErr {
		t.Errorf("second Close = %v, want the exact same error as the first (%v)", second, closeErr)
	}
	// Close gave up on the waiter and never reached its own emu.Close()
	// call, but it must still have released the emulator best-effort on
	// that early-return path — otherwise a pane whose child is wedged holds
	// x/vt's 4 MiB parser buffer for the rest of the process's life.
	if !stub.wasClosed() {
		t.Error("Close gave up on the waiter but never released the emulator")
	}
}

// stubEmulator is a minimal Emulator whose Read blocks — like the real
// adapter's unbuffered pipe — until Close is called, and which records
// whether Close ran. Used only to observe that shutdown releases the
// emulator on its ctx-timeout path, which an opaque real vtEmulator cannot
// show directly.
type stubEmulator struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newStubEmulator() *stubEmulator { return &stubEmulator{closed: make(chan struct{})} }

func (s *stubEmulator) Write(p []byte) (int, error) { return len(p), nil }
func (s *stubEmulator) Resize(int, int)             {}
func (s *stubEmulator) Render() string              { return "" }
func (s *stubEmulator) CursorPosition() (int, int)  { return 0, 0 }
func (s *stubEmulator) SendKey(KeyEvent)            {}
func (s *stubEmulator) Paste(string)                {}
func (s *stubEmulator) Scrollback() Scrollback      { return stubScrollback{} }

func (s *stubEmulator) Read(p []byte) (int, error) {
	<-s.closed
	return 0, io.EOF
}

func (s *stubEmulator) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *stubEmulator) wasClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

type stubScrollback struct{}

func (stubScrollback) Len() int        { return 0 }
func (stubScrollback) Line(int) string { return "" }

// neverWaits wraps a real, killable *exec.Cmd so its Wait blocks until the
// test releases it, independent of what actually happens to the real
// process. See TestPaneCloseHonoursContextAgainstAWaiterThatNeverFinishes.
type neverWaits struct {
	*exec.Cmd
	release chan struct{}
}

func (n *neverWaits) Pid() int {
	if n.Process == nil {
		return 0
	}
	return n.Process.Pid
}

func (n *neverWaits) Wait() error {
	<-n.release
	return n.Cmd.Wait()
}

func TestPaneCloseKillsEveryMemberOfTheChildsProcessGroup(t *testing.T) {
	sh := shell(t)
	p, err := Open(Command{
		Path: sh,
		// The backgrounded sleep is the one that matters: bash gives an
		// asynchronous job in a non-interactive script SIG_IGN for SIGHUP,
		// so endChild's first signal does not touch it, and only the
		// follow-up SIGKILL does.
		Args: []string{"bash", "-c", "sleep 30 & sleep 30"},
	}, 40, 6)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pgid := p.cmd.Pid() // Setsid makes the direct child its own group leader
	// Poll for both sleeps to actually exist rather than guessing a fixed
	// delay: a slow scheduler could still leave one unforked at any fixed
	// sleep, which would make Close's cleanup trivially "complete" without
	// ever proving the backgrounded one was reached.
	deadline := time.Now().Add(5 * time.Second)
	for groupMembers(t, pgid, "sleep") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("bash never forked both sleeps in group %d", pgid)
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline = time.Now().Add(time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			return // the whole group, including the backgrounded sleep, is gone
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still has a live member a second after Close (kill -0 err = %v)", pgid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPaneClosingAnAlreadyExitedChildSendsNoSignal(t *testing.T) {
	// A child that exited long before Close is the exact case endChild's
	// own "already reaped" comment exists for: the pid it left behind may
	// already belong to a stranger, so Close must not signal it — not with
	// SIGHUP, not with killGroup's follow-up SIGKILL. That can't be proven
	// by inspecting a real process group afterward (there is nothing left
	// to inspect), so this records every call through the injectable kill
	// seam instead.
	sh := shell(t)
	p, err := Open(Command{Path: sh, Args: []string{"bash", "-c", "exit 0"}}, 40, 6)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := p.Exited(); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, ok := p.Exited(); !ok {
		t.Fatal("the pane never reported the child's exit")
	}

	var mu sync.Mutex
	var sent []string
	p.kill = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		sent = append(sent, sig.String())
		mu.Unlock()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 0 {
		t.Errorf("Close signalled an already-exited child: %v", sent)
	}
}

func TestPaneResizeAfterCloseReturnsAnError(t *testing.T) {
	p := openTestPane(t, `sleep 30`, 40, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Resize(80, 24); err == nil {
		t.Error("Resize after Close: want an error, got nil")
	}
}

func TestPaneReportsASignalDeath(t *testing.T) {
	// A foreign signal — sent by the test directly, never through Close —
	// must be reported as the failure it is, unlike a death Close itself
	// causes (TestPaneCloseReportsANilErrorForItsOwnSignalDeath).
	p := openTestPane(t, `sleep 30`, 40, 6)
	pid := p.cmd.Pid()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, err, ok := p.Exited(); ok {
			if code != -1 {
				t.Errorf("code = %d, want -1", code)
			}
			// Contains("killed") alone would pass against the format
			// string's own literal even if the signal itself were wrong;
			// the signal's own name is the part that has to be right.
			if err == nil || !strings.Contains(err.Error(), syscall.SIGKILL.String()) {
				t.Errorf("err = %v, want an error naming %s", err, syscall.SIGKILL)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the pane never reported the child's exit")
}

func TestPaneCloseReportsANilErrorForItsOwnSignalDeath(t *testing.T) {
	// endChild ends a running child with SIGHUP (or SIGKILL); that is not a
	// foreign failure, and Exited must not report it as one — a control
	// plane rendering every pane the operator closed as "killed by hangup"
	// is exactly the bug this guards against.
	p := openTestPane(t, `sleep 30`, 40, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	code, err, ok := p.Exited()
	if !ok {
		t.Fatal("Exited: the child was not reported as exited after Close")
	}
	if code != -1 {
		t.Errorf("code = %d, want -1", code)
	}
	if err != nil {
		t.Errorf("err = %v, want nil (Exited's doc: -1 with a nil error means Close closed the pane)", err)
	}
}

func TestPaneResizeSignalsDirty(t *testing.T) {
	p := openTestPane(t, `sleep 30`, 40, 6)
	// A single fixed sleep-then-drain cannot tell Resize's own signal apart
	// from a late startup signal that just happens to land after it: drain
	// until nothing arrives for a stretch, so whatever comes next can only
	// be caused by the Resize call below.
	for {
		select {
		case <-p.Dirty():
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	select {
	case <-p.Dirty():
	case <-time.After(2 * time.Second):
		t.Fatal("Resize did not signal Dirty")
	}
}

func TestOpenValidatesBeforeBuildingTheEmulator(t *testing.T) {
	if _, err := Open(Command{}, 40, 6); err == nil {
		t.Error("Open with an empty command: want an error")
	}
	if _, err := Open(Command{Path: "sh", Args: []string{"sh"}}, 0, 6); err == nil {
		t.Error("Open with a zero size: want an error")
	}
}

func TestPaneResolvesABareCommandNameAgainstPATH(t *testing.T) {
	p, err := Open(Command{Path: "sh", Args: []string{"sh", "-c", "printf ok; sleep 30"}}, 40, 6)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	waitForScreen(t, p, "ok")
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
