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

	// The kind is checked before the probe: an unknown one has no command
	// whatever the probe answers, and the probe is an exec into the
	// container.
	if kind != controlplane.KindClaude && kind != controlplane.KindShell {
		return controlplane.Opened{}, fmt.Errorf("pane kind %v has no command", kind)
	}

	present, err := h.ctrl.Tmux().Present(ctx, row.Container)
	if err != nil {
		// Not knowing whether tmux is there is not the same as knowing it
		// isn't: a direct exec would hit the same failure a moment later.
		return controlplane.Opened{}, fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", row.Name, err)
	}

	spec := control.ShellAttach(row.Container, present)
	if kind == controlplane.KindClaude {
		spec = control.ClaudeAttach(row.Container, present)
	}
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return controlplane.Opened{}, err
	}

	// io.Discard: the warning's TEXT has nowhere to be read here — the
	// dashboard owns the screen and this runs off the UI goroutine — but
	// the boolean does, through Opened.Warning. beginAttachOrWarn
	// downgrades a bookkeeping failure to an inert attachment: no lock, no
	// record, and a Close that detaches nothing, so the tmux client this
	// pane is about to create stays attached inside the sandbox when the
	// tab closes AND the startup sweep has no record to find it by. That is
	// not something to swallow. home was resolved and hard-failed on at
	// `cspace tui` startup, so it is passed with a nil homeErr.
	att, degraded, err := beginAttachOrWarn(ctx, io.Discard, h.ctrl.Tmux(), h.home, nil,
		row.Project, row.Name, row.Container, spec.Session)
	if err != nil {
		return controlplane.Opened{}, err
	}

	var opts []pane.Option
	if forcesExtendedKeys(kind, spec.Session) {
		opts = append(opts, pane.ExtendedKeys())
	}
	p, err := pane.Open(pane.Command{Path: bin, Args: argv}, cols, rows, opts...)
	if err != nil {
		// The bookkeeping is already open; close it rather than strand a
		// lock and a record for a client that never appeared.
		_ = att.Close(ctx)
		return controlplane.Opened{}, err
	}

	return controlplane.Opened{Pane: p, Detach: att, Warning: openWarning(present, degraded)}, nil
}

// forcesExtendedKeys reports whether this pane has to encode its own CSI-u
// forms. A tmux client cannot negotiate the kitty keyboard protocol on the
// pane's behalf — it consumes the child's negotiation and never passes it
// on — so for a session pane the encoding has to be decided here instead.
// See pane.ExtendedKeys for what it does and does not change.
//
// Claude only, and only under tmux. A shell pane gains nothing and loses
// something: bash and zsh have no CSI-u decoder, so a forced form arrives
// as a beep plus the tail of the sequence typed onto the command line. A
// no-tmux fallback pane (empty session) runs its child directly, and that
// child negotiates for itself.
func forcesExtendedKeys(kind controlplane.Kind, session string) bool {
	return session != "" && kind == controlplane.KindClaude
}

// openWarning is the sticky footer line a pane carries when its open was
// less than clean. At most one: the footer is one line, and two facts
// competing for it is how both get truncated.
//
// No tmux wins when both fire. It is the worse of the two — without tmux
// there is no session to rejoin and no client to detach, so the bookkeeping
// that degraded had nothing to book anyway — and it is the only one that
// names a remedy.
func openWarning(tmuxPresent, bookkeepingDegraded bool) string {
	switch {
	case !tmuxPresent:
		return noTmuxWarning()
	case bookkeepingDegraded:
		return degradedAttachWarning()
	}
	return ""
}

// warningWidth is the widest a footer warning may be. The footer renders
// through fit(text, m.width), so anything past the window's width is cut —
// and the part that gets cut is the end, which is where a remedy naturally
// goes. 78 is a standard-width terminal less a margin, and it is deliberately
// stricter than the 120 the first cut of this assumed: Task 8 Step 14 found
// the remedy cut off at 120, and an 80-column window would have lost it from
// the replacement too.
const warningWidth = 78

// noTmuxWarning is what a pane opened without tmux carries. Both steps of
// the remedy are here — `cspace image build` alone does nothing for the
// sandbox in front of you, because the running container keeps the old
// image, and the rebuild's own output never says so. The sandbox is not
// named: its tab already does, and leaving it out is what makes this line a
// constant width instead of one that overflows at a descriptively named
// sandbox (CLAUDE.md asks agents for those).
func noTmuxWarning() string {
	return "no tmux: pane dies with the window. Fix: cspace image build, then down/up"
}

// degradedAttachWarning is what a pane whose attach bookkeeping failed
// carries: an unwritable ~/.cspace/controlplane, a full disk. The
// attachment is inert, so closing the tab detaches nothing and the startup
// sweep has no record to reap it by — the client stays inside the sandbox
// until something kills the session.
func degradedAttachWarning() string {
	return "attach bookkeeping unavailable (~/.cspace): tmux client stays attached"
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
