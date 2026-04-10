package cli

import (
	"github.com/elliottregan/cspace/internal/supervisor"
	"github.com/spf13/cobra"
)

func newRestartSupervisorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart-supervisor <instance>",
		Short:   "Restart agent supervisor (preserves workspace)",
		GroupID: "supervisor",
		Args:    cobra.ExactArgs(1),
		RunE:    runRestartSupervisor,
	}

	cmd.Flags().String("reason", "", "Why the restart is needed")

	return cmd
}

func runRestartSupervisor(cmd *cobra.Command, args []string) error {
	name := args[0]
	reason, _ := cmd.Flags().GetString("reason")

	return supervisor.RestartSupervisor(name, reason, cfg)
}
