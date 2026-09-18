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
// A non-zero exit status is NOT an error: tmux uses it to say ordinary
// things like "no server running", and a `container exec` against an
// unreachable (missing or stopped) container surfaces the same way — a
// non-zero exit with the CLI's own message folded into the combined output
// — rather than as an error here. Only a transport failure (the CLI
// missing, the context cancelled: the command could not even be started)
// returns err. This layer does not try to read the difference between "the
// guest said no" and "the container could not be reached" out of that text;
// Present instead decides presence only from its probe's own yes/no output
// (see Present), so an unreachable container's exit just fails that
// yes/no check like any other unexpected answer would, rather than being a
// special case this interface has to recognize.
//
// The returned string is the command's stdout. When the command exits
// non-zero, its stderr (trimmed) is appended after stdout, separated by a
// newline when both are non-empty, so a caller that wants to know why a
// command failed (DetachClient, Present) can read it from there. This is
// safe for every caller in this package: ListClients only parses the string
// on exit 0, DetachClient reads it on failure, and Present reads it on
// either branch to decide yes/no or to explain a failed probe.
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
// One exec, memoized per container for the life of the process: an image
// cannot grow tmux while its container runs, and every attach and every pane
// would otherwise pay for the probe. The probe command is
// `command -v tmux >/dev/null 2>&1 && echo yes || echo no` — as long as the
// container is reachable, a shell running that always exits 0 and prints
// exactly "yes" or "no", so presence is decided from that output alone,
// never from the exit code and never by pattern-matching the `container`
// CLI's own error wording. An earlier version tried the latter — treating
// any non-zero exit as "tmux not found" — and silently misread a stopped or
// removed container's `container exec` failure as "no tmux", sending the
// caller down the no-tmux fallback while hiding the real problem (found by
// Task 9's manual verification, Step 13). Anything other than a clean
// "yes"/"no" — a non-zero exit (most often the container itself could not
// be reached: stopped, removed, or otherwise not running) or an exit 0 with
// output that is neither — fails the probe with an error instead of
// guessing, and is not cached. A sandbox built from an image that predates
// this feature answers "no" (a plain `command -v` miss), and callers fall
// back to a direct exec with a warning.
//
// A transport failure (the `container` CLI missing, the context cancelled —
// the command could not even be started) also returns a non-nil error and is
// never cached: only a decided answer — the probe actually ran and said yes
// or no — is worth remembering for the life of the process. Caching a
// transport hiccup, or an unreachable container's failed probe, as "no
// tmux" would wrongly force every later attach to this container down the
// no-tmux fallback path even once the transport (or the container) recovers.
//
// The memoization is not single-flight: two goroutines racing to be the
// first to touch the same container can both miss the cache and each pay for
// one probe before either result is stored.
func (t *Tmux) Present(ctx context.Context, container string) (bool, error) {
	t.mu.Lock()
	if cached, ok := t.present[container]; ok {
		t.mu.Unlock()
		return cached, nil
	}
	t.mu.Unlock()

	out, code, err := t.Exec.Exec(ctx, container,
		[]string{"sh", "-c", "command -v tmux >/dev/null 2>&1 && echo yes || echo no"})
	if err != nil {
		return false, err
	}

	trimmed := strings.TrimSpace(out)
	var present bool
	switch {
	case code == 0 && trimmed == "yes":
		present = true
	case code == 0 && trimmed == "no":
		present = false
	default:
		if trimmed != "" {
			return false, fmt.Errorf("tmux presence probe in %s: exit %d: %s", container, code, trimmed)
		}
		return false, fmt.Errorf("tmux presence probe in %s: exit %d", container, code)
	}

	t.mu.Lock()
	t.present[container] = present
	t.mu.Unlock()
	return present, nil
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

// ErrClientGone marks a DetachClient failure that means the client (and
// often the whole session or server) was already gone before the detach ran
// — not a real failure to report. This is the common case, not a rare one:
// when `claude` exits normally, tmux tears its session and client down
// before the host-side `container exec` that ran it even returns, so the
// detach this package runs afterward always finds them gone. Callers treat
// it as a successful detach.
var ErrClientGone = errors.New("tmux client already gone")

// clientGoneMarkers are the tmux error texts that mean "there was nothing
// left to detach" rather than a real failure. Matched as a substring of the
// combined stdout+stderr Execer hands back, case-sensitively — these are
// tmux's own fixed strings, not user input.
var clientGoneMarkers = []string{
	"can't find client",
	"no server running",
	"can't find session",
	"no such session",
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
		out = strings.TrimSpace(out)
		if clientAlreadyGone(out) {
			if out != "" {
				return fmt.Errorf("%w: %s", ErrClientGone, out)
			}
			return ErrClientGone
		}
		if out != "" {
			return fmt.Errorf("tmux detach-client -t %s in %s: exit %d: %s", tty, container, code, out)
		}
		return fmt.Errorf("tmux detach-client -t %s in %s: exit %d", tty, container, code)
	}
	return nil
}

// clientAlreadyGone reports whether a non-zero detach-client's output says
// the client (or its session, or the whole server) was already gone.
func clientAlreadyGone(out string) bool {
	for _, marker := range clientGoneMarkers {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}
