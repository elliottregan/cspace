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
