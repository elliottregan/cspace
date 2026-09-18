package control

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// Down tears down a sandbox: container, sidecars, registry entry, and — as
// `cspace down`'s default does — its clone, sessions and volumes.
//
// The reporting rule is inherited verbatim from the dashboard. teardownSandbox
// has no error return and swallows the container Stop error; its only failure
// signal is "[cspace] warning: …" text, so any warning becomes this action's
// error. That over-reports benign cleanup noise as a failure — see
// .cspace/context/findings/2026-07-20-tui-down-reports-benign-teardown-warnings-as-failure.md
// — and is preserved here deliberately: fixing it means changing
// teardownSandbox's signature, which the CLI down path shares.
func (c *Client) Down(ctx context.Context, project, sandbox string) error {
	if c.host == nil {
		return ErrNoHost
	}
	var buf bytes.Buffer
	c.host.Teardown(ctx, project, sandbox, &buf, true /* wipeState */)
	if strings.Contains(buf.String(), "warning:") {
		return fmt.Errorf("%s", strings.TrimSpace(buf.String()))
	}
	return nil
}

// RestartBrowser restarts the project's shared browser sidecar and waits for
// it to answer again. The caller's context carries the deadline; the ladder
// itself applies its own restart budget on top.
func (c *Client) RestartBrowser(ctx context.Context, project string) error {
	if c.host == nil {
		return ErrNoHost
	}
	return c.host.RestartBrowser(ctx, project)
}
