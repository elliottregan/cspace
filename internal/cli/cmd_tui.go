package cli

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
	"github.com/elliottregan/cspace/internal/tui"
)

// daemonBaseURL is the host daemon's HTTP base (registry + health), matching
// daemonHTTPPort in cmd_daemon.go.
const daemonBaseURL = "http://127.0.0.1:6280"

func newTuiCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Full-screen dashboard of cspace containers with common actions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval < time.Second {
				interval = time.Second // floor
			}
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

			poller := tui.NewPoller(adapter, reg, daemonBaseURL, time.Now)
			actor := newTUIActor(adapter, reg, home)
			model := tui.NewModel(poller, actor, home, interval, time.Now)

			prog := tea.NewProgram(model, tea.WithAltScreen())
			_, err = prog.Run()
			return err
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "refresh interval (floored at 1s)")
	return cmd
}
