package cli

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRegistryDaemonCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "registry-daemon",
		Short: "Manage the host cspace registry daemon (debugging / cleanup)",
		Long: `Most users never need this. cspace2-up auto-spawns the registry daemon and
it idle-exits after 30 minutes of no activity. These subcommands exist for
debugging and manual cleanup.`,
	}
	parent.AddCommand(newRegistryDaemonStatusCmd())
	parent.AddCommand(newRegistryDaemonStopCmd())
	return parent
}

func newRegistryDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print whether the registry daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get("http://127.0.0.1:6280/health")
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: not running")
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: running on 127.0.0.1:6280")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "registry-daemon: unexpected status %d\n", resp.StatusCode)
			}
			return nil
		},
	}
}

func newRegistryDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the registry daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("pkill", "-f", "cspace-registry-daemon").CombinedOutput()
			if err != nil && !strings.Contains(string(out), "no process") {
				// pkill returns 1 when no matches; not an error for our purposes.
				if !strings.Contains(err.Error(), "exit status 1") {
					return fmt.Errorf("pkill: %w (%s)", err, out)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "registry-daemon: stopped")
			return nil
		},
	}
}
