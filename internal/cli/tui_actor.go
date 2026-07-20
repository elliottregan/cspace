package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/tui"
)

// tuiActor implements tui.Actor against the real host: attach via
// tea.ExecProcess, down via teardownSandbox, send/interrupt/browser via HTTP
// and the browser restart ladder. Constructed by cmd_tui.go.
type tuiActor struct {
	adapter  *applecontainer.Adapter
	registry *registry.Registry
	home     string
	client   *http.Client
}

func newTUIActor(a *applecontainer.Adapter, r *registry.Registry, home string) *tuiActor {
	return &tuiActor{adapter: a, registry: r, home: home, client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *tuiActor) Attach(row tui.Row) tea.Cmd {
	bin, argv, err := attachArgs(row.Container)
	if err != nil {
		return func() tea.Msg { return tui.Result("attach", err) }
	}
	cmd := exec.Command(bin, argv[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return tui.Result("attach", err) })
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
	url, token := row.ControlURL, row.Token
	client := t.client
	return func() tea.Msg {
		body, _ := json.Marshal(map[string]string{"session": "primary", "text": text})
		req, err := http.NewRequest(http.MethodPost, url+"/send", bytes.NewReader(body))
		if err != nil {
			return tui.Result("send", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return tui.Result("send", doExpect2xx(client, req))
	}
}

func (t *tuiActor) Interrupt(row tui.Row) tea.Cmd {
	url, token := row.ControlURL, row.Token
	client := t.client
	return func() tea.Msg {
		req, err := http.NewRequest(http.MethodPost, url+"/interrupt", nil)
		if err != nil {
			return tui.Result("interrupt", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return tui.Result("interrupt", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		// A 409 "no active task" is not an error — the agent was simply idle.
		// Surface it as a benign (non-error) notice per the spec.
		if resp.StatusCode == http.StatusConflict {
			return tui.Result("interrupt", nil)
		}
		if resp.StatusCode/100 != 2 {
			return tui.Result("interrupt", fmt.Errorf("status %d: %s", resp.StatusCode, agentErrorText(body)))
		}
		return tui.Result("interrupt", nil)
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

// doExpect2xx runs req and returns nil on a 2xx, else an error carrying the
// server's error text (mirrors agentErrorText for a clean footer message).
func doExpect2xx(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, agentErrorText(body))
	}
	return nil
}
