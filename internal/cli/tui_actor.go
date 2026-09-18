package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor against the real host. Supervisor traffic
// goes through internal/control so the CLI and the dashboard share one
// implementation; attach stays here because it needs tea.ExecProcess to hand
// the terminal to the child, and down still calls teardownSandbox directly.
// home is kept for the attach lock and client records under
// ~/.cspace/controlplane/. Constructed by cmd_tui.go.
type tuiActor struct {
	ctrl     *control.Client
	adapter  *applecontainer.Adapter
	registry *registry.Registry
	home     string
}

func newTUIActor(ctrl *control.Client, a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor {
	return &tuiActor{ctrl: ctrl, adapter: a, registry: r, home: home}
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
	adapter, reg, project, name := t.adapter, t.registry, row.Project, row.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var buf bytes.Buffer
		teardownSandbox(ctx, adapter, reg, project, name, &buf, true /* wipeState */)
		// teardownSandbox has no return value and swallows the container Stop
		// error; its only failure signal is warning text written to the
		// captured writer (prefix "[cspace] warning:"). Surface those warnings
		// instead of a false "down ok".
		if strings.Contains(buf.String(), "warning:") {
			return tui.Result("down", fmt.Errorf("%s", strings.TrimSpace(buf.String())))
		}
		return tui.Result("down", nil)
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

// RestartBrowser restarts the project's shared browser sidecar via the same
// seam the daemon's restart handler uses. Empty plVersion lets the ladder pin
// the version from the running sidecar (sidecarVersion) or fall back to
// defaultPlaywrightVersion. Uses restartBrowserFn (var-seam) so tests can fake it.
func (t *tuiActor) RestartBrowser(row tui.Row) tea.Cmd {
	project := row.Project
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_, err := restartBrowserFn(ctx, project, "")
		return tui.Result("browser restart", err)
	}
}
