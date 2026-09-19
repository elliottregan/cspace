package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
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
// The wait is derived from tm.PollFor (see attachLockWait), so the test
// shortens PollFor itself rather than reaching for a fixed timeout constant.
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

	f := &fakeExec{reply: clientScript("")}
	tm := testTmux(f)
	tm.PollFor = 50 * time.Millisecond

	_, err = BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
	if err == nil {
		t.Fatal("BeginAttach() succeeded while the attach lock was held")
	}
	// A busy lock is contention, not a local bookkeeping problem: callers
	// must not downgrade it to a warning-and-proceed the way they do for
	// ErrBookkeepingUnavailable.
	if errors.Is(err, ErrBookkeepingUnavailable) {
		t.Error("a busy lock must stay a hard error, not ErrBookkeepingUnavailable")
	}
}

// TestAttachLockWaitDerivesFromPollFor — Important 4(a): the lock is held
// across the whole discovery window (see track), so the wait for it must
// exceed PollFor or a second attach can fail against a first attach that is
// merely still succeeding, not stuck.
func TestAttachLockWaitDerivesFromPollFor(t *testing.T) {
	tm := testTmux(&fakeExec{})
	tm.PollFor = 3 * time.Second
	if got, want := attachLockWait(tm), 5*time.Second; got != want {
		t.Errorf("attachLockWait() = %s, want PollFor(%s)+2s = %s", got, tm.PollFor, want)
	}
}

// TestAttachLockWaitOverride — the test-only escape hatch for a short wait
// independent of PollFor.
func TestAttachLockWaitOverride(t *testing.T) {
	tm := testTmux(&fakeExec{})
	tm.PollFor = 3 * time.Second
	old := attachLockTimeout
	attachLockTimeout = 250 * time.Millisecond
	defer func() { attachLockTimeout = old }()
	if got, want := attachLockWait(tm), 250*time.Millisecond; got != want {
		t.Errorf("attachLockWait() = %s, want the override %s", got, want)
	}
}

// TestBeginAttachBookkeepingUnavailableOnDirCreateFailure — Important 4(b):
// a local filesystem problem creating the control-plane directory must not
// refuse the attach; the caller downgrades ErrBookkeepingUnavailable to a
// warning and an inert attachment instead.
func TestBeginAttachBookkeepingUnavailableOnDirCreateFailure(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "controlplane") // MkdirAll under a file fails

	f := &fakeExec{}
	_, err := BeginAttach(context.Background(), testTmux(f), "cspace-demo-mercury", dir, SessionClaude)
	if err == nil {
		t.Fatal("BeginAttach() succeeded despite an uncreatable control-plane dir")
	}
	if !errors.Is(err, ErrBookkeepingUnavailable) {
		t.Errorf("BeginAttach() error = %v, want it to wrap ErrBookkeepingUnavailable", err)
	}
}

// TestLockAttachBookkeepingUnavailableOnOpenFailure — the other half of
// Important 4(b): the directory exists (or MkdirAll no-ops on it) but the
// lock file itself cannot be opened.
func TestLockAttachBookkeepingUnavailableOnOpenFailure(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// dir=blocker: filepath.Join(blocker, "attach.lock") tries to open a
	// path through a regular file, which os.OpenFile cannot do.
	_, err := lockAttach(context.Background(), blocker, time.Second)
	if err == nil {
		t.Fatal("lockAttach() succeeded despite an unopenable lock file")
	}
	if !errors.Is(err, ErrBookkeepingUnavailable) {
		t.Errorf("lockAttach() error = %v, want it to wrap ErrBookkeepingUnavailable", err)
	}
}

// TestCloseLeavesTheRecordWhenDetachFails — the startup sweep is the only
// backstop for a client tmux still lists; deleting the record on a failed
// detach would hide that from it. Regression test for finding 1: Close used
// to run os.Remove unconditionally regardless of whether DetachClient
// succeeded. Uses a genuine (non-gone) failure text — "no server running" is
// now recognized as ErrClientGone (Important 1) and is covered instead by
// TestCloseTreatsAGoneClientAsDetached, where Close succeeds and the record
// is removed.
func TestCloseLeavesTheRecordWhenDetachFails(t *testing.T) {
	dir := t.TempDir()
	list := clientScript("", "/dev/pts/4\n")
	f := &fakeExec{reply: func(n int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "detach-client" {
			return "permission denied", 1, nil
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
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("Close() error = %q, want it to contain tmux's own message", err.Error())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("record deleted despite a failed detach: %v", err)
	}
}

// TestCloseTreatsAGoneClientAsDetached — Important 1. When `claude` exits
// normally, tmux tears its session and client down before `container exec`
// returns, so Close's detach-client exec always finds them gone. That must
// be a successful detach — the record deleted, Close returning nil — not a
// spurious warning and a record the sweep has to reap later.
func TestCloseTreatsAGoneClientAsDetached(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"no server running", "no server running on /tmp/tmux-1000/default"},
		{"can't find client", "can't find client /dev/pts/9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			list := clientScript("", "/dev/pts/9\n")
			f := &fakeExec{reply: func(n int, cmdline []string) (string, int, error) {
				if len(cmdline) > 1 && cmdline[1] == "detach-client" {
					return tc.out, 1, nil
				}
				return list(n, cmdline)
			}}
			tm := testTmux(f)

			att, err := BeginAttach(context.Background(), tm, "cspace-demo-mercury", dir, SessionClaude)
			if err != nil {
				t.Fatalf("BeginAttach() error: %v", err)
			}
			waitTracked(t, att)

			path := filepath.Join(dir, recordName(SessionClaude, "/dev/pts/9"))
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("record not written before Close: %v", err)
			}

			if err := att.Close(context.Background()); err != nil {
				t.Fatalf("Close() error = %v, want nil for an already-gone client", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("record survived Close() for an already-gone client: %v", err)
			}
		})
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

func TestSweepClientRecords(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "mercury")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("cspace-claude.dev-pts-1.json",
		`{"session":"cspace-claude","tty":"/dev/pts/1","pid":101,"at":"2026-09-18T00:00:00Z"}`)
	write("cspace-shell.dev-pts-2.json",
		`{"session":"cspace-shell","tty":"/dev/pts/2","pid":102,"at":"2026-09-18T00:00:00Z"}`)
	write("cspace-claude.dev-pts-3.json",
		`{"session":"cspace-claude","tty":"/dev/pts/3","pid":103,"at":"2026-09-18T00:00:00Z"}`)
	write("garbage.json", `not json at all`)
	// The lock is not a record and must survive.
	write("attach.lock", "")

	// fakeExec, testTmux and fakeContainers all already exist in this
	// package — tmux_test.go and snapshot_test.go — and are reused rather
	// than shadowed. testTmux goes through NewTmux, so the driver's
	// memoization map is initialized; a composite `&Tmux{…}` leaves it nil,
	// which is safe only for as long as nothing on this path calls Present.
	f := &fakeExec{reply: func(_ int, cmdline []string) (string, int, error) {
		if len(cmdline) > 1 && cmdline[1] == "list-clients" {
			if cmdline[3] == "cspace-claude" {
				return "/dev/pts/1\n/dev/pts/3\n", 0, nil
			}
			return "", 0, nil
		}
		return "", 0, nil
	}}
	c := New(Options{
		Home: home,
		Containers: &fakeContainers{out: []applecontainer.ContainerSummary{
			{Name: "cspace-demo-mercury", State: "running"},
		}},
		Tmux: testTmux(f),
		// 101 is dead, 102 is dead, 103 is the process that is still running
		// this very attach.
		ProcessAlive: func(pid int) bool { return pid == 103 },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	// Two dead ttys in one pass, and both are handled: pts/1 is still listed
	// so it is detached, pts/2 is not so its record is just deleted.
	if res.Detached != 1 {
		t.Errorf("detached = %d, want 1", res.Detached)
	}
	if res.Deleted != 3 { // pts/1, pts/2 and the unparseable file
		t.Errorf("deleted = %d, want 3", res.Deleted)
	}
	if res.Kept != 1 {
		t.Errorf("kept = %d, want 1 — the live attach's record", res.Kept)
	}
	if res.Errors != 0 {
		t.Errorf("errors = %d, want 0 — nothing in this sweep failed", res.Errors)
	}
	var detached []string
	for _, call := range f.recorded() {
		if len(call) > 3 && call[1] == "detach-client" {
			detached = append(detached, call[3])
		}
	}
	if len(detached) != 1 || detached[0] != "/dev/pts/1" {
		t.Errorf("detached ttys = %v, want [/dev/pts/1]", detached)
	}
	// The live attach's record survives, and so does the lock.
	for _, keep := range []string{"cspace-claude.dev-pts-3.json", "attach.lock"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was removed: %v", keep, err)
		}
	}
}

func TestSweepReapsRecordsWhoseContainerIsGone(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "ghost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cspace-claude.dev-pts-9.json"),
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/9","pid":999}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f := &fakeExec{}
	c := New(Options{
		Home:         home,
		Containers:   &fakeContainers{}, // List returns nothing: the sandbox is gone
		Tmux:         testTmux(f),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", res.Deleted)
	}
	for _, call := range f.recorded() {
		if len(call) > 1 && call[1] == "detach-client" {
			t.Error("the sweep tried to detach inside a container that is gone")
		}
	}
}

// A sweep that cannot tell which containers exist must not read that as
// "none of them do", and a `list-clients` it could not run must not be read
// as "tmux says nothing is attached". Both are the same mistake — taking the
// absence of an answer for an answer — and both cost the same thing: the
// records of every live client on the machine, after which nothing can ever
// reap them, because the record is the only handle the next sweep has.
//
// Walk it through. `container ls` fails, so liveContainers reports
// known=false and sweepDir is told containerKnownGone=false: the delete
// branch for a container that is gone is off the table. The record's pid is
// dead, so the sweep asks tmux — and that exec fails too, which is what an
// unreachable container does. ListClients returns an error only for a failed
// exec (tmux answering "no such session" is a nil error and an empty list),
// so the listing is not `answered` and the record is kept: Kept 1, Errors 1,
// Deleted 0, and no detach attempted.
//
// It fails against the shape this replaced, which is the point of writing
// it. There, `!known` was folded into `containerLive = true`, and a listing
// that errored produced the same empty tty set as a listing that legitimately
// came back empty — so the tty was "not found", control fell past the detach
// block into an unconditional os.Remove, and both the first assertion
// (Deleted 0) and the last (the file is still there) failed.
func TestSweepLeavesRecordsAloneWhenItCannotListContainers(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "mercury")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "cspace-claude.dev-pts-1.json")
	if err := os.WriteFile(path,
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/1","pid":101}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A container the host cannot list is a container it cannot exec into
	// either, so the exec has to fail here too. A bare `&fakeExec{}` replies
	// ("", 0, nil), which is a *successful* empty listing — real evidence the
	// sweep is entitled to act on, and not this case at all.
	unreachable := &fakeExec{reply: func(_ int, _ []string) (string, int, error) {
		return "", 0, errors.New("container exec: connection refused")
	}}
	c := New(Options{
		Home:         home,
		Containers:   &fakeContainers{err: errors.New("container ls: connection refused")},
		Tmux:         testTmux(unreachable),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 0 {
		t.Errorf("deleted = %d, want 0 — a failed list is not evidence the container is gone", res.Deleted)
	}
	if res.Kept != 1 || res.Errors != 1 {
		t.Errorf("kept = %d, errors = %d, want 1 and 1 — kept, and counted as unfinished work", res.Kept, res.Errors)
	}
	for _, call := range unreachable.recorded() {
		if len(call) > 1 && call[1] == "detach-client" {
			t.Error("the sweep detached a tty it never saw listed")
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the record was removed: %v", err)
	}
}

// The other half of the same rule: a container list that failed must not
// freeze the sweep either. tmux is the authority on who is attached, so when
// the exec does reach it, its answer is acted on — here it answers that the
// session lists no clients, which means the dead attach's client is already
// detached and the record is nothing but litter.
func TestSweepActsOnTmuxEvidenceWhenTheContainerListFails(t *testing.T) {
	home := t.TempDir()
	dir := ControlPlaneDir(home, "demo", "venus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "cspace-claude.dev-pts-4.json")
	if err := os.WriteFile(path,
		[]byte(`{"session":"cspace-claude","tty":"/dev/pts/4","pid":104}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := New(Options{
		Home:       home,
		Containers: &fakeContainers{err: errors.New("container ls: connection refused")},
		// The default reply — exit 0, no output — is tmux answering that
		// the session has no clients.
		Tmux:         testTmux(&fakeExec{}),
		ProcessAlive: func(int) bool { return false },
	})

	res, err := c.SweepClientRecords(context.Background())
	if err != nil {
		t.Fatalf("SweepClientRecords: %v", err)
	}
	if res.Deleted != 1 || res.Kept != 0 || res.Errors != 0 {
		t.Errorf("deleted = %d, kept = %d, errors = %d, want 1, 0 and 0",
			res.Deleted, res.Kept, res.Errors)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the stale record survived a listing that said its client was gone")
	}
}
