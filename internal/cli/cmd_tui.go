package cli

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/config"
	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/controlplane"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// daemonBaseURL is the host daemon's HTTP base (registry + health), matching
// daemonHTTPPort in cmd_daemon.go.
const daemonBaseURL = "http://127.0.0.1:6280"

func newTuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Full-screen dashboard of cspace containers with common actions",
		Long: `Full-screen dashboard of every cspace container on this host, grouped by
project: lifecycle and agent state per sandbox, its labeled URLs, its
compose sidecars and the project's shared browser.

Actions follow the selection — open a Claude pane, a shell or the
supervisor's events, send a turn, interrupt, tear down, boot, restart the
browser sidecar. A pane runs inside the window: every key goes to it
except the leader, ctrl+space, whose second keys move between tabs and
open and close them. Press ? for the full binding list; bindings come
from tui.keys in ~/.cspace/config.json.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			regPath, err := registry.DefaultPath()
			if err != nil {
				return fmt.Errorf("resolve registry path: %w", err)
			}
			reg := &registry.Registry{Path: regPath}
			adapter := applecontainer.New()

			// cfg is nil when `cspace tui` runs outside a project (root.go
			// tolerates that for this command): the dashboard spans every
			// project on the host, and Up resolves each one's root from the
			// registry. Project/ProjectRoot only back the fallback for a
			// project that has never been booted.
			project, projectRoot := "", ""
			if cfg != nil {
				project, projectRoot = projectName(), cfg.ProjectRoot
			}

			ctrl := control.New(control.Options{
				Containers:  adapter,
				Entries:     reg,
				Host:        newCLIHost(adapter, reg),
				DaemonURL:   daemonBaseURL,
				Home:        home,
				Now:         time.Now,
				Project:     project,
				ProjectRoot: projectRoot,
			})

			userCfg, err := config.LoadUser(home)
			if err != nil {
				return fmt.Errorf("load %s: %w", config.UserConfigPath(home), err)
			}

			model := controlplane.New(ctrl,
				newControlPlaneActor(ctrl, home),
				newPaneHost(ctrl, home),
				controlplane.NewKeyMap(userCfg.TUI.Keys))
			_, err = tea.NewProgram(model).Run()
			return err
		},
	}
}
