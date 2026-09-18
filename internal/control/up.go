package control

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Up boots a sandbox by running this same cspace binary's `up` command from
// the project's root.
//
// `cspace up` is a cobra command wrapping a long boot flow and a Bubble Tea
// overlay, not a callable function, and internal/control must not import
// internal/cli — so control shells out to the binary it is already running
// inside, exactly as a person would. Everything the boot flow needs (config,
// devcontainer, compose) it reads from Options.ProjectRoot.
func (c *Client) Up(ctx context.Context, sandbox string) error {
	exe, err := c.executable()
	if err != nil {
		return fmt.Errorf("resolve the running cspace binary: %w", err)
	}
	out, err := c.runCommand(ctx, c.projectRoot, exe, "up", sandbox)
	if err != nil {
		return fmt.Errorf("cspace up %s: %w (%s)", sandbox, err, strings.TrimSpace(out))
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
	err := cmd.Run()
	return buf.String(), err
}
