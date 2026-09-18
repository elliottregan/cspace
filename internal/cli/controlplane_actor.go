package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

// What each dashboard action is allowed to take. These are ceilings, not
// expectations: `up` boots a microVM and can legitimately run for minutes,
// while a send is an HTTP round trip to a process on the same host.
const (
	// attachProbeTimeout bounds the tmux presence probe alone, exactly as
	// `cspace attach` bounds its own (cmd_attach.go). attachBookkeepingTimeout
	// bounds BeginAttach, and is separate and larger on purpose: BeginAttach
	// waits up to control's attachLockWait (tm.PollFor + 2s = 12s) for a
	// concurrent attach's window to clear, and that wait has to be allowed to
	// run out. One shared 10s deadline across both would let a slow probe eat
	// the lock's budget and lose a race `cspace attach` would win.
	attachProbeTimeout       = 10 * time.Second
	attachBookkeepingTimeout = 20 * time.Second
	attachDetachTimeout      = 15 * time.Second
	cpDownTimeout            = 60 * time.Second
	cpSendTimeout            = 30 * time.Second
	cpInterruptTimeout       = 30 * time.Second
	cpBrowserTimeout         = 90 * time.Second
	cpUpTimeout              = 10 * time.Minute
)

// cpActor implements controlplane.Actor by delegating to internal/control,
// which owns the one implementation of each action. Only attach stays here:
// it hands the terminal to a child, which is a Bubble Tea concern and
// therefore not control's. home is kept for the attach lock and the client
// records under ~/.cspace/controlplane/.
type cpActor struct {
	ctrl *control.Client
	home string
}

var _ controlplane.Actor = (*cpActor)(nil)

func newControlPlaneActor(ctrl *control.Client, home string) *cpActor {
	return &cpActor{ctrl: ctrl, home: home}
}

// Attach joins the sandbox's tmux session through the same control-plane
// path `cspace attach` uses, so the two share one session rather than
// running two claudes against one workspace.
//
// Everything it does — the tmux probe, the attach bookkeeping, the child,
// the detach — happens inside attachExec.Run, which bubbletea calls only
// after it has released the terminal. Nothing runs on the UI goroutine: the
// v1 dashboard probed inside Update and froze on a wedged container.
func (a *cpActor) Attach(row control.Row) tea.Cmd {
	ex := a.attachCommand(row)
	// The callback closes over the same exec the program ran, which is how
	// Run's no-tmux finding reaches the footer: tea.Exec calls fn after
	// Run returns, on that same goroutine, so the read is ordered.
	return tea.Exec(ex, func(err error) tea.Msg { return attachResult(ex, err) })
}

// attachCommand builds the ExecCommand. Split out because tea.Exec's own
// message is opaque, so this is the only way a test can run the real thing.
func (a *cpActor) attachCommand(row control.Row) *attachExec {
	return &attachExec{ctrl: a.ctrl, home: a.home, row: row}
}

// attachResult maps the exec's outcome onto the dashboard's action result.
//
// Spec, Error handling: a sandbox whose image has no tmux falls back to the
// direct `claude` exec "with a footer warning naming cspace image build".
// The attach itself succeeded, so this is a warning and not an error — but
// a sticky one, because what it says is that the session the person just
// left did not survive.
func attachResult(ex *attachExec, err error) tea.Msg {
	if err == nil && ex.noTmux {
		return controlplane.ResultWarn(controlplane.LabelAttach, fmt.Sprintf(
			"%s has no tmux: that session did not survive this window, and claude may still be running inside it. Rebuild with `cspace image build`, then `cspace down %s && cspace up %s`.",
			ex.row.Name, ex.row.Name, ex.row.Name))
	}
	return controlplane.Result(controlplane.LabelAttach, err)
}

// attachExec is the tea.ExecCommand the dashboard suspends for. bubbletea
// sets its streams to the program's own, runs it with the terminal released,
// and restores the UI afterwards.
type attachExec struct {
	ctrl *control.Client
	home string
	row  control.Row

	// noTmux is set by Run when the probe found no tmux in the image, and
	// read by attachResult afterwards — the warning has nowhere to go while
	// the child owns the terminal.
	noTmux bool

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (a *attachExec) SetStdin(r io.Reader)  { a.stdin = r }
func (a *attachExec) SetStdout(w io.Writer) { a.stdout = w }
func (a *attachExec) SetStderr(w io.Writer) { a.stderr = w }

// Run probes for tmux, opens the bookkeeping, runs `container exec` in the
// foreground, and detaches the tmux client it created.
//
// The detach is the step that makes closing the session actually end the
// guest-side client: a host-side exit never reaches tmux, which would
// otherwise keep the client attached indefinitely.
func (a *attachExec) Run() error {
	// The child gets cspace's own terminal descriptors and `container exec
	// -it` hands them back non-blocking, which silently truncates the first
	// repaint bubbletea writes after this returns. Give them back the way
	// they were lent out, on every exit path.
	defer restoreBlockingStreams(a.stdin, a.stdout, a.stderr)

	if a.row.Container == "" {
		// A row can reach here with no container when its sandbox is
		// registered but not booted — refuse before the probe names a
		// container that was never going to answer, so the footer says
		// what's actually wrong instead of a transport-shaped error.
		return fmt.Errorf("sandbox %s has no container yet", a.row.Name)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), attachProbeTimeout)
	present, err := a.ctrl.Tmux().Present(probeCtx, a.row.Container)
	probeCancel()
	if err != nil {
		// A transport error means we could not learn whether tmux is there,
		// not that it isn't — a direct exec would hit the same failure a
		// moment later, so there is nothing useful to fall back to.
		return fmt.Errorf("cannot reach sandbox %s to probe for tmux: %w", a.row.Name, err)
	}
	// An image built before tmux still attaches, but the session will not
	// survive this window and `claude` keeps running inside the sandbox
	// afterwards. Record it so attachResult can put the warning in the
	// footer once the dashboard has the screen back — `cspace attach`
	// prints the same thing to stderr, and the dashboard must not be the
	// one place this goes unsaid.
	a.noTmux = !present
	spec := control.ClaudeAttach(a.row.Container, present)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		return err
	}
	// The bookkeeping gets its own, larger deadline: BeginAttach may wait
	// out a concurrent attach's lock (control's attachLockWait, 12s), which
	// the probe's 10s could not have covered.
	//
	// io.Discard: the dashboard is between screens and the child is about
	// to paint over everything, so a bookkeeping warning has nowhere to be
	// read. beginAttachOrWarn still downgrades that failure to an inert
	// attachment; it just cannot say so here. home was resolved — and hard
	// failed on — at `cspace tui` startup, so it is passed with a nil
	// homeErr rather than re-resolved.
	bookCtx, bookCancel := context.WithTimeout(context.Background(), attachBookkeepingTimeout)
	att, _, err := beginAttachOrWarn(bookCtx, io.Discard, a.ctrl.Tmux(), a.home, nil,
		a.row.Project, a.row.Name, a.row.Container, spec.Session)
	bookCancel()
	if err != nil {
		return err
	}

	// Unlike runAttachChild (cmd_attach.go), this exec forwards no signals:
	// bubbletea's Program owns the terminal, and its own signal handling,
	// for as long as this Run is in flight — exactly as it did under the v1
	// dashboard's tea.ExecProcess. If the host terminal itself closes
	// mid-attach, the child goes with it and this attachment's record is
	// left behind for the startup sweep rollout step 4 adds, rather than the
	// SIGHUP-triggered detach cmd_attach.go's own signal handling gives
	// itself.
	child := exec.Command(bin, argv[1:]...)
	child.Stdin, child.Stdout, child.Stderr = a.stdin, a.stdout, a.stderr
	runErr := attachRunErr(child.Run())

	// The detach gets its own context: whatever ended the session may have
	// cancelled everything else, and this is the one thing that must still
	// run while the container is reachable.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), attachDetachTimeout)
	defer closeCancel()
	if closeErr := att.Close(closeCtx); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	return runErr
}

// attachRunErr normalizes the foreground child's exit the same way
// runAttachChild (cmd_attach.go) treats `cspace attach`'s own: a child that
// started, ran, and exited non-zero is not an attach failure — the session
// happened and ended on its own terms (the person typed `exit`, `claude`
// crashed, whatever) — only a failure to start or run it at all is. Without
// this, attachResult would report an ordinary session end as
// Result(LabelAttach, "exit status N") and, worse, silently drop the no-tmux
// warning: attachResult only warns when err == nil.
func attachRunErr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func (a *cpActor) Down(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpDownTimeout)
		defer cancel()
		return controlplane.Result(controlplane.LabelDown, ctrl.Down(ctx, project, name))
	}
}

func (a *cpActor) Send(row control.Row, text string) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpSendTimeout)
		defer cancel()
		return controlplane.Result(controlplane.LabelSend, ctrl.Send(ctx, project, name, "", text))
	}
}

func (a *cpActor) Interrupt(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpInterruptTimeout)
		defer cancel()
		return controlplane.Result(controlplane.LabelInterrupt, ctrl.Interrupt(ctx, project, name))
	}
}

func (a *cpActor) RestartBrowser(row control.Row) tea.Cmd {
	ctrl, project := a.ctrl, row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpBrowserTimeout)
		defer cancel()
		return controlplane.Result(controlplane.LabelBrowserRestart, ctrl.RestartBrowser(ctx, project))
	}
}

// Up boots the selected row's sandbox. control.Up resolves the project's
// checkout from the registry, so this works for any project on screen and
// not just the one `cspace tui` was started in.
//
// The action gate holds for the whole boot — minutes, on a cold image — with
// the footer's spinner as the only progress. `cspace up`'s own overlay is
// not reachable from here: the child is headless. Rollout step 4's panes are
// what make a long boot watchable.
func (a *cpActor) Up(row control.Row) tea.Cmd {
	ctrl, project, name := a.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpUpTimeout)
		defer cancel()
		return controlplane.Result(controlplane.LabelUp, ctrl.Up(ctx, project, name))
	}
}
