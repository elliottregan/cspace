package pane

import (
	"context"
	"errors"
	"io"
	"os"
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
func openTestPane(t *testing.T, script string, cols, rows int, opts ...Option) *Pane {
	t.Helper()
	sh := shell(t)
	p, err := Open(Command{
		Path: sh,
		Args: []string{"bash", "-c", script},
		Env:  []string{"TERM=xterm-256color", "COLORTERM=truecolor", "PS1="},
	}, cols, rows, opts...)
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
		if errors.Is(err, exec.ErrNotFound) {
			t.Skipf("pgrep is not on PATH")
		}
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

// A child that never negotiates the kitty keyboard protocol still gets the
// CSI-u form when the pane was opened with ExtendedKeys. This is the pane
// side of Task 8 Step 5: run under tmux, Claude Code's own `ESC [ > 5 u`
// goes to tmux and never reaches the host, so without the option Shift+Enter
// degrades to a plain Enter — which sends the half-written message instead
// of breaking the line.
func TestExtendedKeysSendsCSIUWithoutTheChildAskingForIt(t *testing.T) {
	p := openTestPane(t,
		`IFS= read -r -s -d u seq; printf 'SEQ[%s]' "${seq#$'\033'[}"; sleep 30`,
		40, 6, ExtendedKeys())
	time.Sleep(300 * time.Millisecond)
	p.SendKey(KeyEvent{Code: KeyEnter, Mod: ModShift})
	waitForScreen(t, p, "SEQ[13;2")
}

// ...while a key whose legacy form carries its modifier keeps that form
// even with the option on. Shift+Tab is the case: ESC[Z says "shift" out
// loud, tmux forwards a CSI-u Shift+Tab verbatim (it has no table entry to
// fold it back to), and in a Claude pane this is the permission-mode cycle.
// The first cut of ExtendedKeys changed it to ESC[9;2u for every pane.
func TestExtendedKeysLeaveShiftTabLegacy(t *testing.T) {
	// cat -v prints the bytes it reads in caret notation, so ESC[Z lands on
	// the screen as ^[[Z and a CSI-u form would land as ^[[9;2u.
	p := openTestPane(t, `stty raw -echo; cat -v`, 40, 6, ExtendedKeys())
	time.Sleep(300 * time.Millisecond)
	p.SendKey(KeyEvent{Code: KeyTab, Mod: ModShift})
	waitForScreen(t, p, "^[[Z")
}

// ...and without the option the same key degrades, which is the right
// answer for a child that speaks for itself: it asked for nothing, so it
// gets the encoding a legacy terminal would have sent.
func TestWithoutExtendedKeysShiftEnterDegrades(t *testing.T) {
	p := openTestPane(t, `read -r line; printf 'GOT[%s]' "$line"; sleep 30`, 40, 6)
	time.Sleep(300 * time.Millisecond)
	for _, r := range "hi" {
		p.SendKey(KeyEvent{Code: r, Text: string(r)})
	}
	p.SendKey(KeyEvent{Code: KeyEnter, Mod: ModShift})
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

func TestPaneExitedReportsNotYetExitedForALiveChild(t *testing.T) {
	p := openTestPane(t, `sleep 30`, 40, 6)
	code, err, ok := p.Exited()
	if ok {
		t.Fatalf("Exited = (%d, %v, %v), want ok = false for a running child", code, err, ok)
	}
	if code != 0 || err != nil {
		t.Errorf("Exited = (%d, %v, false), want (0, nil, false)", code, err)
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

	// The assertion is "Close returned instead of blocking on a Wait that
	// never finishes", and the ctx it was given expires at 200ms. Two
	// seconds proves that just as well as a tight bound would, and does not
	// flake on a loaded machine running -race alongside another test
	// binary — the one wall-clock upper bound in this package is not the
	// place to be precise.
	if elapsed > 2*time.Second {
		t.Errorf("Close took %s, want it to return when its 200ms ctx expired", elapsed)
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
	// shutdown's defer runs on every return, including this give-up one, so
	// Closed() must already report true even though the child itself is
	// still alive (Exited() would say so) — Closed() is about Dirty and
	// shutdown having run, not about the child.
	if !p.Closed() {
		t.Error("Closed() is false after a Close that gave up on the waiter")
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
	// be caused by the Resize call below. Bounded overall so a pane that
	// (wrongly) never goes quiet fails with a clear message instead of
	// hanging the test.
	quietBy := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-p.Dirty():
			if time.Now().After(quietBy) {
				t.Fatal("the pane never went quiet after startup")
			}
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

// TestPaneDirtyIsClosedOnceThePaneIs — the control plane's redraw loop parks
// a goroutine on `<-p.Dirty()` per tick. After Close the pump and the waiter
// have both exited, so nothing would ever signal the channel again: without
// a terminal state that goroutine parks for the life of the process, and its
// closure retains the whole *Pane, x/vt's eagerly allocated 4 MiB parser
// buffer included. One leaked goroutine and ~4 MiB per pane the operator
// ever opens and closes.
func TestPaneDirtyIsClosedOnceThePaneIs(t *testing.T) {
	p := openTestPane(t, `printf 'hello'; sleep 30`, 40, 6)
	waitForScreen(t, p, "hello")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The channel carries one slot, so the first receive can hand back a
	// signal the pump or the waiter queued on its way out; the close is
	// what the next one reports. Both have to return promptly.
	deadline := time.After(100 * time.Millisecond)
	closed := false
	for i := 0; i < 2 && !closed; i++ {
		select {
		case _, ok := <-p.Dirty():
			closed = !ok
		case <-deadline:
			t.Fatal("a receive on Dirty blocked for 100ms after Close returned")
		}
	}
	if !closed {
		t.Fatal("Dirty is still open after Close; a waiter on it would park forever")
	}

	// ...and the pane answers the question the unparked waiter then asks.
	if _, _, exited := p.Exited(); !exited {
		t.Error("Exited() reports the child still running after Close")
	}

	// markDirty has to be a no-op now rather than a send on a closed
	// channel: on Close's give-up paths the pump is still parked in Read
	// and calls it on its way out, after the close. Calling it directly is
	// the only way to reach that ordering deterministically.
	p.markDirty()
}

// TestPaneClosedReportsWhetherCloseHasRun pins Closed() as the structural
// counterpart to Dirty's terminal state: a caller deciding whether to wait
// on Dirty again should be able to ask this directly, rather than infer it
// from Exited — which a give-up-on-the-waiter Close can leave false (the
// child is still alive) even though Dirty is already closed and shutdown is
// done.
func TestPaneClosedReportsWhetherCloseHasRun(t *testing.T) {
	p := openTestPane(t, `sleep 30`, 40, 6)
	if p.Closed() {
		t.Fatal("Closed() is true before Close ever ran")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !p.Closed() {
		t.Error("Closed() is false after Close returned")
	}
}

// TestOpenAndResizeClampAnOversizeScreen — both sizes end up in a uint16
// (TIOCSWINSZ's ws_col/ws_row, and creack/pty's Winsize), where 70000 wraps
// to 4464 rather than failing. validateOpen only rejects <= 0, so without
// the clamp an oversize request silently produces a narrow pane.
//
// It drives open() with a stub emulator rather than Open(): the clamp is
// about the conversions, and building a real x/vt screen 65535 columns wide
// to observe them would allocate hundreds of megabytes.
func TestOpenAndResizeClampAnOversizeScreen(t *testing.T) {
	sh := shell(t)
	p, err := open(Command{Path: sh, Args: []string{"bash", "-c", "sleep 30"}}, 70000, 10, newStubEmulator())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})

	ws, err := pty.GetsizeFull(p.ptmx)
	if err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	}
	if ws.Cols != 65535 || ws.Rows != 10 {
		t.Errorf("pty opened at %dx%d, want 65535x10 (70000 truncates to 4464 unclamped)", ws.Cols, ws.Rows)
	}

	if err := p.Resize(70000, 70000); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	ws, err = pty.GetsizeFull(p.ptmx)
	if err != nil {
		t.Fatalf("GetsizeFull after Resize: %v", err)
	}
	if ws.Cols != 65535 || ws.Rows != 65535 {
		t.Errorf("pty resized to %dx%d, want 65535x65535", ws.Cols, ws.Rows)
	}
}

// TestHostShell covers the two branches nothing else in this package reaches:
// the basename slicing that produces argv[0], and the $SHELL-unset fallback.
// 4b opens host-shell panes from both.
func TestHostShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	c := HostShell()
	if c.Path != "/bin/sh" {
		t.Errorf("Path = %q, want /bin/sh", c.Path)
	}
	if len(c.Args) != 2 || c.Args[0] != "sh" || c.Args[1] != "-l" {
		t.Errorf("Args = %q, want [sh -l]", c.Args)
	}

	// argv[0] is the shell's own name, not its path — a login shell reads
	// its argv[0] to decide what it is.
	t.Setenv("SHELL", "/opt/homebrew/bin/fish")
	if c := HostShell(); c.Path != "/opt/homebrew/bin/fish" || c.Args[0] != "fish" {
		t.Errorf("HostShell() = %+v, want path /opt/homebrew/bin/fish with argv[0] fish", c)
	}

	// Unset, not empty: the documented fallback. The t.Setenv above still
	// restores the developer's own $SHELL on cleanup.
	if err := os.Unsetenv("SHELL"); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}
	c = HostShell()
	if c.Path != "/bin/bash" {
		t.Errorf("Path with $SHELL unset = %q, want the /bin/bash fallback", c.Path)
	}
	if len(c.Args) != 2 || c.Args[0] != "bash" || c.Args[1] != "-l" {
		t.Errorf("Args with $SHELL unset = %q, want [bash -l]", c.Args)
	}
}
