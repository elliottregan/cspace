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

// sweepExecTimeout bounds each list-clients exec the sweep runs, inside the
// sweep's own overall context. Without its own bound, one sandbox whose
// container is wedged rather than cleanly unreachable (a hung `container
// exec`, not a fast connection-refused) could eat the whole sweep's budget
// and leave every sandbox after it unswept. A var so tests do not have to
// wait out the real value.
var sweepExecTimeout = 5 * time.Second

// sweepLockWait bounds how long the sweep waits for one sandbox's
// attach.lock before giving up on that directory for this pass. It is
// deliberately much shorter than attachLockWait: BeginAttach has nothing to
// fall back to and must wait out a legitimate concurrent attach's own
// discovery window, but the sweep does — a lock another cspace process
// holds means that process (an attach starting, or another sweep) owns the
// decision on this directory's records and will account for them itself, so
// the sweep can simply skip the directory this pass rather than block
// everything behind it. Waiting the full attachLockWait here would stall
// every startup sweep — the tui, `cspace up` — for up to 12s behind one
// sandbox mid-attach, for a directory the very next sweep will look at
// again anyway. A var so tests do not have to wait out the real value.
var sweepLockWait = 2 * time.Second

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
			return nil, fmt.Errorf("attach lock %q busy after %s: another cspace process is attaching to or sweeping this sandbox", path, wait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// SweepResult counts what one sweep did.
//
// Every record the sweep looked at is either kept or deleted, so
// Kept+Deleted is the number of records it saw. Detached counts the subset
// of the deleted whose tmux client had to be detached first — it is not a
// separate outcome, and adding it to Deleted double-counts.
//
// Errors is the count of unfinished work, and it has two sources: a record
// kept because something could not be asked rather than because the
// evidence said to keep it, and a directory that could not be listed at all
// (whose records were therefore never seen, and so appear in neither Kept
// nor Deleted). Either way a sweep with Errors > 0 has work left that the
// next sweep will have to redo, which is the only claim this field makes.
//
// A record skipped because its sandbox's attach.lock was held by another
// cspace process (an attach starting, or a concurrent sweep) counts as
// Kept, not Errors: that process's own Close or sweep owns the decision on
// those records, and nothing went wrong by leaving them to it.
type SweepResult struct {
	Kept     int // records left in place
	Detached int // clients tmux still listed, now detached (a subset of Deleted)
	Deleted  int // record files removed
	Errors   int // unfinished work: a record that could not be decided, or a directory that could not be read
}

// SweepClientRecords is step 4 of the design's detach protocol: for every
// client record under ~/.cspace/controlplane/ whose host process is gone,
// detach the client tmux still lists and delete the record.
//
// It is the backstop for the two ways a client outlives its owner: the
// control plane or a `cspace attach` crashing before its own Close ran, and
// a host terminal closing hard. Run it once at startup, before any pane
// opens — a stale record is inert, but the tmux client it names is not, and
// a sandbox accumulating attached clients is one whose next attach shares a
// screen with a ghost.
//
// Every record is processed in one pass, including several in one sandbox's
// directory: a crash strands one record per pane that was open, and reaping
// only the first would need as many restarts as there were panes.
//
// **A record is deleted only on evidence, and every delete is its own
// branch naming the evidence it has:**
//
//   - the file does not parse — it names no client, so it can only be litter;
//   - the host pid is dead and an *authoritative* container list does not
//     have this container — there is no tmux server left to talk to, so the
//     record is deleted without an exec that would only fail slowly;
//   - the host pid is dead and tmux answered that it does not list this tty
//     — the client is already detached, so only the record is left;
//   - the host pid is dead, tmux listed the tty, and the detach succeeded
//     (or said the client was already gone).
//
// Everything else keeps the record: a live pid, a container list that could
// not be believed (liveContainers' second return), a `list-clients` exec
// that failed, and a detach that failed. The asymmetry is the whole design.
// A record kept one sweep too long costs one exec next time; a record
// deleted without evidence throws away the only handle any later sweep has
// on a client that may still be attached, and nothing can reap it after
// that. Absence of an answer is never an answer.
func (c *Client) SweepClientRecords(ctx context.Context) (SweepResult, error) {
	var res SweepResult
	if c.home == "" {
		return res, ErrNoHome
	}
	root := controlPlaneRoot(c.home)

	projects, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil // nothing has ever attached
		}
		return res, fmt.Errorf("read %s: %w", root, err)
	}

	live, known := c.liveContainers(ctx)
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		sandboxes, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			// Counted, not swallowed: whatever records are under here were
			// not looked at, so this sweep left work behind — which is
			// exactly what SweepResult.Errors means.
			res.Errors++
			continue
		}
		for _, sandbox := range sandboxes {
			if !sandbox.IsDir() {
				continue
			}
			dir := filepath.Join(root, project.Name(), sandbox.Name())
			container := containerName(project.Name(), sandbox.Name())
			// "Known gone" needs both halves: a list that can be believed
			// *and* this container missing from it. A list that failed says
			// nothing at all, and the sweep falls through to asking tmux —
			// which for a container that really is gone costs one failed
			// exec per session and keeps the records for the next sweep.
			c.sweepDir(ctx, dir, container, known && !live[container], &res)
		}
	}
	return res, nil
}

// sweepDir handles one sandbox's records. containerKnownGone is true only
// when an authoritative container list did not mention this container; a
// list that could not be taken arrives here as false, because "gone" and
// "unanswered" authorise different things.
//
// The whole directory is processed under the sandbox's own attach.lock — the
// same file BeginAttach holds while it is discovering its own client's tty.
// Without it, a sweep that read a stale record for a tty a concurrent attach
// is about to reuse could see that attach's own brand-new client show up in
// list-clients, read it as the dead record's client still being attached,
// and detach it — deleting the fresh record that names it in the same
// stroke. Held for the whole directory, the two can never interleave:
// BeginAttach cannot start its own discovery window until the sweep
// releases the lock, and vice versa.
//
// Unlike BeginAttach, the sweep only waits sweepLockWait (short) for it, not
// attachLockWait (up to 12s): a busy lock here means another cspace process
// — an attach starting, or another sweep — is already handling this exact
// directory, so waiting the long way out would only stall this pass behind
// work that is already being done, for a directory the very next sweep will
// look at again regardless. A busy lock therefore skips the directory
// without logging anything and counts its records as Kept (SweepResult's
// own doc), using a best-effort, unlocked count — not Errors, since nothing
// here went wrong. Only a lock that could not be opened at all
// (ErrBookkeepingUnavailable — a local filesystem problem, not contention)
// is genuinely unfinished work.
func (c *Client) sweepDir(ctx context.Context, dir, container string, containerKnownGone bool, res *SweepResult) {
	lock, lockErr := lockAttach(ctx, dir, sweepLockWait)
	if lockErr != nil {
		if errors.Is(lockErr, ErrBookkeepingUnavailable) {
			res.Errors++
			return
		}
		// Busy, not broken: leave this directory's records for whichever
		// cspace process holds the lock — its own Close or sweep will
		// account for them.
		res.Kept += countRecords(dir)
		return
	}
	defer func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}()

	entries, err := os.ReadDir(dir)
	if err != nil {
		// Same rule as the per-project read above: a directory that could
		// not be listed is work this sweep did not do.
		res.Errors++
		return
	}

	// One list-clients per session, not per record: a sandbox with a Claude
	// pane and a shell pane has two sessions and any number of records.
	//
	// `answered` is the second half of the evidence, and it is NOT simply
	// "the exec returned no error". This package's Execer contract treats a
	// `container exec` against a missing, stopped or otherwise unreachable
	// container as an ordinary non-zero exit with a nil error (tmux.go's own
	// doc), and ListClients' public contract folds every non-zero exit —
	// whatever caused it — into "no clients, no error", because every other
	// caller is fine treating "no session yet" and "container unreachable"
	// alike. That fold is exactly wrong here: both produce the identical
	// empty tty set, but only one of them is evidence this sweep may delete
	// a record on. listClients (unexported, this sweep's only caller) keeps
	// them apart by reading a failed exec's own output the same way
	// DetachClient already does: one of tmux's own gone-client/gone-session
	// markers (clientAlreadyGone) means tmux itself answered; any other
	// non-zero exit — the `container` CLI's own "not found"/"not running"
	// text, a refused connection, a daemon mid-restart — means the exec
	// never reached tmux at all.
	type listing struct {
		ttys     map[string]bool
		answered bool
	}
	listed := map[string]listing{}
	clientsFor := func(session string) listing {
		if got, ok := listed[session]; ok {
			return got
		}
		got := listing{ttys: map[string]bool{}}
		execCtx, cancel := context.WithTimeout(ctx, sweepExecTimeout)
		ttys, answered, err := c.tmux.listClients(execCtx, container, session)
		cancel()
		if err == nil {
			got.answered = answered
			for _, tty := range ttys {
				got.ttys[tty] = true
			}
		}
		listed[session] = got
		return got
	}

	// remove is the only place a record file is unlinked, so every delete in
	// this function runs through one of the evidence branches below. A
	// target already gone (fs.ErrNotExist — its owner's own Close, or an
	// earlier sweep, got there first) is success, not failure, matching
	// Attachment.Close's own rule. It reports whether the delete counted, so
	// a caller that also wants to count Detached does so only once the
	// record is actually gone — Detached is documented as a subset of
	// Deleted, and counting it before a failed unlink could make that false.
	remove := func(path string) bool {
		if err := os.Remove(path); err == nil || errors.Is(err, fs.ErrNotExist) {
			res.Deleted++
			return true
		}
		res.Kept++
		res.Errors++
		return false
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue // attach.lock, and anything else that is not a record
		}
		path := filepath.Join(dir, name)

		rec, err := readClientRecord(path)
		if err != nil {
			// Evidence: the file names no client, so there is nothing it
			// could be a handle on. Litter.
			remove(path)
			continue
		}
		if c.processAlive(rec.PID) {
			// Evidence: the process that owns this client is running. Its
			// own Close detaches and deletes; the sweep must not race it.
			res.Kept++
			continue
		}
		if containerKnownGone {
			// Evidence: the container is not there, so neither is the tmux
			// server. Delete without an exec.
			remove(path)
			continue
		}

		clients := clientsFor(rec.Session)
		if !clients.answered {
			// No evidence either way: the exec failed, or reached a
			// container that could not answer as tmux. This is the branch
			// the whole shape of the function exists for — deleting here is
			// what would wipe live clients' records on one bad `container
			// ls` plus one unreachable container.
			res.Kept++
			res.Errors++
			continue
		}
		if !clients.ttys[rec.TTY] {
			// Evidence: tmux answered and does not list this tty, so the
			// client is already detached and only the record is left.
			remove(path)
			continue
		}

		// Belt and braces: the directory lock keeps a concurrent BeginAttach
		// from writing a fresh record over this exact path while the sweep
		// is deciding, but not from the pid itself becoming live again in
		// the time since the check above — a respawn, or simply the time
		// this directory's other records took to process. Re-reading the
		// record and re-checking its pid immediately before the detach is
		// the last chance to notice and back off before ending a client
		// this record no longer accurately describes.
		fresh, ferr := readClientRecord(path)
		if ferr != nil {
			if errors.Is(ferr, fs.ErrNotExist) {
				// Already gone — its own owner's Close (or a sibling sweep)
				// beat us to it between the listing above and now. Nothing
				// left to detach or delete, but this is still a record the
				// sweep looked at and resolved, so Kept+Deleted stays the
				// count of records seen.
				res.Deleted++
			} else {
				// Exists but no longer parses — changed shape since the
				// listing above, most plausibly a partial write. Leave it
				// for the next sweep rather than guess at it.
				res.Kept++
				res.Errors++
			}
			continue
		}
		if c.processAlive(fresh.PID) {
			res.Kept++
			continue
		}
		if fresh.TTY != rec.TTY || fresh.Session != rec.Session {
			// The record at this path no longer names the client the
			// list-clients evidence above was gathered for — it was
			// replaced by an unrelated record in the window between that
			// listing and now. Detaching fresh.TTY here would act on tty
			// evidence tmux was never actually asked about; leave it for the
			// next sweep, which will judge the replacement on its own fresh
			// evidence.
			res.Kept++
			res.Errors++
			continue
		}

		detachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachTimeout)
		detachErr := c.tmux.DetachClient(detachCtx, container, fresh.TTY)
		cancel()
		if detachErr != nil && !errors.Is(detachErr, ErrClientGone) {
			// Leave the record: the next sweep is the retry, and deleting
			// it would hide a client that is still attached. ErrClientGone
			// is not a failure — it means the detach had already happened.
			res.Kept++
			res.Errors++
			continue
		}
		// Evidence: the client was listed and is now detached.
		if remove(path) {
			res.Detached++
		}
	}
}

// countRecords is the sweep's best-effort, unlocked tally of a sandbox
// directory's record files, used only when that directory's attach.lock is
// held by another cspace process and its records cannot safely be decided
// this pass. It exists to keep Kept an honest count of records the sweep
// saw, not to detect a problem — a directory that cannot even be listed this
// way counts as 0 rather than an error, since the point of skipping it was
// precisely to not treat someone else's in-progress work as this sweep's own
// unfinished business.
func countRecords(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			n++
		}
	}
	return n
}

// readClientRecord parses one record file.
func readClientRecord(path string) (ClientRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClientRecord{}, err
	}
	var rec ClientRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return ClientRecord{}, err
	}
	if rec.TTY == "" || rec.Session == "" || rec.PID <= 0 {
		return ClientRecord{}, errors.New("control: incomplete client record")
	}
	return rec, nil
}

// liveContainers is the set of container names the substrate currently has,
// running or not — and whether that set can be believed.
//
// The second return is the whole point of the function's shape. An empty map
// and an unanswerable question look identical, and a caller that reads "not
// in the map" as "the container is gone, delete the record without even
// trying to detach" would, on a substrate with no ContainerCLI or one
// transient `container ls` failure at `cspace tui` startup, delete every
// client record under ~/.cspace/controlplane/ — including the ones naming
// clients that really are attached, after which nothing can ever reap them,
// because the record is the only handle the next sweep has. false means
// "ask again next time".
func (c *Client) liveContainers(ctx context.Context) (map[string]bool, bool) {
	out := map[string]bool{}
	if c.containers == nil {
		return out, false
	}
	list, err := c.containers.List(ctx)
	if err != nil {
		return out, false
	}
	for _, ct := range list {
		out[ct.Name] = true
	}
	return out, true
}
