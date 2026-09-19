package cli

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
)

// What each dashboard action is allowed to take. These are ceilings, not
// expectations: `up` boots a microVM and can legitimately run for minutes,
// while a send is an HTTP round trip to a process on the same host.
const (
	cpDownTimeout      = 60 * time.Second
	cpSendTimeout      = 30 * time.Second
	cpInterruptTimeout = 30 * time.Second
	cpBrowserTimeout   = 90 * time.Second
	cpUpTimeout        = 10 * time.Minute
)

// cpActor implements controlplane.Actor by delegating to internal/control,
// which owns the one implementation of each action. Every action delegates;
// nothing is implemented here. Panes are the other seam — `paneHost`
// (controlplane_panes.go) — because opening one produces a live process the
// dashboard then owns. home is kept for the attach lock and the client
// records under ~/.cspace/controlplane/.
type cpActor struct {
	ctrl *control.Client
	home string
}

var _ controlplane.Actor = (*cpActor)(nil)

func newControlPlaneActor(ctrl *control.Client, home string) *cpActor {
	return &cpActor{ctrl: ctrl, home: home}
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
