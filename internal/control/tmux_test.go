package control

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeExec is the substrate stand-in for every test in this package: it
// records each command it was asked to run and replies from a script keyed by
// call index. Shared with attach_test.go.
type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	reply func(n int, cmdline []string) (string, int, error)
}

func (f *fakeExec) Exec(_ context.Context, _ string, cmdline []string) (string, int, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, append([]string(nil), cmdline...))
	reply := f.reply
	f.mu.Unlock()
	if reply == nil {
		return "", 0, nil
	}
	return reply(n, cmdline)
}

func (f *fakeExec) recorded() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// testTmux builds a Tmux whose polling is fast enough for a unit test.
func testTmux(f *fakeExec) *Tmux {
	tm := NewTmux()
	tm.Exec = f
	tm.PollEvery = time.Millisecond
	tm.PollFor = 2 * time.Second
	return tm
}

// wantProbeCmd is the exact probe argv Present sends: always 0 exit, always
// exactly "yes" or "no" on stdout, so presence never has to be read off the
// exit code or off the `container` CLI's own error wording.
var wantProbeCmd = []string{"sh", "-c", "command -v tmux >/dev/null 2>&1 && echo yes || echo no"}

// TestPresentProbesOnceAndMemoizes — an image cannot grow tmux while its
// container runs, so the probe is worth exactly one exec per sandbox. Every
// attach would otherwise pay for it.
func TestPresentProbesOnceAndMemoizes(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "yes\n", 0, nil
	}}
	tm := testTmux(f)

	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err != nil || !ok {
		t.Fatalf("Present() = (%v, %v), want (true, nil) for a probe that prints yes", ok, err)
	}
	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err != nil || !ok {
		t.Fatalf("memoized Present() = (%v, %v), want (true, nil)", ok, err)
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("probed %d times, want 1: %v", len(calls), calls)
	}
	if strings.Join(calls[0], " ") != strings.Join(wantProbeCmd, " ") {
		t.Errorf("probe = %v, want %v", calls[0], wantProbeCmd)
	}
}

// TestPresentCachesNoAnswer — an image built before cspace shipped tmux
// prints "no", not just any non-zero exit; the caller falls back to the
// direct exec and warns, and it is worth remembering just as much as "yes"
// is — the probe still runs exactly once.
func TestPresentCachesNoAnswer(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "no\n", 0, nil
	}}
	tm := testTmux(f)

	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err != nil || ok {
		t.Fatalf("Present() = (%v, %v), want (false, nil) when the probe prints no", ok, err)
	}
	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err != nil || ok {
		t.Fatalf("memoized Present() = (%v, %v), want (false, nil)", ok, err)
	}
	if got := len(f.recorded()); got != 1 {
		t.Fatalf("probed %d times, want 1: %v", got, f.recorded())
	}
}

// TestPresentMemoizesPerContainer — two sandboxes can run different images.
func TestPresentMemoizesPerContainer(t *testing.T) {
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return "yes\n", 0, nil
		}
		return "no\n", 0, nil
	}}
	tm := testTmux(f)
	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err != nil || !ok {
		t.Errorf("first container: Present() = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := tm.Present(context.Background(), "cspace-demo-venus"); err != nil || ok {
		t.Errorf("second container: Present() = (%v, %v), want its own probe to decide (false, nil)", ok, err)
	}
}

// TestPresentTransportErrorIsNotCached — a transport failure (the `container`
// CLI missing, the context cancelled: the command could not even be
// started) is not tmux's own answer and must not be remembered as "no
// tmux": that would wrongly strand every later attach to this container on
// the no-tmux fallback even once the transport recovers.
func TestPresentTransportErrorIsNotCached(t *testing.T) {
	wantErr := errors.New("boom: transport down")
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return "", -1, wantErr
		}
		return "yes\n", 0, nil
	}}
	tm := testTmux(f)

	if _, err := tm.Present(context.Background(), "cspace-demo-mercury"); !errors.Is(err, wantErr) {
		t.Fatalf("Present() error = %v, want %v", err, wantErr)
	}
	ok, err := tm.Present(context.Background(), "cspace-demo-mercury")
	if err != nil {
		t.Fatalf("Present() second call error = %v, want nil now that the probe succeeded", err)
	}
	if !ok {
		t.Error("Present() second call = false, want true")
	}
	if got := len(f.recorded()); got != 2 {
		t.Fatalf("probed %d times, want 2 — a transport error must not be cached", got)
	}
}

// TestPresentUnreachableContainerErrorsAndIsNotCached — finding from Task 9's
// review round 1: a stopped or removed container makes `container exec`
// itself fail before it ever reaches the guest shell, which CLIExecer
// reports as an ordinary non-zero exit carrying the CLI's own message (e.g.
// "Error: container … is not running") in the combined output, not as an
// Execer-level error. That text is not "no" — Present must fail the probe
// with an error rather than silently read an unrecognized answer as "no
// tmux", and must not cache a failed probe, so a later call (once the
// container is reachable again) gets to decide for real.
func TestPresentUnreachableContainerErrorsAndIsNotCached(t *testing.T) {
	const cliMessage = "Error: container cspace-demo-mercury is not running"
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return cliMessage, 1, nil
		}
		return "yes\n", 0, nil
	}}
	tm := testTmux(f)

	ok, err := tm.Present(context.Background(), "cspace-demo-mercury")
	if err == nil {
		t.Fatal("Present() error = nil, want the unreachable-container probe failure surfaced")
	}
	if ok {
		t.Error("Present() ok = true, want false alongside the error")
	}
	if !strings.Contains(err.Error(), cliMessage) || !strings.Contains(err.Error(), "exit 1") {
		t.Errorf("error = %q, want it to name the exit code and include the CLI's own message", err.Error())
	}

	ok, err = tm.Present(context.Background(), "cspace-demo-mercury")
	if err != nil || !ok {
		t.Fatalf("Present() second call = (%v, %v), want (true, nil) now that the probe answers yes", ok, err)
	}
	if got := len(f.recorded()); got != 2 {
		t.Fatalf("probed %d times, want 2 — a failed probe must not be cached", got)
	}
}

// TestPresentUnexpectedOutputErrorsAndIsNotCached — an exit-0 probe that
// prints neither "yes" nor "no" (empty output, truncated output, …) is not
// a decided answer either; Present must fail rather than guess, and must
// not cache the failure.
func TestPresentUnexpectedOutputErrorsAndIsNotCached(t *testing.T) {
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return "", 0, nil
		}
		return "yes\n", 0, nil
	}}
	tm := testTmux(f)

	if ok, err := tm.Present(context.Background(), "cspace-demo-mercury"); err == nil || ok {
		t.Fatalf("Present() = (%v, %v), want (false, non-nil) for an exit-0 probe with unexpected output", ok, err)
	}

	ok, err := tm.Present(context.Background(), "cspace-demo-mercury")
	if err != nil || !ok {
		t.Fatalf("Present() second call = (%v, %v), want (true, nil) now that the probe answers yes", ok, err)
	}
	if got := len(f.recorded()); got != 2 {
		t.Fatalf("probed %d times, want 2 — a failed probe must not be cached", got)
	}
}

func TestListClients(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "/dev/pts/1\n/dev/pts/2\n\n", 0, nil
	}}
	got, err := testTmux(f).ListClients(context.Background(), "cspace-demo-mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients() error: %v", err)
	}
	want := []string{"/dev/pts/1", "/dev/pts/2"}
	if len(got) != len(want) {
		t.Fatalf("clients = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("client[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	wantCmd := []string{"tmux", "list-clients", "-t", SessionClaude, "-F", "#{client_tty}"}
	if strings.Join(f.recorded()[0], " ") != strings.Join(wantCmd, " ") {
		t.Errorf("command = %v, want %v", f.recorded()[0], wantCmd)
	}
}

// TestListClientsOnNoServer — before the first attach there is no tmux server
// and no session, and tmux exits non-zero saying so. "No clients" is the
// honest answer, not an error: the snapshot before the first attach must
// succeed or nothing can ever be tracked.
func TestListClientsOnNoServer(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil
	}}
	got, err := testTmux(f).ListClients(context.Background(), "cspace-demo-mercury", SessionClaude)
	if err != nil {
		t.Fatalf("ListClients() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("clients = %v, want none", got)
	}
}

// TestDetachClient — this is the call that actually reaps a guest-side
// client. Killing the host-side `container exec` never reaches tmux.
func TestDetachClient(t *testing.T) {
	f := &fakeExec{}
	if err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/2"); err != nil {
		t.Fatalf("DetachClient() error: %v", err)
	}
	want := []string{"tmux", "detach-client", "-t", "/dev/pts/2"}
	if strings.Join(f.recorded()[0], " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", f.recorded()[0], want)
	}
}

func TestDetachClientReportsFailure(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil
	}}
	if err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/9"); err == nil {
		t.Error("DetachClient() returned nil for a non-zero tmux exit")
	}
}

// TestDetachClientErrorIncludesTmuxOutput — the whole point of surfacing a
// non-zero exit is to say why. A caller staring at "exit 1" with no further
// text cannot tell what tmux actually objected to. Uses a genuine (non-gone)
// failure text — "can't find client" is now recognized as ErrClientGone, so
// it no longer exercises this path (see TestDetachClientRecognizesAGoneClient).
func TestDetachClientErrorIncludesTmuxOutput(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "permission denied", 1, nil
	}}
	err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/9")
	if err == nil {
		t.Fatal("DetachClient() returned nil for a non-zero tmux exit")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %q, want it to contain tmux's own message", err.Error())
	}
	if errors.Is(err, ErrClientGone) {
		t.Error("error wraps ErrClientGone for a genuine failure, not an already-gone client")
	}
}

// TestDetachClientRecognizesAGoneClient — finding: Important 1. When
// `claude` exits normally, tmux tears its session and client down before
// `container exec` returns, so DetachClient's own exec always finds them
// gone. That is success, not a failure a caller should warn about.
func TestDetachClientRecognizesAGoneClient(t *testing.T) {
	cases := []string{
		"no server running on /tmp/tmux-1000/default",
		"can't find client /dev/pts/9",
		"can't find session: cspace-claude",
		"no such session: cspace-claude",
	}
	for _, out := range cases {
		out := out
		t.Run(out, func(t *testing.T) {
			f := &fakeExec{reply: func(int, []string) (string, int, error) {
				return out, 1, nil
			}}
			err := testTmux(f).DetachClient(context.Background(), "cspace-demo-mercury", "/dev/pts/9")
			if !errors.Is(err, ErrClientGone) {
				t.Errorf("DetachClient() error = %v, want it to wrap ErrClientGone for tmux output %q", err, out)
			}
		})
	}
}
