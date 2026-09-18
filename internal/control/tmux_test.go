package control

import (
	"context"
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

// TestPresentProbesOnceAndMemoizes — an image cannot grow tmux while its
// container runs, so the probe is worth exactly one exec per sandbox. Every
// attach would otherwise pay for it.
func TestPresentProbesOnceAndMemoizes(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "/usr/bin/tmux\n", 0, nil
	}}
	tm := testTmux(f)

	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Fatal("Present() = false for a container whose probe exits 0")
	}
	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Fatal("memoized Present() = false")
	}
	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("probed %d times, want 1: %v", len(calls), calls)
	}
	want := []string{"sh", "-c", "command -v tmux"}
	if strings.Join(calls[0], " ") != strings.Join(want, " ") {
		t.Errorf("probe = %v, want %v", calls[0], want)
	}
}

// TestPresentFalseOnMissingBinary — an image built before cspace shipped
// tmux. The caller falls back to the direct exec and warns.
func TestPresentFalseOnMissingBinary(t *testing.T) {
	f := &fakeExec{reply: func(int, []string) (string, int, error) {
		return "", 1, nil // `command -v tmux` found nothing
	}}
	if testTmux(f).Present(context.Background(), "cspace-demo-mercury") {
		t.Error("Present() = true when the probe exits non-zero")
	}
}

// TestPresentMemoizesPerContainer — two sandboxes can run different images.
func TestPresentMemoizesPerContainer(t *testing.T) {
	f := &fakeExec{reply: func(n int, _ []string) (string, int, error) {
		if n == 0 {
			return "/usr/bin/tmux\n", 0, nil
		}
		return "", 1, nil
	}}
	tm := testTmux(f)
	if !tm.Present(context.Background(), "cspace-demo-mercury") {
		t.Error("first container: Present() = false")
	}
	if tm.Present(context.Background(), "cspace-demo-venus") {
		t.Error("second container: Present() = true, want its own probe to decide")
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
