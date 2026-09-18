package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNoProjectRoot is returned by Up when the Client was built with an empty
// Options.ProjectRoot. exec.Cmd treats an empty Dir as "inherit this
// process's cwd" — silently correct for a one-off CLI invocation, silently
// wrong for a long-lived control-plane process, which has no cwd of its own
// that means anything. New deliberately does not default ProjectRoot (no
// os.Getwd fallback): a caller that wants Up must say which project.
var ErrNoProjectRoot = errors.New("control: no ProjectRoot configured for Up")

// Up boots a sandbox by running this same cspace binary's `up` command from
// the project's root.
//
// `cspace up` is a cobra command wrapping a long boot flow and a Bubble Tea
// overlay, not a callable function, and internal/control must not import
// internal/cli — so control shells out to the binary it is already running
// inside, exactly as a person would. Everything the boot flow needs (config,
// devcontainer, compose) it reads from Options.ProjectRoot.
//
// Up is single-project by construction: it always boots into the one
// Options.ProjectRoot its Client was built with. `cspace up` derives the
// project it boots from its own cwd, and a registry entry carries no project
// root of its own for a running sandbox — so nothing today lets a control
// plane resolve, let alone Up, a sandbox belonging to a project other than
// this Client's own. See
// .cspace/context/findings/2026-09-18-registry-entries-do-not-record-a-project-root.md.
func (c *Client) Up(ctx context.Context, sandbox string) error {
	if c.projectRoot == "" {
		return ErrNoProjectRoot
	}
	exe, err := c.executable()
	if err != nil {
		return fmt.Errorf("resolve the running cspace binary: %w", err)
	}
	out, err := c.runCommand(ctx, c.projectRoot, exe, "up", sandbox)
	if err != nil {
		out = strings.TrimSpace(out)
		if out == "" {
			return fmt.Errorf("cspace up %s: %w", sandbox, err)
		}
		return fmt.Errorf("cspace up %s: %w (%s)", sandbox, err, out)
	}
	return nil
}

// runHostCommand runs bin in dir and returns its combined output. The default
// for Client.runCommand.
func runHostCommand(ctx context.Context, dir, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	// Stdin must not be nil: exec.Cmd then connects the child directly to
	// the opened /dev/null *os.File, which is a character device — and
	// cmd_up.go's isStdinTTY() treats any character device as a terminal.
	// A Client.Up caller is always headless (the daemon, the TUI), so a
	// stray prompt (e.g. the stale-image rebuild gate) must see EOF on a
	// pipe, not what looks like an interactive terminal. Any io.Reader that
	// is not an *os.File makes exec.Cmd allocate a real os.Pipe instead.
	cmd.Stdin = bytes.NewReader(nil)

	err := cmd.Run()
	return buf.String(), err
}
