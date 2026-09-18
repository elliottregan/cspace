package cli

import (
	"context"
	"io"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor by delegating to internal/control, which owns
// the one implementation of each action. Only Attach stays here: it needs
// tea.ExecProcess to hand the terminal to the child, which is a Bubble Tea
// concern and therefore not control's. home is kept for the attach lock and
// client records under ~/.cspace/controlplane/. Constructed by cmd_tui.go.
type tuiActor struct {
	ctrl *control.Client
	home string
}

func newTUIActor(ctrl *control.Client, home string) *tuiActor {
	return &tuiActor{ctrl: ctrl, home: home}
}

// Attach joins the sandbox's tmux session through the same control-plane path
// `cspace attach` uses, so the two share one session rather than running two
// claudes against one workspace. tea.ExecProcess suspends the dashboard and
// runs the exec in the foreground; its callback is where the detach happens.
func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	// The Present probe and BeginAttach run synchronously here, in
	// bubbletea's Update (internal/tui/model.go), before the tea.Cmd this
	// returns ever runs — the v1 dashboard is replaced in a later rollout
	// step, so bound them rather than redesign this path now: a wedged
	// container or flock contention must not freeze the whole dashboard.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	present, err := defaultTmux.Present(ctx, row.Container)
	if err != nil {
		cancel()
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	spec := control.ClaudeAttach(row.Container, present)
	bin, argv, err := control.AttachArgv(spec)
	if err != nil {
		cancel()
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	// io.Discard: Attach runs synchronously inside bubbletea's Update, with
	// the dashboard's alt-screen still owning the terminal — a direct
	// stderr write here would corrupt that rendering rather than being seen
	// (the same reason the no-tmux fallback prints nothing in this path
	// either). beginAttachOrWarn still downgrades a bookkeeping failure to
	// an inert attachment; it just has nowhere safe to say so. t.home was
	// already resolved (and hard-failed on, if unresolvable) at `cspace tui`
	// startup, so it is passed with a nil homeErr rather than re-resolved.
	att, _, err := beginAttachOrWarn(ctx, io.Discard, t.home, nil, row.Project, row.Name, row.Container, spec.Session)
	cancel()
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if closeErr := att.Close(closeCtx); closeErr != nil && execErr == nil {
			execErr = closeErr
		}
		return tui.Result("attach", execErr)
	})
}

func (t *tuiActor) Down(row tui.Row) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return tui.Result("down", ctrl.Down(ctx, project, name))
	}
}

func (t *tuiActor) Send(row tui.Row, text string) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("send", ctrl.Send(ctx, project, name, "", text))
	}
}

func (t *tuiActor) Interrupt(row tui.Row) tea.Cmd {
	ctrl, project, name := t.ctrl, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return tui.Result("interrupt", ctrl.Interrupt(ctx, project, name))
	}
}

func (t *tuiActor) RestartBrowser(row tui.Row) tea.Cmd {
	ctrl, project := t.ctrl, row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		return tui.Result("browser restart", ctrl.RestartBrowser(ctx, project))
	}
}
