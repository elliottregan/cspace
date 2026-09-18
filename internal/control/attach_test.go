package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// clientScript replies to ListClients calls with successive snapshots and to
// everything else with a clean exit, so a test can say "this is what tmux
// showed before the attach, and this is what it showed after".
func clientScript(snapshots ...string) func(int, []string) (string, int, error) {
	var listN int
	return func(_ int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "list-clients" {
			i := listN
			if i >= len(snapshots) {
				i = len(snapshots) - 1
			}
			listN++
			return snapshots[i], 0, nil
		}
		return "", 0, nil
	}
}

// TestBeginAttachRecordsTheNewClient — tmux never tells a client its own tty,
// and the host-side `container exec` cannot ask. The one tty that appears
// between the snapshot before the attach and the one after is this client's,
// which is the whole reason the lock is held across that window.
func TestBeginAttachRecordsTheNewClient(t *testing.T) {
	dir := t.TempDir()
	// Snapshot before the attach (somebody else was already attached), then
	// the one after (ours showed up).
	f := &fakeExec{reply: clientScript(
		"/dev/pts/1\n",
		"/dev/pts/1\n/dev/pts/2\n",
	)}
	tm := testTmux(f)

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	if att.TTY() != "/dev/pts/2" {
		t.Fatalf("TTY() = %q, want the one new tty /dev/pts/2", att.TTY())
	}

	path := filepath.Join(dir, "cspace-claude.dev-pts-2.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no client record at %s: %v", path, err)
	}
	var rec ClientRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("client record is not JSON: %v (%s)", err, data)
	}
	if rec.Session != SessionClaude || rec.TTY != "/dev/pts/2" {
		t.Errorf("record = %+v, want session %q tty /dev/pts/2", rec, SessionClaude)
	}
	if rec.PID != os.Getpid() {
		t.Errorf("record PID = %d, want this process %d — the startup sweep uses it to spot a crashed attach", rec.PID, os.Getpid())
	}
	if _, err := time.Parse(time.RFC3339, rec.At); err != nil {
		t.Errorf("record At = %q, want RFC3339", rec.At)
	}

	// The lock must be free again once the client is known: a second attach
	// to the same sandbox has to be able to start.
	assertLockFree(t, dir)

	// ── Close detaches exactly that client and removes its record ────────
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	var detached bool
	for _, call := range f.recorded() {
		if len(call) >= 4 && call[1] == "detach-client" && call[3] == "/dev/pts/2" {
			detached = true
		}
	}
	if !detached {
		t.Errorf("Close() did not run `tmux detach-client -t /dev/pts/2`: %v", f.recorded())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("client record survived Close(): %v", err)
	}
}

// TestCloseIsIdempotent — attach calls it on the child's exit, and may call it
// again from a signal path.
func TestCloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{reply: clientScript("", "/dev/pts/3\n")}
	att, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
	var detaches int
	for _, call := range f.recorded() {
		if len(call) >= 2 && call[1] == "detach-client" {
			detaches++
		}
	}
	if detaches != 1 {
		t.Errorf("detach ran %d times, want exactly 1", detaches)
	}
}

// TestBeginAttachWithoutSessionIsInert — the no-tmux fallback. Callers must
// not have to nil-check, and nothing may be written for a sandbox that has no
// tmux to detach from.
func TestBeginAttachWithoutSessionIsInert(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{}
	att, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, "")
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	if err := att.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if calls := f.recorded(); len(calls) != 0 {
		t.Errorf("inert attachment ran %v", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("inert attachment wrote %d file(s) into the control-plane dir", len(entries))
	}
}

// TestCloseWithoutAKnownClientStillSucceeds — the attach died before its
// client ever showed up (a failed exec). There is nothing to detach, and
// failing here would mask the real error from the child.
func TestCloseWithoutAKnownClientStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{reply: clientScript("")} // no client ever appears
	tm := testTmux(f)
	tm.PollFor = time.Minute // Close must not wait this out

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- att.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() blocked waiting for a client that never appeared")
	}
	assertLockFree(t, dir)
}

// TestBeginAttachFailsWhenTheLockIsHeld — the lock is what makes "the one new
// tty" unambiguous. If another attach is mid-window, this one must not guess.
func TestBeginAttachFailsWhenTheLockIsHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "attach.lock")
	held, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("could not take the lock in the test: %v", err)
	}

	old := attachLockTimeout
	attachLockTimeout = 150 * time.Millisecond
	defer func() { attachLockTimeout = old }()

	f := &fakeExec{reply: clientScript("")}
	if _, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude); err == nil {
		t.Error("BeginAttach() succeeded while the attach lock was held")
	}
}

// TestCloseLeavesTheRecordWhenDetachFails — the startup sweep is the only
// backstop for a client tmux still lists; deleting the record on a failed
// detach would hide that from it. Regression test for finding 1: Close used
// to run os.Remove unconditionally regardless of whether DetachClient
// succeeded.
func TestCloseLeavesTheRecordWhenDetachFails(t *testing.T) {
	dir := t.TempDir()
	list := clientScript("", "/dev/pts/4\n")
	f := &fakeExec{reply: func(n int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "detach-client" {
			return "no server running", 1, nil
		}
		return list(n, cmdline)
	}}
	tm := testTmux(f)

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	path := filepath.Join(dir, recordName(SessionClaude, "/dev/pts/4"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("record not written before Close: %v", err)
	}

	err = att.Close(context.Background())
	if err == nil {
		t.Fatal("Close() error = nil, want the tmux failure surfaced")
	}
	if !strings.Contains(err.Error(), "no server running") {
		t.Errorf("Close() error = %q, want it to contain tmux's own message", err.Error())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("record deleted despite a failed detach: %v", err)
	}
}

// TestCloseDetachesEvenWithACancelledContext — Task 8's SIGHUP path calls
// Close with a context that is already done; the detach must still run
// rather than being silently skipped because the caller can no longer wait
// on it. Regression test for finding 1.
func TestCloseDetachesEvenWithACancelledContext(t *testing.T) {
	dir := t.TempDir()
	f := &fakeExec{reply: clientScript("", "/dev/pts/5\n")}
	att, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}
	waitTracked(t, att)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := att.Close(ctx); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	var detaches int
	for _, call := range f.recorded() {
		if len(call) >= 4 && call[1] == "detach-client" && call[3] == "/dev/pts/5" {
			detaches++
		}
	}
	if detaches != 1 {
		t.Errorf("detach ran %d times with an already-cancelled context, want exactly 1: %v", detaches, f.recorded())
	}
}

// TestCloseRacingDiscoveryStillDetaches — Close must not lose a client that
// track discovers while Close is already tearing down. Before the fix, track
// checked a.closed and skipped recording the tty it just found whenever
// Close had already flipped it, so a client whose discovery raced Close's
// start was silently left attached with no record (finding 2). Deterministic:
// the discovery poll and the detach call are each gated by their own
// channel, never a sleep.
func TestCloseRacingDiscoveryStillDetaches(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	detachEntered := make(chan struct{})
	detachRelease := make(chan struct{})

	f := &fakeExec{reply: func(n int, cmdline []string) (string, int, error) {
		switch {
		case len(cmdline) > 1 && cmdline[1] == "list-clients":
			if n == 0 {
				return "", 0, nil // "before": nobody attached yet
			}
			close(entered)
			<-release
			return "/dev/pts/6\n", 0, nil
		case len(cmdline) > 1 && cmdline[1] == "detach-client":
			close(detachEntered)
			<-detachRelease
			return "", 0, nil
		default:
			return "", 0, nil
		}
	}}
	tm := testTmux(f)

	att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("BeginAttach() error: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("discovery poll never started")
	}

	// Close starts while the discovery poll that will find our client is
	// still blocked inside tmux's list-clients call — exactly the
	// interleaving that must not lose the client.
	closeDone := make(chan error, 1)
	go func() { closeDone <- att.Close(context.Background()) }()

	close(release)

	select {
	case <-detachEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("Close() never reached the detach call — the racing client was lost")
	}

	path := filepath.Join(dir, recordName(SessionClaude, "/dev/pts/6"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("record not written before the detach: %v", err)
	}
	if tty := att.TTY(); tty != "/dev/pts/6" {
		t.Fatalf("TTY() = %q, want /dev/pts/6", tty)
	}

	close(detachRelease)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() did not return")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("record survived Close(): %v", err)
	}
	var detaches int
	for _, call := range f.recorded() {
		if len(call) >= 4 && call[1] == "detach-client" && call[3] == "/dev/pts/6" {
			detaches++
		}
	}
	if detaches != 1 {
		t.Errorf("detach ran %d times, want exactly 1: %v", detaches, f.recorded())
	}
}

// TestBeginAttachSerializesThenSucceeds — proves the lock's handoff, not just
// its fail-fast (that's TestBeginAttachFailsWhenTheLockIsHeld): a second
// attach queued behind the first's discovery window starts only once that
// window closes, and its own "before" snapshot already reflects the first
// attach's client. Ordering is proven from the fake's recorded calls, never
// from a sleep (finding 3).
func TestBeginAttachSerializesThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	gate := make(chan struct{})
	entered := make(chan struct{})

	var mu sync.Mutex
	var callCount int
	firstDiscoveryIndex := -1
	secondBeforeIndex := -1

	f := &fakeExec{reply: func(n int, cmdline []string) (string, int, error) {
		if len(cmdline) <= 1 || cmdline[1] != "list-clients" {
			return "", 0, nil
		}
		mu.Lock()
		callCount++
		ordinal := callCount
		mu.Unlock()

		switch ordinal {
		case 1:
			return "", 0, nil // attach 1's "before": nobody attached yet
		case 2:
			mu.Lock()
			firstDiscoveryIndex = n
			mu.Unlock()
			close(entered)
			<-gate
			return "/dev/pts/7\n", 0, nil // attach 1's own client shows up
		case 3:
			mu.Lock()
			secondBeforeIndex = n
			mu.Unlock()
			return "/dev/pts/7\n", 0, nil // attach 2's "before": attach 1 already live
		default:
			return "/dev/pts/7\n/dev/pts/8\n", 0, nil // attach 2's own client shows up too
		}
	}}
	tm := testTmux(f)

	att1, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err != nil {
		t.Fatalf("first BeginAttach() error: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first attach's discovery poll never started")
	}

	type result struct {
		att *Attachment
		err error
	}
	second := make(chan result, 1)
	go func() {
		a2, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
		second <- result{a2, err}
	}()

	// Non-blocking: the second attach cannot possibly have returned yet — it
	// needs the flock the first attach still holds — so this is a state
	// check, not a timing gamble.
	select {
	case r := <-second:
		t.Fatalf("second BeginAttach() returned (err=%v) before the first attach's lock was released", r.err)
	default:
	}

	close(gate)
	waitTracked(t, att1)
	if att1.TTY() != "/dev/pts/7" {
		t.Fatalf("first attach TTY() = %q, want /dev/pts/7", att1.TTY())
	}

	var r result
	select {
	case r = <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("second BeginAttach() never returned after the lock was released")
	}
	if r.err != nil {
		t.Fatalf("second BeginAttach() error: %v", r.err)
	}
	waitTracked(t, r.att)
	if r.att.TTY() != "/dev/pts/8" {
		t.Fatalf("second attach TTY() = %q, want /dev/pts/8", r.att.TTY())
	}

	mu.Lock()
	fi, si := firstDiscoveryIndex, secondBeforeIndex
	mu.Unlock()
	if fi < 0 || si < 0 {
		t.Fatalf("did not observe both calls: firstDiscoveryIndex=%d secondBeforeIndex=%d", fi, si)
	}
	if si <= fi {
		t.Errorf("second attach's before-snapshot call (index %d) did not come after the first attach's discovery call (index %d)", si, fi)
	}

	if err := att1.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := r.att.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

func TestRecordName(t *testing.T) {
	got := recordName(SessionClaude, "/dev/pts/12")
	want := "cspace-claude.dev-pts-12.json"
	if got != want {
		t.Errorf("recordName = %q, want %q", got, want)
	}
}

// waitTracked blocks until the background tracking goroutine has finished
// looking for this attach's client.
func waitTracked(t *testing.T, att *Attachment) {
	t.Helper()
	select {
	case <-att.trackDone:
	case <-time.After(10 * time.Second):
		t.Fatal("client tracking did not finish")
	}
}

// assertLockFree proves the attach lock was released by taking it.
func assertLockFree(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "attach.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("attach lock still held: %v", err)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
