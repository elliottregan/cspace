package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Execer runs a one-shot command inside a sandbox and reports what it printed
// and how it exited. It is the only thing the tmux plumbing needs from the
// substrate, and it is an interface so the plumbing can be tested without
// Apple Container.
//
// A non-zero exit status is NOT an error: tmux uses it to say ordinary things
// like "no server running". Only a transport failure (the CLI missing, the
// context cancelled) returns err.
//
// The returned string is the command's stdout. When the command exits
// non-zero, its stderr (trimmed) is appended after stdout, separated by a
// newline when both are non-empty, so a caller that wants to know why a
// command failed (DetachClient) can read it from there. This is safe for
// every caller in this package: ListClients only parses the string on exit
// 0, and Present ignores it entirely.
type Execer interface {
	Exec(ctx context.Context, container string, cmdline []string) (stdout string, exitCode int, err error)
}

// CLIExecer shells out to `container exec <container> <cmdline…>`. No -i, no
// -t: these are bookkeeping calls that must not touch the user's terminal,
// and they run concurrently with an interactive attach that owns it.
type CLIExecer struct{}

// Exec implements Execer.
func (CLIExecer) Exec(ctx context.Context, container string, cmdline []string) (string, int, error) {
	args := append([]string{"exec", container}, cmdline...)
	cmd := exec.CommandContext(ctx, "container", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return combineOutput(stdout.String(), stderr.String()), exitErr.ExitCode(), nil
	}
	if err != nil {
		return stdout.String(), -1, fmt.Errorf("container exec %s: %w: %s",
			container, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), 0, nil
}

// combineOutput implements Execer's non-zero-exit contract: stdout as-is,
// with stderr (trimmed) appended after it, separated by a newline only when
// both are non-empty.
func combineOutput(stdout, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	switch {
	case stderr == "":
		return stdout
	case stdout == "":
		return stderr
	default:
		return stdout + "\n" + stderr
	}
}

// Tmux drives the tmux server inside a sandbox from the host.
//
// Its job is small and specific: say whether tmux is there at all, list a
// session's clients, and detach one. Everything else about the session
// (creating it, attaching to it) rides the interactive argv from AttachArgv.
type Tmux struct {
	Exec Execer

	// PollEvery and PollFor bound the wait for a new client to appear after
	// an attach starts. Fields rather than constants so tests do not sleep.
	PollEvery time.Duration
	PollFor   time.Duration

	mu      sync.Mutex
	present map[string]bool
}

// NewTmux returns a Tmux wired to the real `container` CLI.
func NewTmux() *Tmux {
	return &Tmux{
		Exec:      CLIExecer{},
		PollEvery: 100 * time.Millisecond,
		PollFor:   10 * time.Second,
		present:   map[string]bool{},
	}
}

// Present reports whether the sandbox's image carries tmux.
//
// One `sh -c 'command -v tmux'` exec, memoized per container for the life of
// the process: an image cannot grow tmux while its container runs, and every
// attach and every pane would otherwise pay for the probe. A sandbox built
// from an image that predates this feature answers false, and callers fall
// back to a direct exec with a warning.
//
// The memoization is not single-flight: two goroutines racing to be the
// first to touch the same container can both miss the cache and each pay for
// one probe before either result is stored.
func (t *Tmux) Present(ctx context.Context, container string) bool {
	t.mu.Lock()
	if cached, ok := t.present[container]; ok {
		t.mu.Unlock()
		return cached
	}
	t.mu.Unlock()

	_, code, err := t.Exec.Exec(ctx, container, []string{"sh", "-c", "command -v tmux"})
	ok := err == nil && code == 0

	t.mu.Lock()
	t.present[container] = ok
	t.mu.Unlock()
	return ok
}

// ListClients returns the ttys currently attached to one tmux session.
//
// A session that does not exist yet — or a server that is not running,
// because nothing has attached since the sandbox booted — is not an error.
// The first attach creates both, and the snapshot taken just before it has to
// succeed or there is nothing to diff against.
func (t *Tmux) ListClients(ctx context.Context, container, session string) ([]string, error) {
	out, code, err := t.Exec.Exec(ctx, container,
		[]string{"tmux", "list-clients", "-t", session, "-F", "#{client_tty}"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, nil
	}
	var ttys []string
	for _, line := range strings.Split(out, "\n") {
		if tty := strings.TrimSpace(line); tty != "" {
			ttys = append(ttys, tty)
		}
	}
	return ttys, nil
}

// DetachClient ends one client's attachment to its session.
//
// This is the call that makes a closed window actually stop being attached:
// killing the host-side `container exec` never reaches the guest, and the
// tmux client it left behind stays attached indefinitely — measured still
// attached until an explicit detach-client was run from a fresh exec.
func (t *Tmux) DetachClient(ctx context.Context, container, tty string) error {
	out, code, err := t.Exec.Exec(ctx, container, []string{"tmux", "detach-client", "-t", tty})
	if err != nil {
		return err
	}
	if code != 0 {
		if out = strings.TrimSpace(out); out != "" {
			return fmt.Errorf("tmux detach-client -t %s in %s: exit %d: %s", tty, container, code, out)
		}
		return fmt.Errorf("tmux detach-client -t %s in %s: exit %d", tty, container, code)
	}
	return nil
}
