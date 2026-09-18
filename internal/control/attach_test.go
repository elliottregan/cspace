package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
