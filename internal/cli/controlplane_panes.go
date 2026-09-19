package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/pane"
)

// paneHost implements controlplane.PaneHost: it turns a row and a kind into
// a running child on a pty, and books the attach so the tmux client it
// creates can be detached again.
//
// This is where the three layers meet, and why it lives here: internal/pane
// must not know what a sandbox is, internal/controlplane must not import
// internal/cli, and the argv builder plus the attach bookkeeping are
// internal/control's. The seam is satisfied once, in the one package that is
// allowed to see all three.
type paneHost struct {
	ctrl *control.Client
	home string
}

var _ controlplane.PaneHost = (*paneHost)(nil)

func newPaneHost(ctrl *control.Client, home string) *paneHost {
	return &paneHost{ctrl: ctrl, home: home}
}

// Open starts one pane's child. It runs inside a tea.Cmd, never on the UI
// goroutine: the tmux probe is an exec into the container and BeginAttach
// may wait out another attach's lock.
func (h *paneHost) Open(ctx context.Context, kind controlplane.Kind, row control.Row, cols, rows int) (controlplane.Opened, error) {
	if kind == controlplane.KindHostShell {
		// No container, no tmux, no bookkeeping: this is the operator's own
		// shell, and it belongs to no sandbox.
		p, err := pane.Open(pane.HostShell(), cols, rows)
		if err != nil {
			return controlplane.Opened{}, err
		}
		return controlplane.Opened{Pane: p}, nil
	}

	if row.Container == "" {
		// A registered sandbox that is not booted has no container to exec
		// into. Say that, rather than let the probe fail with a
		// transport-shaped error about a name that was never going to answer.
		return controlplane.Opened{}, fmt.Errorf("sandbox %s has no container yet", row.Name)
	}

	present, err := h.ctrl.Tmux().Present(ctx, row.Container)
	if err != nil {
		// Not knowing whether tmux is there is not the same as knowing it
		// isn't: a direct exec would hit the same failure a moment later.
		return controlplane.Opened{}, fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", row.Name, err)
	}

	var spec control.AttachSpec
	switch kind {
	case controlplane.KindClaude:
		spec = control.ClaudeAttach(row.Container, present)
	case controlplane.KindShell:
		spec = control.ShellAttach(row.Container, present)
	default:
		return controlplane.Opened{}, fmt.Errorf("pane kind %v has no command", kind)
	}
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return controlplane.Opened{}, err
	}

	// io.Discard: a bookkeeping warning has nowhere to be read here — the
	// dashboard owns the screen and this runs off the UI goroutine.
	// beginAttachOrWarn still downgrades that failure to an inert
	// attachment. home was resolved and hard-failed on at `cspace tui`
	// startup, so it is passed with a nil homeErr.
	att, _, err := beginAttachOrWarn(ctx, io.Discard, h.ctrl.Tmux(), h.home, nil,
		row.Project, row.Name, row.Container, spec.Session)
	if err != nil {
		return controlplane.Opened{}, err
	}

	p, err := pane.Open(pane.Command{Path: bin, Args: argv}, cols, rows)
	if err != nil {
		// The bookkeeping is already open; close it rather than strand a
		// lock and a record for a client that never appeared.
		_ = att.Close(ctx)
		return controlplane.Opened{}, err
	}

	warning := ""
	if !present {
		warning = fmt.Sprintf(
			"%s has no tmux: this session will not survive the window, and its process keeps running inside the sandbox. Rebuild with `cspace image build`, then `cspace down %s && cspace up %s`.",
			row.Name, row.Name, row.Name)
	}
	return controlplane.Opened{Pane: p, Detach: att, Warning: warning}, nil
}

// Sweep reaps the client records of attaches whose host process is gone.
//
// SweepOutcome deliberately drops SweepResult.Kept: a kept record is nothing
// happening, and the dashboard has nothing to show for it.
func (h *paneHost) Sweep(ctx context.Context) (controlplane.SweepOutcome, error) {
	res, err := h.ctrl.SweepClientRecords(ctx)
	return controlplane.SweepOutcome{
		Detached: res.Detached,
		Deleted:  res.Deleted,
		Errors:   res.Errors,
	}, err
}
