package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// attachLockTimeout overrides attachLockWait's derived value when non-zero.
// Production leaves it at its zero value; a test that needs a short wait
// independent of PollFor can set it directly rather than waiting out a
// PollFor-derived window.
var attachLockTimeout time.Duration

// attachLockWait bounds how long BeginAttach waits for another process's
// attach window. It must exceed tm.PollFor: PollFor is how long a
// legitimate attach holds the lock while it tracks down its own client (see
// track), so a wait shorter than PollFor would fail a second attach against
// a first one that is still succeeding, not stuck. The 2s margin covers the
// gap between the poll loop's last iteration and the lock actually clearing.
func attachLockWait(tm *Tmux) time.Duration {
	if attachLockTimeout > 0 {
		return attachLockTimeout
	}
	return tm.PollFor + 2*time.Second
}

// detachTimeout bounds the detach-client exec that Close runs, independent
// of the caller's context. Close derives its own bounded context
// (context.WithoutCancel(ctx) plus this timeout) rather than trusting the
// caller's, because the caller's context being done — a dead host side, a
// SIGHUP — is exactly the situation the detach exists to recover from; it
// must not be skipped on the one path that needs it most. A var so tests do
// not have to wait out the real value.
var detachTimeout = 5 * time.Second

// ClientRecord is the on-disk record of one live tmux client, written to
// <ControlPlaneDir>/<session>.<tty>.json.
//
// PID is the host-side process that owns the client. The control plane's
// startup sweep (rollout step 4) uses it: a record whose pid is gone but
// whose tty tmux still lists is a client that crashed without detaching, and
// gets detached and deleted then. Nothing here depends on that sweep — a
// stale record is an inert file, never a lock.
type ClientRecord struct {
	Session string `json:"session"`
	TTY     string `json:"tty"`
	PID     int    `json:"pid"`
	At      string `json:"at"`
}

// ErrBookkeepingUnavailable marks a BeginAttach failure that has nothing to
// do with another attach being in progress: the control-plane directory or
// its lock file could not be created or opened at all — a permissions
// problem, a full disk, a missing parent. Callers treat it as non-fatal:
// warn once and proceed with an inert attachment (BeginAttach's own
// empty-session path returns one) rather than refuse an attach over
// bookkeeping that exists only to make a graceful detach possible. A busy
// lock (another attach's window still open) is a different failure — it is
// not wrapped in this and stays a hard error, since guessing the wrong tty
// out from under a concurrent attach is the exact bug the lock prevents.
var ErrBookkeepingUnavailable = errors.New("attach bookkeeping unavailable")

// recordName turns a session and a tty into a file name:
// (cspace-claude, /dev/pts/12) -> cspace-claude.dev-pts-12.json
func recordName(session, tty string) string {
	return session + "." + strings.ReplaceAll(strings.Trim(tty, "/"), "/", "-") + ".json"
}

// Attachment is one live attach into a sandbox's tmux session: the client it
// created, the record file naming it, and the detach that ends it.
type Attachment struct {
	tmux      *Tmux
	container string
	dir       string
	session   string

	lock      *os.File
	lockOnce  sync.Once
	trackStop context.CancelFunc
	trackDone chan struct{}

	mu     sync.Mutex
	tty    string
	closed bool
}

// BeginAttach takes the sandbox's attach lock, snapshots the session's
// current clients, and starts tracking this attach's own client in the
// background. Call it immediately before starting the attaching
// `container exec`, and Close when that child ends.
//
// The lock is a file under ~/.cspace/controlplane/<project>/<sandbox>/
// because the processes that attach to one session are genuinely separate:
// the control plane, and any number of hand-started `cspace attach`es. It is
// flock rather than a pid file so the kernel drops it when its holder dies —
// a crashed attach must not wedge the next one.
//
// An empty session (the no-tmux fallback) yields an inert Attachment: nothing
// is locked, written or detached, and Close succeeds. Callers never nil-check.
func BeginAttach(ctx context.Context, tm *Tmux, container, dir, session string) (*Attachment, error) {
	a := &Attachment{
		tmux:      tm,
		container: container,
		dir:       dir,
		session:   session,
		trackDone: make(chan struct{}),
	}
	if session == "" {
		close(a.trackDone)
		return a, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create control-plane dir %q: %w: %w", dir, ErrBookkeepingUnavailable, err)
	}
	lock, err := lockAttach(ctx, dir, attachLockWait(tm))
	if err != nil {
		return nil, err
	}
	a.lock = lock

	before, err := tm.ListClients(ctx, container, session)
	if err != nil {
		a.releaseLock()
		return nil, fmt.Errorf("snapshot tmux clients for %s: %w", container, err)
	}

	// Tracking outlives the caller's ctx on purpose: the attach child runs
	// for as long as the user keeps the window, and cancelling the snapshot
	// context must not stop us identifying the client we have to detach.
	trackCtx, stop := context.WithTimeout(context.Background(), tm.PollFor)
	a.trackStop = stop
	go a.track(trackCtx, before)
	return a, nil
}

// TTY reports this attach's client tty, or "" while it is still unknown.
func (a *Attachment) TTY() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tty
}

// track waits for this attach's client to appear, records it, and releases
// the lock. tmux does not tell a client its own tty and the host-side
// `container exec` cannot ask, so the one tty that was not in the snapshot is
// the answer — which only holds while no other attach can interleave, hence
// the lock held across this whole window.
//
// It always records what it finds, regardless of whether Close has already
// been called: `closed` exists purely to make Close idempotent, not to gate
// discovery. Close always waits on trackDone before reading the tty, so a
// discovery that races Close's start still gets recorded and detached —
// skipping it here would silently strand the client tmux actually has, with
// no record for the sweep to find later.
func (a *Attachment) track(ctx context.Context, before []string) {
	defer close(a.trackDone)
	defer a.releaseLock()
	defer a.trackStop()

	seen := make(map[string]bool, len(before))
	for _, tty := range before {
		seen[tty] = true
	}

	for {
		clients, err := a.tmux.ListClients(ctx, a.container, a.session)
		if err == nil {
			for _, tty := range clients {
				if seen[tty] {
					continue
				}
				a.mu.Lock()
				a.tty = tty
				a.mu.Unlock()
				a.writeRecord(tty)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(a.tmux.PollEvery):
		}
	}
}

// writeRecord publishes the client record atomically: the sweep may read this
// directory at any moment and must never see half a record.
func (a *Attachment) writeRecord(tty string) {
	data, err := json.Marshal(ClientRecord{
		Session: a.session,
		TTY:     tty,
		PID:     os.Getpid(),
		At:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	path := filepath.Join(a.dir, recordName(a.session, tty))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}

// Close detaches this attach's tmux client and deletes its record. It is
// idempotent and safe to call when no client was ever identified.
//
// This is the step that makes a closed window actually end the guest-side
// client: the host-side `container exec` dying never reaches tmux, which
// keeps the client attached indefinitely. The detach always runs even when
// ctx is already done — a dead caller context (the SIGHUP path) is exactly
// the case this exists to recover from, so Close derives its own bounded
// context rather than trusting the caller's. The record is deleted once the
// detach succeeds OR the client turns out to already be gone
// (errors.Is(err, ErrClientGone)) — the common case: when `claude` exits
// normally, tmux tears the session and client down before `container exec`
// even returns, so the detach this runs always finds them gone, and that is
// success, not a failure to warn about and a record to strand. On any other
// detach failure the record is left in place so the startup sweep (rollout
// step 4), which reaps a record whose tty tmux still lists, remains the
// backstop — deleting it here would hide from the sweep that a client is
// still attached.
func (a *Attachment) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	// Stop tracking and wait for it to finish, so the tty it may have just
	// found is visible below. Cancelling first bounds the wait: if the
	// client never appeared, the attach is over and it never will.
	if a.trackStop != nil {
		a.trackStop()
	}
	<-a.trackDone

	var err error
	if tty := a.TTY(); tty != "" {
		detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachTimeout)
		detachErr := a.tmux.DetachClient(detachCtx, a.container, tty)
		cancel()
		if detachErr == nil || errors.Is(detachErr, ErrClientGone) {
			if rmErr := os.Remove(filepath.Join(a.dir, recordName(a.session, tty))); rmErr != nil &&
				!errors.Is(rmErr, fs.ErrNotExist) {
				err = rmErr
			}
		} else {
			err = detachErr
		}
	}
	a.releaseLock()
	return err
}

func (a *Attachment) releaseLock() {
	a.lockOnce.Do(func() {
		if a.lock == nil {
			return
		}
		_ = syscall.Flock(int(a.lock.Fd()), syscall.LOCK_UN)
		_ = a.lock.Close()
	})
}

// lockAttach takes an exclusive flock on <dir>/attach.lock, retrying until
// the context is done or wait passes. Non-blocking + retry rather than a
// blocking LOCK_EX because a blocking flock cannot be cancelled, and a
// wedged peer must not hang the user's terminal forever.
//
// Failing to open the lock file at all (as opposed to failing to acquire it)
// wraps ErrBookkeepingUnavailable: that is a local filesystem problem, not
// contention with another attach, and callers downgrade it to a warning
// rather than refusing the attach.
func lockAttach(ctx context.Context, dir string, wait time.Duration) (*os.File, error) {
	path := filepath.Join(dir, "attach.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open attach lock %q: %w: %w", path, ErrBookkeepingUnavailable, err)
	}
	deadline := time.Now().Add(wait)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return f, nil
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("attach lock %q busy after %s: another attach to this sandbox is starting", path, wait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
