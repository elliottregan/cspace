package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ErrNoProjectRoot is returned when no project root can be resolved for the
// project a caller asked to boot. exec.Cmd treats an empty Dir as "inherit
// this process's cwd" — silently correct for a one-off CLI invocation,
// silently wrong for a long-lived control-plane process, which has no cwd of
// its own that means anything. New deliberately does not default ProjectRoot
// (no os.Getwd fallback): a caller that wants Up must say which project.
var ErrNoProjectRoot = errors.New("control: no project root")

// Up boots a sandbox for project by running this same cspace binary's `up`
// command from that project's root.
//
// `cspace up` is a cobra command wrapping a long boot flow and a Bubble Tea
// overlay, not a callable function, and internal/control must not import
// internal/cli — so control shells out to the binary it is already running
// inside, exactly as a person would. Everything the boot flow needs (config,
// devcontainer, compose) it reads from the directory it runs in.
func (c *Client) Up(ctx context.Context, project, sandbox string) error {
	root, err := c.projectRootFor(project)
	if err != nil {
		return err
	}
	exe, err := c.executable()
	if err != nil {
		return fmt.Errorf("resolve the running cspace binary: %w", err)
	}
	out, err := c.runCommand(ctx, root, exe, "up", sandbox)
	if err != nil {
		out = strings.TrimSpace(out)
		if out == "" {
			return fmt.Errorf("cspace up %s: %w", sandbox, err)
		}
		return fmt.Errorf("cspace up %s: %w (%s)", sandbox, err, out)
	}
	return nil
}

// projectRootFor resolves the directory `cspace up` must run in to boot a
// sandbox of project.
//
// The registry is the source of truth: every entry cspace up writes records
// the root it booted from (registry.Entry.ProjectRoot), so any existing
// sandbox of a project names that project's checkout — which is what lets
// one dashboard boot sandboxes for projects the process was never started
// in. Entries are scanned in sandbox-name order so the answer is
// deterministic when two checkouts of one project disagree.
//
// A project with no entry recording a root — nothing ever booted, or only
// entries written before the field existed — falls back to the launching
// process's own ProjectRoot, and only when that is the same project (or the
// Client never named one, the single-project case). The root is not checked
// for existence here: a stale one fails loudly in cspace up's own chdir,
// with a better message than this could invent.
func (c *Client) projectRootFor(project string) (string, error) {
	if c.entries != nil {
		if entries, err := c.entries.List(); err == nil {
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			for _, e := range entries {
				if e.Project == project && e.ProjectRoot != "" {
					return e.ProjectRoot, nil
				}
			}
		}
	}
	if c.projectRoot != "" && (c.project == "" || c.project == project) {
		return c.projectRoot, nil
	}
	return "", fmt.Errorf("%w for project %s: boot one of its sandboxes with `cspace up` from its checkout first", ErrNoProjectRoot, project)
}

// runHostCommand runs bin in dir and returns its combined output. The default
// for Client.runCommand.
func runHostCommand(ctx context.Context, dir, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	// Every Client.Up caller is headless — the daemon, the dashboard — so the
	// child must never inherit an interactive stdin: a prompt it printed (the
	// stale-image rebuild gate is the one that exists) would have no one to
	// answer it, and whatever it read would be this process's input.
	//
	// A nil Stdin already gives the child /dev/null, which `cspace up` reads
	// as non-interactive: its gate asks the kernel whether stdin is a tty
	// (cmd_up.go's isTerminal), and /dev/null is not. This is belt and
	// braces on top of that — an empty reader is an EOF nothing can mistake
	// for a terminal, because exec.Cmd allocates a real pipe for any
	// io.Reader that is not an *os.File.
	cmd.Stdin = bytes.NewReader(nil)

	err := cmd.Run()
	return buf.String(), err
}
